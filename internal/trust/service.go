package trust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/logging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

type Service struct {
	mu               sync.Mutex
	log              *slog.Logger
	peers            *discovery.Registry
	bus              *natsbus.Client
	store            *store.FSStore
	botID            string
	identity         *identity.Identity
	trustAutoApprove bool
	publishJSON      func(string, any) error
	state            store.TrustState
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, fs *store.FSStore, botID string, id *identity.Identity, trustAutoApprove bool) (*Service, error) {
	st, err := fs.LoadTrustState()
	if err != nil {
		return nil, err
	}

	s := &Service{
		log:              log,
		peers:            peers,
		bus:              bus,
		store:            fs,
		botID:            botID,
		identity:         id,
		trustAutoApprove: trustAutoApprove,
		state:            st,
	}
	if bus != nil {
		s.publishJSON = bus.PublishJSON
	} else {
		s.publishJSON = func(string, any) error {
			return errors.New("nats client is closed")
		}
	}

	s.syncPeerTrustStates()
	return s, nil
}

func (s *Service) Start() error {
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.botID+".request", s.handleTrustRequest); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.botID+".response", s.handleTrustResponse); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.botID+".revoke", s.handleTrustRevoke); err != nil {
		return err
	}
	return nil
}

func (s *Service) Request(_ context.Context, targetBot, reason string, capabilities []string) (string, error) {
	if targetBot == "" {
		return "", errors.New("targetBot is required")
	}
	peer, ok := s.peers.Get(targetBot)
	if !ok {
		return "", errors.New("target peer not found")
	}
	if peer.AuthStatus != types.AuthAuthenticated {
		return "", errors.New("target peer must be authenticated before trust request")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasPendingLocked(targetBot, "outbound") {
		return "", protocol.NewError(protocol.ErrTrustAlreadyPending, "trust request is already pending")
	}
	if peer.TrustStatus == types.TrustTrusted {
		return "", protocol.NewError(protocol.ErrTrustAlreadyTrusted, "peer already trusted")
	}

	requestID := randID()
	now := time.Now().UnixMilli()
	req := protocol.TrustRequest{
		MessageID:    randID(),
		MessageType:  "trust.request",
		From:         s.botID,
		To:           targetBot,
		RequestID:    requestID,
		Reason:       reason,
		Capabilities: capabilities,
		Ts:           now,
	}
	req.Signature = s.signTrustRequest(req)

	if err := s.publishJSON("clawsynapse.trust."+targetBot+".request", req); err != nil {
		return "", err
	}

	s.state.Pending = append(s.state.Pending, store.TrustPendingState{
		RequestID:    requestID,
		From:         s.botID,
		To:           targetBot,
		Direction:    "outbound",
		Reason:       reason,
		ReceivedAtMs: now,
	})
	if err := s.persistLocked(); err != nil {
		return "", err
	}
	_ = s.peers.SetTrustStatus(targetBot, types.TrustPending)
	return requestID, nil
}

func (s *Service) Pending() []store.TrustPendingState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.TrustPendingState, len(s.state.Pending))
	copy(out, s.state.Pending)
	return out
}

func (s *Service) Approve(requestID, reason string) error {
	return s.respond(requestID, "approve", reason)
}

func (s *Service) Reject(requestID, reason string) error {
	return s.respond(requestID, "reject", reason)
}

