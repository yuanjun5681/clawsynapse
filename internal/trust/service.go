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
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

type Service struct {
	mu       sync.Mutex
	log      *slog.Logger
	peers    *discovery.Registry
	bus      *natsbus.Client
	store    *store.FSStore
	nodeID   string
	identity *identity.Identity
	state    store.TrustState
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, fs *store.FSStore, nodeID string, id *identity.Identity) (*Service, error) {
	st, err := fs.LoadTrustState()
	if err != nil {
		return nil, err
	}

	s := &Service{
		log:      log,
		peers:    peers,
		bus:      bus,
		store:    fs,
		nodeID:   nodeID,
		identity: id,
		state:    st,
	}

	s.syncPeerTrustStates()
	return s, nil
}

func (s *Service) Start() error {
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.nodeID+".request", s.handleTrustRequest); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.nodeID+".response", s.handleTrustResponse); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.trust."+s.nodeID+".revoke", s.handleTrustRevoke); err != nil {
		return err
	}
	return nil
}

func (s *Service) Request(_ context.Context, targetNode, reason string, capabilities []string) (string, error) {
	if targetNode == "" {
		return "", errors.New("targetNode is required")
	}
	peer, ok := s.peers.Get(targetNode)
	if !ok {
		return "", errors.New("target peer not found")
	}
	if peer.AuthStatus != types.AuthAuthenticated {
		return "", errors.New("target peer must be authenticated before trust request")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasPendingLocked(targetNode, "outbound") {
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
		From:         s.nodeID,
		To:           targetNode,
		RequestID:    requestID,
		Reason:       reason,
		Capabilities: capabilities,
		Ts:           now,
	}
	req.Signature = s.signTrustRequest(req)

	if err := s.bus.PublishJSON("clawsynapse.trust."+targetNode+".request", req); err != nil {
		return "", err
	}

	s.state.Pending = append(s.state.Pending, store.TrustPendingState{
		RequestID:    requestID,
		From:         s.nodeID,
		To:           targetNode,
		Direction:    "outbound",
		Reason:       reason,
		ReceivedAtMs: now,
	})
	if err := s.persistLocked(); err != nil {
		return "", err
	}
	_ = s.peers.SetTrustStatus(targetNode, types.TrustPending)
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

func (s *Service) Revoke(targetNode, reason string) error {
	if targetNode == "" {
		return errors.New("targetNode is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	rev := protocol.TrustRevoke{
		MessageID:   randID(),
		MessageType: "trust.revoke",
		From:        s.nodeID,
		To:          targetNode,
		Reason:      reason,
		Ts:          now,
	}
	rev.Signature = s.signTrustRevoke(rev)

	if err := s.bus.PublishJSON("clawsynapse.trust."+targetNode+".revoke", rev); err != nil {
		return err
	}

	s.removeFromTrustedLocked(targetNode)
	s.upsertPeerStateLocked(&s.state.Revoked, targetNode, now, reason)
	s.removePendingByNodeLocked(targetNode)
	if err := s.persistLocked(); err != nil {
		return err
	}
	_ = s.peers.SetTrustStatus(targetNode, types.TrustRevoked)
	return nil
}

func (s *Service) handleTrustRequest(subject string, data []byte) {
	var req protocol.TrustRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.log.Warn("decode trust request failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	if req.To != s.nodeID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: req.MessageType, To: req.To, Ts: req.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust request", slog.String("error", err.Error()))
		return
	}

	peerPub, err := s.peerPublicKey(req.From)
	if err != nil {
		s.log.Warn("trust request peer key unavailable", slog.String("peer", req.From), slog.String("error", err.Error()))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustRequestSignatureInput(req)), req.Signature) {
		s.log.Warn("invalid trust request signature", slog.String("peer", req.From))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.hasRequestIDLocked(req.RequestID) {
		return
	}

	now := time.Now().UnixMilli()
	s.state.Pending = append(s.state.Pending, store.TrustPendingState{
		RequestID:    req.RequestID,
		From:         req.From,
		To:           req.To,
		Direction:    "inbound",
		Reason:       req.Reason,
		ReceivedAtMs: now,
	})
	if err := s.persistLocked(); err != nil {
		s.log.Warn("persist trust pending failed", slog.String("error", err.Error()))
		return
	}
	_ = s.peers.SetTrustStatus(req.From, types.TrustPending)
}

func (s *Service) handleTrustResponse(subject string, data []byte) {
	var resp protocol.TrustResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		s.log.Warn("decode trust response failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	if resp.To != s.nodeID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: resp.MessageType, To: resp.To, Ts: resp.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust response", slog.String("error", err.Error()))
		return
	}

	peerPub, err := s.peerPublicKey(resp.From)
	if err != nil {
		s.log.Warn("trust response peer key unavailable", slog.String("peer", resp.From), slog.String("error", err.Error()))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustResponseSignatureInput(resp)), resp.Signature) {
		s.log.Warn("invalid trust response signature", slog.String("peer", resp.From))
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
		s.log.Warn("persist trust response failed", slog.String("error", err.Error()))
	}
}

