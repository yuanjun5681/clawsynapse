package discovery

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"clawsynapse/internal/identity"
	"clawsynapse/internal/logging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

const (
	announceSubject         = "clawsynapse.discovery.global.announce"
	departSubject           = "clawsynapse.discovery.global.depart"
	legacyAnnounceLogWindow = 5 * time.Minute
)

var (
	errAnnouncePublicKeyRequired = errors.New("publicKey is required")
	errAnnounceDIDRequired       = errors.New("did is required")
	errAnnounceDIDMismatch       = errors.New("did does not match publicKey")
	errAnnounceNodeIDMismatch    = errors.New("nodeId does not match did")
)

type Service struct {
	log                *slog.Logger
	bus                *natsbus.Client
	peers              *Registry
	store              *store.FSStore
	nodeID             string
	did                string
	publicKey          string
	ttl                time.Duration
	heartbeat          time.Duration
	trustMode          string
	agentProduct       string
	agentVersion       string
	capabilities       []string
	autoAuth           func(context.Context, string) error
	authMu             sync.Mutex
	authing            map[string]struct{}
	legacyAnnounceMu   sync.Mutex
	legacyAnnounceSeen map[string]time.Time
}

type AnnounceConfig struct {
	AgentProduct string
	AgentVersion string
	Capabilities []string
}

func NewService(log *slog.Logger, bus *natsbus.Client, peers *Registry, fs *store.FSStore, nodeID string, did string, publicKey string, heartbeat, ttl time.Duration, trustMode string, announceConfigs ...AnnounceConfig) *Service {
	announce := AnnounceConfig{
		AgentProduct: "clawsynapse",
		AgentVersion: "v0.1.0",
		Capabilities: []string{"chat", "tools"},
	}
	if len(announceConfigs) > 0 {
		configured := announceConfigs[0]
		if configured.AgentProduct != "" {
			announce.AgentProduct = configured.AgentProduct
		}
		if configured.AgentVersion != "" {
			announce.AgentVersion = configured.AgentVersion
		}
		if len(configured.Capabilities) > 0 {
			announce.Capabilities = configured.Capabilities
		}
	}
	return &Service{
		log:                log,
		bus:                bus,
		peers:              peers,
		store:              fs,
		nodeID:             nodeID,
		did:                did,
		publicKey:          publicKey,
		ttl:                ttl,
		heartbeat:          heartbeat,
		trustMode:          trustMode,
		agentProduct:       announce.AgentProduct,
		agentVersion:       announce.AgentVersion,
		capabilities:       append([]string(nil), announce.Capabilities...),
		authing:            map[string]struct{}{},
		legacyAnnounceSeen: map[string]time.Time{},
	}
}

func (s *Service) SetAutoAuthenticator(fn func(context.Context, string) error) {
	s.autoAuth = fn
}

func (s *Service) Start(ctx context.Context) error {
	if _, err := s.bus.Subscribe(announceSubject, s.handleAnnounce); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe(departSubject, s.handleDepart); err != nil {
		return err
	}

	if err := s.publishAnnounce(); err != nil {
		return err
	}

	heartbeatTicker := time.NewTicker(s.heartbeat)
	gcTicker := time.NewTicker(time.Second)

	go func() {
		defer heartbeatTicker.Stop()
		defer gcTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.publishDepart("shutdown")
				return
			case <-heartbeatTicker.C:
				if err := s.publishAnnounce(); err != nil {
					s.log.Warn("publish announce failed", logging.Error(err))
				}
			case <-gcTicker.C:
				s.gc()
			}
		}
	}()

	return nil
}

func (s *Service) publishAnnounce() error {
	msg := s.newAnnounce()
	if err := s.bus.PublishJSON(announceSubject, msg); err != nil {
		return err
	}
	s.log.Debug("announce published", logging.Event("peer.announce.sent"), logging.Subject(announceSubject), slog.Int64("ttlMs", msg.TTLms))
	return nil
}