func (s *Service) Revoke(targetBot, reason string) error {
	if targetBot == "" {
		return errors.New("targetBot is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	rev := protocol.TrustRevoke{
		MessageID:   randID(),
		MessageType: "trust.revoke",
		From:        s.botID,
		To:          targetBot,
		Reason:      reason,
		Ts:          now,
	}
	rev.Signature = s.signTrustRevoke(rev)

	if err := s.publishJSON("clawsynapse.trust."+targetBot+".revoke", rev); err != nil {
		return err
	}

	s.removeFromTrustedLocked(targetBot)
	s.upsertPeerStateLocked(&s.state.Revoked, targetBot, now, reason)
	s.removePendingByBotLocked(targetBot)
	if err := s.persistLocked(); err != nil {
		return err
	}
	_ = s.peers.SetTrustStatus(targetBot, types.TrustRevoked)
	return nil
}

// RevokeAll withdraws every trust relationship and rejects inbound requests.
// Deletion is best-effort: failures are reported as warnings, never as an
// error, so one unreachable peer cannot block local cleanup.
func (s *Service) RevokeAll(reason string) (revoked []string, warnings []string) {
	s.mu.Lock()
	targets := make([]string, 0, len(s.state.Trusted)+len(s.state.Pending))
	seen := make(map[string]bool, len(s.state.Trusted)+len(s.state.Pending))
	for _, t := range s.state.Trusted {
		if !seen[t.BotID] {
			seen[t.BotID] = true
			targets = append(targets, t.BotID)
		}
	}
	type inboundRequest struct {
		requestID string
		from      string
	}
	var inbound []inboundRequest
	for _, p := range s.state.Pending {
		switch p.Direction {
		case "outbound":
			if !seen[p.To] {
				seen[p.To] = true
				targets = append(targets, p.To)
			}
		case "inbound":
			inbound = append(inbound, inboundRequest{requestID: p.RequestID, from: p.From})
		}
	}
	s.mu.Unlock()

	// Reject inbound requests first: Revoke also drops every pending request
	// for the same Bot, which would make a later Reject fail.
	for _, req := range inbound {
		if err := s.Reject(req.requestID, reason); err != nil {
			warnings = append(warnings, fmt.Sprintf("reject trust request %s from %s: %v", req.requestID, req.from, err))
		}
	}
	for _, target := range targets {
		if err := s.Revoke(target, reason); err != nil {
			warnings = append(warnings, fmt.Sprintf("revoke trust for %s: %v", target, err))
			continue
		}
		revoked = append(revoked, target)
	}
	return revoked, warnings
}

// RemovePeer erases every local trace of a Bot: trust state, pending requests,
// its peer registry entry, and persisted local remarks.
func (s *Service) RemovePeer(botID string) error {
	if botID == "" {
		return errors.New("botID is required")
	}

	s.mu.Lock()
	s.removeFromTrustedLocked(botID)
	s.removeFromRejectedLocked(botID)
	s.removeFromRevokedLocked(botID)
	s.removePendingByBotLocked(botID)
	err := s.persistLocked()
	s.mu.Unlock()

	if s.peers != nil {
		s.peers.Remove(botID)
		s.peers.SetLocalAttrs(botID, types.PeerLocalAttrs{})
	}
	if s.store != nil {
		err = errors.Join(err, s.removePeerAttrs(botID))
	}
	return err
}

func (s *Service) removePeerAttrs(botID string) error {
	attrs, err := s.store.LoadPeerAttrsState()
	if err != nil {
		return err
	}
	if _, ok := attrs.Peers[botID]; !ok {
		return nil
	}
	delete(attrs.Peers, botID)
	return s.store.SavePeerAttrsState(attrs)
}

func (s *Service) handleTrustRequest(subject string, data []byte) {
	var req protocol.TrustRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.log.Warn("decode trust request failed", logging.Subject(subject), logging.Error(err))
		return
	}
	if req.To != s.botID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: req.MessageType, To: req.To, Ts: req.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust request", logging.Error(err))
		return
	}

	peerPub, err := s.peerPublicKey(req.From)
	if err != nil {
		s.log.Warn("trust request peer key unavailable", logging.Peer(req.From), logging.Error(err))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustRequestSignatureInput(req)), req.Signature) {
		s.log.Warn("invalid trust request signature", logging.Peer(req.From))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasRequestIDLocked(req.RequestID) {
		return
	}

	now := time.Now().UnixMilli()
	pending := store.TrustPendingState{
		RequestID:    req.RequestID,
		From:         req.From,
		To:           req.To,
		Direction:    "inbound",
		Reason:       req.Reason,
		ReceivedAtMs: now,
	}
	s.state.Pending = append(s.state.Pending, pending)

	if s.trustAutoApprove {
		reason := "auto-approved trust request"
		if err := s.applyTrustDecisionLocked(pending, "approve", reason, now); err != nil {
			s.log.Warn("auto approve trust request failed", logging.Peer(req.From), logging.RequestID(req.RequestID), logging.Error(err))
		} else {
			if err := s.persistLocked(); err != nil {
				s.log.Warn("persist auto approved trust failed", logging.Error(err))
				return
			}
			s.log.Info("trust request auto approved",
				logging.Event("trust.request.auto_approved"),
				logging.Peer(req.From),
				logging.RequestID(req.RequestID),
			)
			return
		}
	}

	if err := s.persistLocked(); err != nil {
		s.log.Warn("persist trust pending failed", logging.Error(err))
		return
	}
	_ = s.peers.SetTrustStatus(req.From, types.TrustPending)
	s.log.Info("trust request received",
		logging.Event("trust.request.received"),
		logging.Peer(req.From),
		logging.RequestID(req.RequestID),
	)
}

