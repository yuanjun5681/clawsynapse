package discovery

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	announceSubject   = "clawsynapse.discovery.global.announce"
	departSubject     = "clawsynapse.discovery.global.depart"
	maxDiscoveryAge   = 2 * time.Minute
	maxFutureSkew     = 30 * time.Second
	maxDiscoveryBytes = 32 * 1024
	maxMetadataBytes  = 8 * 1024
	maxReplayBots     = 4096
	maxReplayMessages = 16384
)

var (
	errAnnouncePublicKeyRequired = errors.New("publicKey is required")
	errAnnounceDIDRequired       = errors.New("did is required")
	errAnnounceDIDMismatch       = errors.New("did does not match publicKey")
	errAnnounceBotIDMismatch     = errors.New("nodeId does not match did")
)

type Service struct {
	log             *slog.Logger
	bus             *natsbus.Client
	peers           *Registry
	store           *store.FSStore
	botID           string
	did             string
	publicKey       string
	ttl             time.Duration
	heartbeat       time.Duration
	trustMode       string
	agentProduct    string
	agentVersion    string
	harnessAdapter  string
	harnessModel    string
	displayName     string
	labels          types.Labels
	autoAuth        func(context.Context, string) error
	authMu          sync.Mutex
	authing         map[string]struct{}
	authCtx         context.Context
	authWorkers     sync.WaitGroup
	stopping        bool
	id              *identity.Identity
	discoveryMu     sync.Mutex
	latest          map[string]discoveryClaim
	seen            map[discoveryMessage]int64
	nextPruneMs     int64
	publishMu       sync.Mutex
	lastPublishedTs int64
	workers         sync.WaitGroup
}

type discoveryClaim struct {
	ts        int64
	expiresAt int64
}

type discoveryMessage struct {
	botID string
	id    string
}

// AnnounceConfig contains public declarations covered by the discovery signature.
// Harness fields describe configuration only, never execution health or a Run.
type AnnounceConfig struct {
	Identity       *identity.Identity
	AgentProduct   string
	AgentVersion   string
	HarnessAdapter string
	HarnessModel   string
	DisplayName    string
	Labels         types.Labels
}

func NewService(log *slog.Logger, bus *natsbus.Client, peers *Registry, fs *store.FSStore, botID string, did string, publicKey string, heartbeat, ttl time.Duration, trustMode string, announceConfigs ...AnnounceConfig) *Service {
	announce := AnnounceConfig{
		AgentProduct: "clawsynapse",
		AgentVersion: "v0.1.0",
		Labels:       types.Labels{}.WithCaps([]string{"chat", "tools"}),
	}
	if len(announceConfigs) > 0 {
		configured := announceConfigs[0]
		announce.Identity = configured.Identity
		announce.HarnessAdapter = configured.HarnessAdapter
		announce.HarnessModel = configured.HarnessModel
		if configured.AgentProduct != "" {
			announce.AgentProduct = configured.AgentProduct
		}
		if configured.AgentVersion != "" {
			announce.AgentVersion = configured.AgentVersion
		}
		if configured.DisplayName != "" {
			announce.DisplayName = configured.DisplayName
		}
		if len(configured.Labels) > 0 {
			announce.Labels = configured.Labels
		}
	}
	return &Service{
		log:            log,
		bus:            bus,
		peers:          peers,
		store:          fs,
		botID:          botID,
		did:            did,
		publicKey:      publicKey,
		ttl:            ttl,
		heartbeat:      heartbeat,
		trustMode:      trustMode,
		agentProduct:   announce.AgentProduct,
		agentVersion:   announce.AgentVersion,
		harnessAdapter: announce.HarnessAdapter,
		harnessModel:   announce.HarnessModel,
		displayName:    announce.DisplayName,
		labels:         announce.Labels,
		authing:        map[string]struct{}{},
		id:             announce.Identity,
		latest:         map[string]discoveryClaim{},
		seen:           map[discoveryMessage]int64{},
	}
}

func (s *Service) SetAutoAuthenticator(fn func(context.Context, string) error) {
	s.autoAuth = fn
}

