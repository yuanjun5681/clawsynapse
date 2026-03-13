package discovery

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

const (
	announceSubject = "clawsynapse.discovery.global.announce"
	departSubject   = "clawsynapse.discovery.global.depart"
)

type Service struct {
	log       *slog.Logger
	bus       *natsbus.Client
	peers     *Registry
	store     *store.FSStore
	nodeID    string
	publicKey string
	ttl       time.Duration
	heartbeat time.Duration
	trustMode string
	autoAuth  func(context.Context, string) error
	authMu    sync.Mutex
	authing   map[string]struct{}
}

func NewService(log *slog.Logger, bus *natsbus.Client, peers *Registry, fs *store.FSStore, nodeID string, publicKey string, heartbeat, ttl time.Duration, trustMode string) *Service {
	return &Service{
		log:       log,
		bus:       bus,
		peers:     peers,
		store:     fs,
		nodeID:    nodeID,
		publicKey: publicKey,
		ttl:       ttl,
		heartbeat: heartbeat,
		trustMode: trustMode,
		authing:   map[string]struct{}{},
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
					s.log.Warn("publish announce failed", slog.String("error", err.Error()))
				}
			case <-gcTicker.C:
				s.gc()
			}
		}
	}()

	return nil
}

func (s *Service) publishAnnounce() error {
	msg := protocol.DiscoveryAnnounce{
		MessageID:    randID(),
		MessageType:  "discovery.announce",
		NodeID:       s.nodeID,
		Version:      "v0.1.0",
		AgentProduct: "clawsynapse",
		Capabilities: []string{"chat", "tools"},
		Inbox:        "clawsynapse.msg." + s.nodeID + ".inbox",
		PublicKey:    s.publicKey,
		Ts:           time.Now().UnixMilli(),
		TTLms:        s.ttl.Milliseconds(),
	}
	if err := s.bus.PublishJSON(announceSubject, msg); err != nil {
		return err
	}
	s.log.Debug("announce published", slog.String("event", "peer.announce.sent"), slog.String("subject", announceSubject), slog.Int64("ttlMs", msg.TTLms))
	return nil
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
		s.log.Warn("decode announce failed", slog.String("error", err.Error()))
		return
	}
	if msg.NodeID == s.nodeID {
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
						s.log.Warn("peer public key mismatch detected", slog.String("peer", msg.NodeID), slog.String("mode", s.trustMode))
						authStatus = types.AuthRejected
						metadata["publicKey"] = known
					}
				}
			}
		}
	}

	s.peers.Upsert(types.Peer{
		NodeID:       msg.NodeID,
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
			slog.String("event", "peer.discovered"),
			slog.String("peer", msg.NodeID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			slog.String("trustStatus", trustStatus),
			slog.String("authStatus", authStatus),
		)
	} else if existing.Inbox != msg.Inbox || existing.Version != msg.Version || existing.AgentProduct != msg.AgentProduct {
		s.log.Info("peer refreshed",
			slog.String("event", "peer.refreshed"),
			slog.String("peer", msg.NodeID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			slog.String("trustStatus", trustStatus),
			slog.String("authStatus", authStatus),
		)
	} else {
		s.log.Debug("peer heartbeat received",
			slog.String("event", "peer.heartbeat.received"),
			slog.String("peer", msg.NodeID),
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
		s.log.Warn("load trust state failed", slog.String("peer", nodeID), slog.String("error", err.Error()))
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
		slog.String("event", "auth.auto.start"),
		slog.String("peer", nodeID),
	)

	go func() {
		defer s.clearAutoAuth(nodeID)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.autoAuth(ctx, nodeID); err != nil {
			s.log.Warn("auto auth challenge failed", slog.String("peer", nodeID), slog.String("error", err.Error()))
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
		s.log.Warn("decode depart failed", slog.String("error", err.Error()))
		return
	}
	if msg.NodeID == s.nodeID {
		return
	}
	s.peers.Remove(msg.NodeID)
	s.log.Info("peer departed",
		slog.String("event", "peer.departed"),
		slog.String("peer", msg.NodeID),
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
				slog.String("event", "peer.expired"),
				slog.String("peer", peer.NodeID),
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

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