func (s *Service) handleTrustResponse(subject string, data []byte) {
	var resp protocol.TrustResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		s.log.Warn("decode trust response failed", logging.Subject(subject), logging.Error(err))
		return
	}
	if resp.To != s.botID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: resp.MessageType, To: resp.To, Ts: resp.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust response", logging.Error(err))
		return
	}

	peerPub, err := s.peerPublicKey(resp.From)
	if err != nil {
		s.log.Warn("trust response peer key unavailable", logging.Peer(resp.From), logging.Error(err))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustResponseSignatureInput(resp)), resp.Signature) {
		s.log.Warn("invalid trust response signature", logging.Peer(resp.From))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.findPendingByRequestIDLocked(resp.RequestID)
	if !ok {
		return
	}
	if p.Direction != "outbound" {
		return
	}

	s.removePendingByRequestIDLocked(resp.RequestID)
	now := time.Now().UnixMilli()
	if resp.Decision == "approve" {
		s.upsertPeerStateLocked(&s.state.Trusted, resp.From, now, resp.Reason)
		s.removeFromRejectedLocked(resp.From)
		s.removeFromRevokedLocked(resp.From)
		_ = s.peers.SetTrustStatus(resp.From, types.TrustTrusted)
	} else {
		s.upsertPeerStateLocked(&s.state.Rejected, resp.From, now, resp.Reason)
		s.removeFromTrustedLocked(resp.From)
		_ = s.peers.SetTrustStatus(resp.From, types.TrustRejected)
	}
	if err := s.persistLocked(); err != nil {
		s.log.Warn("persist trust response failed", logging.Error(err))
	}
	s.log.Info("trust response applied",
		logging.Event("trust.response.applied"),
		logging.Peer(resp.From),
		logging.RequestID(resp.RequestID),
		slog.String("decision", resp.Decision),
	)
}

func (s *Service) handleTrustRevoke(subject string, data []byte) {
	var rev protocol.TrustRevoke
	if err := json.Unmarshal(data, &rev); err != nil {
		s.log.Warn("decode trust revoke failed", logging.Subject(subject), logging.Error(err))
		return
	}
	if rev.To != s.botID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: rev.MessageType, To: rev.To, Ts: rev.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust revoke", logging.Error(err))
		return
	}

	peerPub, err := s.peerPublicKey(rev.From)
	if err != nil {
		s.log.Warn("trust revoke peer key unavailable", logging.Peer(rev.From), logging.Error(err))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustRevokeSignatureInput(rev)), rev.Signature) {
		s.log.Warn("invalid trust revoke signature", logging.Peer(rev.From))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	s.removeFromTrustedLocked(rev.From)
	s.upsertPeerStateLocked(&s.state.Revoked, rev.From, now, rev.Reason)
	s.removePendingByBotLocked(rev.From)
	if err := s.persistLocked(); err != nil {
		s.log.Warn("persist trust revoke failed", logging.Error(err))
	}
	_ = s.peers.SetTrustStatus(rev.From, types.TrustRevoked)
	s.log.Info("trust revoked by peer",
		logging.Event("trust.revoked"),
		logging.Peer(rev.From),
		slog.String("reason", rev.Reason),
	)
}