func (s *Service) Start(ctx context.Context) error {
	if err := s.validateLocalIdentity(); err != nil {
		return err
	}
	if s.heartbeat <= 0 || s.ttl <= 0 {
		return errors.New("discovery heartbeat and TTL must be positive")
	}
	s.authMu.Lock()
	s.authCtx = ctx
	s.authMu.Unlock()
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
	s.workers.Add(1)

	go func() {
		defer s.workers.Done()
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

// Wait joins the cancellation/depart loop before the owning Bot closes NATS.
func (s *Service) Wait() {
	s.authMu.Lock()
	s.stopping = true
	s.authMu.Unlock()
	s.workers.Wait()
	s.authWorkers.Wait()
}

func (s *Service) publishAnnounce() error {
	msg, err := s.newAnnounce()
	if err != nil {
		return err
	}
	if err := s.bus.PublishJSON(announceSubject, msg); err != nil {
		return err
	}
	s.log.Debug("announce published", logging.Event("peer.announce.sent"), logging.Subject(announceSubject), slog.Int64("ttlMs", msg.TTLms))
	return nil
}

func (s *Service) newAnnounce() (protocol.DiscoveryAnnounce, error) {
	ttl := s.ttl
	if ttl > maxDiscoveryAge {
		ttl = maxDiscoveryAge
	}
	// Normalize labels before signing so the signed wire value is
	// canonical (set members deduplicated and sorted).
	labels, err := types.NormalizeLabels(s.labels)
	if err != nil {
		return protocol.DiscoveryAnnounce{}, fmt.Errorf("normalize announce labels: %w", err)
	}
	msg := protocol.DiscoveryAnnounce{
		ProtocolVersion: "v2",
		MessageID:       randID(),
		MessageType:     "discovery.announce",
		BotID:           s.botID,
		DID:             s.did,
		Version:         s.agentVersion,
		AgentProduct:    s.agentProduct,
		DisplayName:     strings.TrimSpace(s.displayName),
		Labels:          labels,
		Inbox:           "clawsynapse.msg." + s.botID + ".inbox",
		PublicKey:       s.publicKey,
		Ts:              s.nextTimestamp(),
		TTLms:           ttl.Milliseconds(),
	}
	if s.harnessAdapter != "" || s.harnessModel != "" {
		msg.Metadata = make(map[string]any, 2)
		if s.harnessAdapter != "" {
			msg.Metadata["harnessAdapter"] = s.harnessAdapter
		}
		if s.harnessModel != "" {
			msg.Metadata["harnessModel"] = s.harnessModel
		}
	}
	signature, err := s.sign(announceSubject, msg)
	msg.Signature = signature
	return msg, err
}

func (s *Service) publishDepart(reason string) error {
	msg := protocol.DiscoveryDepart{
		ProtocolVersion: "v2",
		MessageID:       randID(),
		MessageType:     "discovery.depart",
		BotID:           s.botID,
		Reason:          reason,
		Ts:              s.nextTimestamp(),
	}
	signature, err := s.sign(departSubject, msg)
	if err != nil {
		return err
	}
	msg.Signature = signature
	return s.bus.PublishJSON(departSubject, msg)
}

// PublishDeparture announces that a Bot is leaving without requiring a
// running discovery Service, so it remains usable during shutdown.
func PublishDeparture(bus *natsbus.Client, id *identity.Identity, botID, reason string) error {
	if bus == nil {
		return errors.New("nats client is required")
	}
	if id == nil {
		return errors.New("identity is required")
	}
	msg, err := newDepartureMessage(id, botID, reason, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return bus.PublishJSON(departSubject, msg)
}

func newDepartureMessage(id *identity.Identity, botID, reason string, ts int64) (protocol.DiscoveryDepart, error) {
	msg := protocol.DiscoveryDepart{
		ProtocolVersion: "v2",
		MessageID:       randID(),
		MessageType:     "discovery.depart",
		BotID:           botID,
		Reason:          reason,
		Ts:              ts,
	}
	signed, err := protocol.SignedBytes(departSubject, msg)
	if err != nil {
		return protocol.DiscoveryDepart{}, err
	}
	msg.Signature = identity.Sign(id.PrivateKey, signed)
	return msg, nil
}

func (s *Service) handleAnnounce(subject string, data []byte) {
	var msg protocol.DiscoveryAnnounce
	if subject != announceSubject || len(data) > maxDiscoveryBytes {
		return
	}
	if err := protocol.DecodeStrict(data, &msg); err != nil {
		s.log.Warn("decode announce failed", logging.Error(err))
		return
	}
	now := time.Now().UnixMilli()
	if msg.BotID == s.botID || msg.ProtocolVersion != "v2" || msg.MessageType != "discovery.announce" ||
		!validMessageID(msg.MessageID) || msg.TTLms <= 0 || msg.TTLms > maxDiscoveryAge.Milliseconds() ||
		!validDiscoveryTime(msg.Ts, now, msg.TTLms) {
		return
	}
	if err := validateAnnounceIdentity(msg); err != nil {
		s.log.Warn("reject invalid announce identity", logging.Peer(msg.BotID), logging.Error(err))
		return
	}
	if !validAnnounceAttributes(msg) {
		return
	}
	pub, err := identity.DecodePublicKey(msg.PublicKey)
	if err != nil {
		return
	}
	signed, err := protocol.SignedBytes(subject, msg)
	if err != nil || !identity.Verify(pub, signed, msg.Signature) {
		return
	}

	// Serialise freshness checks and mutations across both subscription callbacks.
	s.discoveryMu.Lock()
	existing, ok := s.peers.Get(msg.BotID)
	if ok {
		if known, _ := existing.Metadata["publicKey"].(string); known != "" && known != msg.PublicKey {
			s.discoveryMu.Unlock()
			return
		}
	}
	if !s.acceptClaim(msg.BotID, msg.MessageID, msg.Ts, now) {
		s.discoveryMu.Unlock()
		return
	}
	trustStatus := existing.TrustStatus
	if !ok {
		trustStatus = s.persistedTrustStatus(msg.BotID)
	}
	harnessAdapter, _ := msg.Metadata["harnessAdapter"].(string)
	harnessModel, _ := msg.Metadata["harnessModel"].(string)
	peer := types.Peer{
		BotID:          msg.BotID,
		DID:            msg.DID,
		Version:        msg.Version,
		AgentProduct:   msg.AgentProduct,
		HarnessAdapter: harnessAdapter,
		HarnessModel:   harnessModel,
		DisplayName:    msg.DisplayName,
		Labels:         msg.Labels,
		Inbox:          msg.Inbox,
		TrustStatus:    trustStatus,
		LastSeenMs:     now,
		Metadata: map[string]any{
			"publicKey": msg.PublicKey,
			"ttlMs":     msg.TTLms,
		},
	}
	// Remote metadata never supplies local authentication or trust decisions.
	existing, ok = s.peers.refreshDiscovery(peer)
	s.discoveryMu.Unlock()
	authStatus := types.AuthSeen
	if ok {
		authStatus = existing.AuthStatus
		trustStatus = existing.TrustStatus
	}
	if !ok {
		s.log.Info("peer discovered",
			logging.Event("peer.discovered"),
			logging.Peer(msg.BotID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			logging.TrustStatus(trustStatus),
			logging.AuthStatus(authStatus),
		)
	} else if existing.Inbox != msg.Inbox || existing.Version != msg.Version || existing.AgentProduct != msg.AgentProduct {
		s.log.Info("peer refreshed",
			logging.Event("peer.refreshed"),
			logging.Peer(msg.BotID),
			slog.String("inbox", msg.Inbox),
			slog.String("version", msg.Version),
			slog.String("agentProduct", msg.AgentProduct),
			logging.TrustStatus(trustStatus),
			logging.AuthStatus(authStatus),
		)
	} else {
		s.log.Debug("peer heartbeat received",
			logging.Event("peer.heartbeat.received"),
			logging.Peer(msg.BotID),
			slog.Int64("ts", msg.Ts),
		)
	}

	s.maybeAutoAuthenticate(msg.BotID)
}

func (s *Service) persistedTrustStatus(botID string) string {
	if s.store == nil {
		return types.TrustNone
	}

	st, err := s.store.LoadTrustState()
	if err != nil {
		s.log.Warn("load trust state failed", logging.Peer(botID), logging.Error(err))
		return types.TrustNone
	}

	for _, peer := range st.Trusted {
		if peer.BotID == botID {
			return types.TrustTrusted
		}
	}
	for _, peer := range st.Pending {
		if peer.From == botID || peer.To == botID {
			return types.TrustPending
		}
	}
	for _, peer := range st.Rejected {
		if peer.BotID == botID {
			return types.TrustRejected
		}
	}
	for _, peer := range st.Revoked {
		if peer.BotID == botID {
			return types.TrustRevoked
		}
	}

	return types.TrustNone
}

func (s *Service) maybeAutoAuthenticate(botID string) {
	if s.autoAuth == nil || s.trustMode == "open" {
		return
	}

	peer, ok := s.peers.Get(botID)
	if !ok {
		return
	}
	if peer.TrustStatus != types.TrustTrusted || peer.AuthStatus == types.AuthAuthenticated || peer.AuthStatus == types.AuthPending {
		return
	}

	s.authMu.Lock()
	if s.stopping {
		s.authMu.Unlock()
		return
	}
	if _, exists := s.authing[botID]; exists {
		s.authMu.Unlock()
		return
	}
	s.authing[botID] = struct{}{}
	authCtx := s.authCtx
	if authCtx == nil {
		authCtx = context.Background()
	}
	s.authWorkers.Add(1)
	s.authMu.Unlock()
	s.log.Info("starting automatic authentication",
		logging.Event("auth.auto.start"),
		logging.Peer(botID),
	)

	go func() {
		defer s.clearAutoAuth(botID)
		defer s.authWorkers.Done()

		ctx, cancel := context.WithTimeout(authCtx, 10*time.Second)
		defer cancel()

		if err := s.autoAuth(ctx, botID); err != nil {
			s.log.Warn("auto auth challenge failed", logging.Peer(botID), logging.Error(err))
		}
	}()
}

func (s *Service) clearAutoAuth(botID string) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	delete(s.authing, botID)
}

func (s *Service) handleDepart(subject string, data []byte) {
	var msg protocol.DiscoveryDepart
	if subject != departSubject || len(data) > maxDiscoveryBytes {
		return
	}
	if err := protocol.DecodeStrict(data, &msg); err != nil {
		s.log.Warn("decode depart failed", logging.Error(err))
		return
	}
	now := time.Now().UnixMilli()
	if msg.BotID == s.botID || msg.ProtocolVersion != "v2" || msg.MessageType != "discovery.depart" ||
		!validMessageID(msg.MessageID) || len(msg.Reason) > 1024 ||
		!validDiscoveryTime(msg.Ts, now, maxDiscoveryAge.Milliseconds()) {
		return
	}
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	peer, ok := s.peers.Get(msg.BotID)
	if !ok {
		return
	}
	known, _ := peer.Metadata["publicKey"].(string)
	pub, err := identity.DecodePublicKey(known)
	if err != nil || identity.DeriveBotID(identity.DeriveBotDID(pub)) != msg.BotID {
		return
	}
	signed, err := protocol.SignedBytes(subject, msg)
	if err != nil || !identity.Verify(pub, signed, msg.Signature) {
		return
	}
	if !s.acceptClaim(msg.BotID, msg.MessageID, msg.Ts, now) {
		return
	}
	s.peers.Remove(msg.BotID)
	s.log.Info("peer departed",
		logging.Event("peer.departed"),
		logging.Peer(msg.BotID),
		slog.String("reason", msg.Reason),
	)
}

func (s *Service) gc() {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	now := time.Now().UnixMilli()
	s.pruneClaims(now)
	for _, peer := range s.peers.List() {
		if peer.BotID == s.botID {
			continue
		}
		ttlMs := metadataInt64(peer.Metadata, "ttlMs")
		if ttlMs <= 0 || ttlMs > maxDiscoveryAge.Milliseconds() {
			ttlMs = maxDiscoveryAge.Milliseconds()
		}
		if now-peer.LastSeenMs > ttlMs {
			s.peers.Remove(peer.BotID)
			s.log.Info("peer expired",
				logging.Event("peer.expired"),
				logging.Peer(peer.BotID),
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

	expectedDID := identity.DeriveBotDID(pub)
	if msg.DID != expectedDID {
		return errAnnounceDIDMismatch
	}

	expectedBotID := identity.DeriveBotID(expectedDID)
	if msg.BotID != expectedBotID {
		return errAnnounceBotIDMismatch
	}

	return nil
}

func (s *Service) validateLocalIdentity() error {
	if s.id == nil || len(s.id.PrivateKey) != ed25519.PrivateKeySize || len(s.id.PublicKey) != ed25519.PublicKeySize {
		return errors.New("discovery requires an Ed25519 signing identity")
	}
	derived := ed25519.NewKeyFromSeed(s.id.PrivateKey.Seed())
	if !bytes.Equal(derived, s.id.PrivateKey) || !bytes.Equal(derived.Public().(ed25519.PublicKey), s.id.PublicKey) {
		return errors.New("discovery identity key pair does not match")
	}
	did := identity.DeriveBotDID(s.id.PublicKey)
	if s.did != did || s.botID != identity.DeriveBotID(did) ||
		s.publicKey != base64.RawURLEncoding.EncodeToString(s.id.PublicKey) {
		return errors.New("discovery identity does not match local Bot")
	}
	return nil
}

func (s *Service) sign(subject string, msg any) (string, error) {
	if err := s.validateLocalIdentity(); err != nil {
		return "", err
	}
	signed, err := protocol.SignedBytes(subject, msg)
	if err != nil {
		return "", err
	}
	return identity.Sign(s.id.PrivateKey, signed), nil
}

func (s *Service) nextTimestamp() int64 {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	now := time.Now().UnixMilli()
	if now <= s.lastPublishedTs {
		now = s.lastPublishedTs + 1
	}
	s.lastPublishedTs = now
	return now
}

func validMessageID(id string) bool {
	return id != "" && len(id) <= 128
}

func validDiscoveryTime(ts, now, maxAge int64) bool {
	return ts > 0 && ts <= now+maxFutureSkew.Milliseconds() && ts >= now-maxAge
}

func validAnnounceAttributes(msg protocol.DiscoveryAnnounce) bool {
	if msg.Inbox != "clawsynapse.msg."+msg.BotID+".inbox" || len(msg.Version) > 256 ||
		len(msg.AgentProduct) > 256 || len(msg.Metadata) > 64 {
		return false
	}
	if err := types.ValidateDisplayName(msg.DisplayName); err != nil {
		return false
	}
	if _, err := types.NormalizeLabels(msg.Labels); err != nil {
		return false
	}
	metadata, err := json.Marshal(msg.Metadata)
	return err == nil && len(metadata) <= maxMetadataBytes
}

// acceptClaim is called with discoveryMu held. Unexpired entries are never
// evicted to make room: saturation fails closed instead of reopening replay.
func (s *Service) acceptClaim(botID, messageID string, ts, now int64) bool {
	s.pruneClaims(now)
	key := discoveryMessage{botID: botID, id: messageID}
	if _, seen := s.seen[key]; seen {
		return false
	}
	latest, exists := s.latest[botID]
	if exists && ts <= latest.ts {
		return false
	}
	if (!exists && len(s.latest) >= maxReplayBots) || len(s.seen) >= maxReplayMessages {
		return false
	}
	// Keep depart tombstones until every previously valid claim is too old.
	expiresAt := now + maxDiscoveryAge.Milliseconds() + maxFutureSkew.Milliseconds()
	s.latest[botID] = discoveryClaim{ts: ts, expiresAt: expiresAt}
	s.seen[key] = expiresAt
	return true
}

func (s *Service) pruneClaims(now int64) {
	if now < s.nextPruneMs {
		return
	}
	s.nextPruneMs = now + time.Second.Milliseconds()
	for botID, claim := range s.latest {
		if claim.expiresAt < now {
			delete(s.latest, botID)
		}
	}
	for key, expiresAt := range s.seen {
		if expiresAt < now {
			delete(s.seen, key)
		}
	}
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