func (s *Service) handleTrustRevoke(subject string, data []byte) {
	var rev protocol.TrustRevoke
	if err := json.Unmarshal(data, &rev); err != nil {
		s.log.Warn("decode trust revoke failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	if rev.To != s.nodeID {
		return
	}
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: rev.MessageType, To: rev.To, Ts: rev.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid trust revoke", slog.String("error", err.Error()))
		return
	}

	peerPub, err := s.peerPublicKey(rev.From)
	if err != nil {
		s.log.Warn("trust revoke peer key unavailable", slog.String("peer", rev.From), slog.String("error", err.Error()))
		return
	}
	if !identity.Verify(peerPub, []byte(s.trustRevokeSignatureInput(rev)), rev.Signature) {
		s.log.Warn("invalid trust revoke signature", slog.String("peer", rev.From))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	s.removeFromTrustedLocked(rev.From)
	s.upsertPeerStateLocked(&s.state.Revoked, rev.From, now, rev.Reason)
	s.removePendingByNodeLocked(rev.From)
	if err := s.persistLocked(); err != nil {
		s.log.Warn("persist trust revoke failed", slog.String("error", err.Error()))
	}
	_ = s.peers.SetTrustStatus(rev.From, types.TrustRevoked)
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
	resp := protocol.TrustResponse{
		MessageID:   randID(),
		MessageType: "trust.response",
		From:        s.nodeID,
		To:          pending.From,
		RequestID:   requestID,
		Decision:    decision,
		Reason:      reason,
		Ts:          now,
	}
	resp.Signature = s.signTrustResponse(resp)

	if err := s.bus.PublishJSON("clawsynapse.trust."+pending.From+".response", resp); err != nil {
		return err
	}

	s.removePendingByRequestIDLocked(requestID)
	if decision == "approve" {
		s.upsertPeerStateLocked(&s.state.Trusted, pending.From, now, reason)
		s.removeFromRejectedLocked(pending.From)
		s.removeFromRevokedLocked(pending.From)
		_ = s.peers.SetTrustStatus(pending.From, types.TrustTrusted)
	} else {
		s.upsertPeerStateLocked(&s.state.Rejected, pending.From, now, reason)
		s.removeFromTrustedLocked(pending.From)
		_ = s.peers.SetTrustStatus(pending.From, types.TrustRejected)
	}
	if err := s.persistLocked(); err != nil {
		return err
	}
	return nil
}

func (s *Service) hasPendingLocked(nodeID, direction string) bool {
	for _, p := range s.state.Pending {
		if p.Direction == direction {
			if direction == "outbound" && p.To == nodeID {
				return true
			}
			if direction == "inbound" && p.From == nodeID {
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

func (s *Service) removePendingByNodeLocked(nodeID string) {
	out := make([]store.TrustPendingState, 0, len(s.state.Pending))
	for _, p := range s.state.Pending {
		if p.From == nodeID || p.To == nodeID {
			continue
		}
		out = append(out, p)
	}
	s.state.Pending = out
}

func (s *Service) upsertPeerStateLocked(target *[]store.TrustPeerState, nodeID string, at int64, reason string) {
	out := make([]store.TrustPeerState, 0, len(*target)+1)
	found := false
	for _, item := range *target {
		if item.NodeID == nodeID {
			out = append(out, store.TrustPeerState{NodeID: nodeID, AtMs: at, Reason: reason})
			found = true
			continue
		}
		out = append(out, item)
	}
	if !found {
		out = append(out, store.TrustPeerState{NodeID: nodeID, AtMs: at, Reason: reason})
	}
	*target = out
}

func (s *Service) removeFromTrustedLocked(nodeID string) {
	s.state.Trusted = removePeerState(s.state.Trusted, nodeID)
}

func (s *Service) removeFromRejectedLocked(nodeID string) {
	s.state.Rejected = removePeerState(s.state.Rejected, nodeID)
}

func (s *Service) removeFromRevokedLocked(nodeID string) {
	s.state.Revoked = removePeerState(s.state.Revoked, nodeID)
}

func removePeerState(src []store.TrustPeerState, nodeID string) []store.TrustPeerState {
	out := make([]store.TrustPeerState, 0, len(src))
	for _, item := range src {
		if item.NodeID == nodeID {
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
		_ = s.peers.SetTrustStatus(t.NodeID, types.TrustTrusted)
	}
	for _, p := range s.state.Pending {
		if p.Direction == "inbound" {
			_ = s.peers.SetTrustStatus(p.From, types.TrustPending)
		} else {
			_ = s.peers.SetTrustStatus(p.To, types.TrustPending)
		}
	}
	for _, r := range s.state.Rejected {
		_ = s.peers.SetTrustStatus(r.NodeID, types.TrustRejected)
	}
	for _, r := range s.state.Revoked {
		_ = s.peers.SetTrustStatus(r.NodeID, types.TrustRevoked)
	}
}

func (s *Service) peerPublicKey(peerNode string) (ed25519.PublicKey, error) {
	peer, ok := s.peers.Get(peerNode)
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