func (s *Service) respond(requestID, decision, reason string) error {
	if requestID == "" {
		return errors.New("requestId is required")
	}
	if decision != "approve" && decision != "reject" {
		return errors.New("decision must be approve or reject")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pending, ok := s.findPendingByRequestIDLocked(requestID)
	if !ok || pending.Direction != "inbound" {
		return protocol.NewError(protocol.ErrTrustNotFound, "pending trust request not found")
	}

	now := time.Now().UnixMilli()
	if err := s.applyTrustDecisionLocked(pending, decision, reason, now); err != nil {
		return err
	}
	if err := s.persistLocked(); err != nil {
		return err
	}
	s.log.Info("trust decision sent",
		logging.Event("trust.response.sent"),
		logging.Peer(pending.From),
		logging.RequestID(requestID),
		slog.String("decision", decision),
	)
	return nil
}

func (s *Service) applyTrustDecisionLocked(pending store.TrustPendingState, decision, reason string, now int64) error {
	resp := protocol.TrustResponse{
		MessageID:   randID(),
		MessageType: "trust.response",
		From:        s.botID,
		To:          pending.From,
		RequestID:   pending.RequestID,
		Decision:    decision,
		Reason:      reason,
		Ts:          now,
	}
	resp.Signature = s.signTrustResponse(resp)

	if err := s.publishJSON("clawsynapse.trust."+pending.From+".response", resp); err != nil {
		return err
	}

	s.removePendingByRequestIDLocked(pending.RequestID)
	if decision == "approve" {
		s.upsertPeerStateLocked(&s.state.Trusted, pending.From, now, reason)
		s.removeFromRejectedLocked(pending.From)
		s.removeFromRevokedLocked(pending.From)
		_ = s.peers.SetTrustStatus(pending.From, types.TrustTrusted)
		return nil
	}

	s.upsertPeerStateLocked(&s.state.Rejected, pending.From, now, reason)
	s.removeFromTrustedLocked(pending.From)
	_ = s.peers.SetTrustStatus(pending.From, types.TrustRejected)
	return nil
}

func (s *Service) hasPendingLocked(botID, direction string) bool {
	for _, p := range s.state.Pending {
		if p.Direction == direction {
			if direction == "outbound" && p.To == botID {
				return true
			}
			if direction == "inbound" && p.From == botID {
				return true
			}
		}
	}
	return false
}

func (s *Service) hasRequestIDLocked(requestID string) bool {
	for _, p := range s.state.Pending {
		if p.RequestID == requestID {
			return true
		}
	}
	return false
}

func (s *Service) findPendingByRequestIDLocked(requestID string) (store.TrustPendingState, bool) {
	for _, p := range s.state.Pending {
		if p.RequestID == requestID {
			return p, true
		}
	}
	return store.TrustPendingState{}, false
}

func (s *Service) removePendingByRequestIDLocked(requestID string) {
	out := make([]store.TrustPendingState, 0, len(s.state.Pending))
	for _, p := range s.state.Pending {
		if p.RequestID == requestID {
			continue
		}
		out = append(out, p)
	}
	s.state.Pending = out
}

func (s *Service) removePendingByBotLocked(botID string) {
	out := make([]store.TrustPendingState, 0, len(s.state.Pending))
	for _, p := range s.state.Pending {
		if p.From == botID || p.To == botID {
			continue
		}
		out = append(out, p)
	}
	s.state.Pending = out
}

func (s *Service) upsertPeerStateLocked(target *[]store.TrustPeerState, botID string, at int64, reason string) {
	out := make([]store.TrustPeerState, 0, len(*target)+1)
	found := false
	for _, item := range *target {
		if item.BotID == botID {
			out = append(out, store.TrustPeerState{BotID: botID, AtMs: at, Reason: reason})
			found = true
			continue
		}
		out = append(out, item)
	}
	if !found {
		out = append(out, store.TrustPeerState{BotID: botID, AtMs: at, Reason: reason})
	}
	*target = out
}

func (s *Service) removeFromTrustedLocked(botID string) {
	s.state.Trusted = removePeerState(s.state.Trusted, botID)
}

func (s *Service) removeFromRejectedLocked(botID string) {
	s.state.Rejected = removePeerState(s.state.Rejected, botID)
}

func (s *Service) removeFromRevokedLocked(botID string) {
	s.state.Revoked = removePeerState(s.state.Revoked, botID)
}

func removePeerState(src []store.TrustPeerState, botID string) []store.TrustPeerState {
	out := make([]store.TrustPeerState, 0, len(src))
	for _, item := range src {
		if item.BotID == botID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *Service) persistLocked() error {
	return s.store.SaveTrustState(s.state)
}

func (s *Service) syncPeerTrustStates() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.state.Trusted {
		_ = s.peers.SetTrustStatus(t.BotID, types.TrustTrusted)
	}
	for _, p := range s.state.Pending {
		if p.Direction == "inbound" {
			_ = s.peers.SetTrustStatus(p.From, types.TrustPending)
		} else {
			_ = s.peers.SetTrustStatus(p.To, types.TrustPending)
		}
	}
	for _, r := range s.state.Rejected {
		_ = s.peers.SetTrustStatus(r.BotID, types.TrustRejected)
	}
	for _, r := range s.state.Revoked {
		_ = s.peers.SetTrustStatus(r.BotID, types.TrustRevoked)
	}
}

func (s *Service) peerPublicKey(peerBot string) (ed25519.PublicKey, error) {
	peer, ok := s.peers.Get(peerBot)
	if !ok {
		return nil, errors.New("peer not found")
	}
	if peer.Metadata == nil {
		return nil, errors.New("peer metadata is empty")
	}
	v, ok := peer.Metadata["publicKey"].(string)
	if !ok || v == "" {
		return nil, errors.New("peer public key is unavailable")
	}
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid peer public key size")
	}
	return ed25519.PublicKey(b), nil
}