func (s *Service) newAnnounce() protocol.DiscoveryAnnounce {
	return protocol.DiscoveryAnnounce{
		MessageID:    randID(),
		MessageType:  "discovery.announce",
		NodeID:       s.nodeID,
		DID:          s.did,
		Version:      s.agentVersion,
		AgentProduct: s.agentProduct,
		Capabilities: append([]string(nil), s.capabilities...),
		Inbox:        "clawsynapse.msg." + s.nodeID + ".inbox",
		PublicKey:    s.publicKey,
		Ts:           time.Now().UnixMilli(),
		TTLms:        s.ttl.Milliseconds(),
	}
}

func (s *Service) publishDepart(reason string) error {
	msg := protocol.DiscoveryDepart{
		MessageID:   randID(),
		MessageType: "discovery.depart",
		NodeID:      s.nodeID,
		Reason:      reason,
		Ts:          time.Now().UnixMilli(),
	}
	return s.bus.PublishJSON(departSubject, msg)
}

func (s *Service) handleAnnounce(_ string, data []byte) {
	var msg protocol.DiscoveryAnnounce
	if err := json.Unmarshal(data, &msg); err != nil {
		s.log.Warn("decode announce failed", logging.Error(err))
		return
	}
	if msg.NodeID == s.nodeID {
		return
	}
	if err := validateAnnounceIdentity(msg); err != nil {
		s.logInvalidAnnounceIdentity(msg, err)
		return
	}

	authStatus := types.AuthSeen
	trustStatus := s.persistedTrustStatus(msg.NodeID)
	metadata := map[string]any{
		"publicKey": msg.PublicKey,
		"ttlMs":     msg.TTLms,
	}
	existing, ok := s.peers.Get(msg.NodeID)
	if ok {
		authStatus = existing.AuthStatus
		trustStatus = existing.TrustStatus
		if existing.Metadata != nil {
			if known, ok := existing.Metadata["publicKey"].(string); ok && known != "" {
				if s.trustMode == "tofu" || s.trustMode == "explicit" {
					if msg.PublicKey != "" && known != msg.PublicKey {
						s.log.Warn("peer public key mismatch detected", logging.Peer(msg.NodeID), slog.String("mode", s.trustMode))
						authStatus = types.AuthRejected
						metadata["publicKey"] = known
					}
				}
			}
		}
	}

	s.peers.Upsert(types.Peer{
		NodeID:       msg.NodeID,
		DID:          msg.DID,
		Version:      msg.Version,
		AgentProduct: msg.AgentProduct,
		Capabilities: msg.Capabilities,
		Inbox:        msg.Inbox,
		AuthStatus:   authStatus,
		TrustStatus:  trustStatus,
		LastSeenMs:   msg.Ts,
		Metadata:     metadata,
	})
	if !ok {
		s.log.Info("peer discovered",
			logging.Event("peer.discovered"),
			logging.Peer(msg.NodeID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			logging.TrustStatus(trustStatus),
			logging.AuthStatus(authStatus),
		)
	} else if existing.Inbox != msg.Inbox || existing.Version != msg.Version || existing.AgentProduct != msg.AgentProduct {
		s.log.Info("peer refreshed",
			logging.Event("peer.refreshed"),
			logging.Peer(msg.NodeID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			logging.TrustStatus(trustStatus),
			logging.AuthStatus(authStatus),
		)
	} else {
		s.log.Debug("peer heartbeat received",
			logging.Event("peer.heartbeat.received"),
			logging.Peer(msg.NodeID),
			slog.Int64("ts", msg.Ts),
		)
	}

	s.maybeAutoAuthenticate(msg.NodeID)
}

func (s *Service) persistedTrustStatus(nodeID string) string {
	if s.store == nil {
		return types.TrustNone
	}

	st, err := s.store.LoadTrustState()
	if err != nil {
		s.log.Warn("load trust state failed", logging.Peer(nodeID), logging.Error(err))
		return types.TrustNone
	}

	for _, peer := range st.Trusted {
		if peer.NodeID == nodeID {
			return types.TrustTrusted
		}
	}
	for _, peer := range st.Pending {
		if peer.From == nodeID || peer.To == nodeID {
			return types.TrustPending
		}
	}
	for _, peer := range st.Rejected {
		if peer.NodeID == nodeID {
			return types.TrustRejected
		}
	}
	for _, peer := range st.Revoked {
		if peer.NodeID == nodeID {
			return types.TrustRevoked
		}
	}

	return types.TrustNone
}

func (s *Service) maybeAutoAuthenticate(nodeID string) {
	if s.autoAuth == nil || s.trustMode == "open" {
		return
	}

	peer, ok := s.peers.Get(nodeID)
	if !ok {
		return
	}
	if peer.TrustStatus != types.TrustTrusted || peer.AuthStatus == types.AuthAuthenticated || peer.AuthStatus == types.AuthPending {
		return
	}

	s.authMu.Lock()
	if _, exists := s.authing[nodeID]; exists {
		s.authMu.Unlock()
		return
	}
	s.authing[nodeID] = struct{}{}
	s.authMu.Unlock()
	s.log.Info("starting automatic authentication",
		logging.Event("auth.auto.start"),
		logging.Peer(nodeID),
	)

	go func() {
		defer s.clearAutoAuth(nodeID)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.autoAuth(ctx, nodeID); err != nil {
			s.log.Warn("auto auth challenge failed", logging.Peer(nodeID), logging.Error(err))
		}
	}()
}

func (s *Service) clearAutoAuth(nodeID string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	delete(s.authing, nodeID)
}

func (s *Service) handleDepart(_ string, data []byte) {
	var msg protocol.DiscoveryDepart
	if err := json.Unmarshal(data, &msg); err != nil {
		s.log.Warn("decode depart failed", logging.Error(err))
		return
	}
	if msg.NodeID == s.nodeID {
		return
	}
	s.peers.Remove(msg.NodeID)
	s.log.Info("peer departed",
		logging.Event("peer.departed"),
		logging.Peer(msg.NodeID),
		slog.String("reason", msg.Reason),
	)
}

func (s *Service) gc() {
	now := time.Now().UnixMilli()
	for _, peer := range s.peers.List() {
		if peer.NodeID == s.nodeID {
			continue
		}
		ttlMs := metadataInt64(peer.Metadata, "ttlMs")
		if ttlMs <= 0 {
			ttlMs = s.ttl.Milliseconds()
		}
		if now-peer.LastSeenMs > ttlMs {
			s.peers.Remove(peer.NodeID)
			s.log.Info("peer expired",
				logging.Event("peer.expired"),
				logging.Peer(peer.NodeID),
				slog.Int64("lastSeenMs", peer.LastSeenMs),
				slog.Int64("ttlMs", ttlMs),
			)
		}
	}
}

func metadataInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func validateAnnounceIdentity(msg protocol.DiscoveryAnnounce) error {
	if msg.PublicKey == "" {
		return errAnnouncePublicKeyRequired
	}
	if msg.DID == "" {
		return errAnnounceDIDRequired
	}

	pub, err := identity.DecodePublicKey(msg.PublicKey)
	if err != nil {
		return fmt.Errorf("decode publicKey: %w", err)
	}

	expectedDID := identity.DeriveNodeDID(pub)
	if msg.DID != expectedDID {
		return errAnnounceDIDMismatch
	}

	expectedNodeID := identity.DeriveNodeID(expectedDID)
	if msg.NodeID != expectedNodeID {
		return errAnnounceNodeIDMismatch
	}

	return nil
}

func (s *Service) logInvalidAnnounceIdentity(msg protocol.DiscoveryAnnounce, err error) {
	attrs := []any{
		logging.Peer(msg.NodeID),
		slog.String("did", msg.DID),
		logging.Error(err),
	}

	if errors.Is(err, errAnnounceDIDRequired) {
		if !s.shouldLogLegacyAnnounce(msg.NodeID, time.Now()) {
			return
		}
		s.log.Info("reject legacy announce without did", attrs...)
		return
	}

	s.log.Warn("reject invalid announce identity", attrs...)
}

func (s *Service) shouldLogLegacyAnnounce(peer string, now time.Time) bool {
	s.legacyAnnounceMu.Lock()
	defer s.legacyAnnounceMu.Unlock()

	last, ok := s.legacyAnnounceSeen[peer]
	if ok && now.Sub(last) < legacyAnnounceLogWindow {
		return false
	}
	s.legacyAnnounceSeen[peer] = now
	return true
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