func (s *Service) signTrustRequest(req protocol.TrustRequest) string {
	return identity.Sign(s.identity.PrivateKey, []byte(s.trustRequestSignatureInput(req)))
}

func (s *Service) signTrustResponse(resp protocol.TrustResponse) string {
	return identity.Sign(s.identity.PrivateKey, []byte(s.trustResponseSignatureInput(resp)))
}

func (s *Service) signTrustRevoke(rev protocol.TrustRevoke) string {
	return identity.Sign(s.identity.PrivateKey, []byte(s.trustRevokeSignatureInput(rev)))
}

func (s *Service) trustRequestSignatureInput(req protocol.TrustRequest) string {
	return stringsJoin(req.MessageType, req.From, req.To, req.RequestID, req.Reason, fmt.Sprintf("%d", req.Ts))
}

func (s *Service) trustResponseSignatureInput(resp protocol.TrustResponse) string {
	return stringsJoin(resp.MessageType, resp.From, resp.To, resp.RequestID, resp.Decision, resp.Reason, fmt.Sprintf("%d", resp.Ts))
}

func (s *Service) trustRevokeSignatureInput(rev protocol.TrustRevoke) string {
	return stringsJoin(rev.MessageType, rev.From, rev.To, rev.Reason, fmt.Sprintf("%d", rev.Ts))
}

func stringsJoin(parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
