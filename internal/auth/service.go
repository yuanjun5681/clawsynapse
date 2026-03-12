package auth

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
	"clawsynapse/pkg/types"
)

type pendingChallenge struct {
	requestID string
	nonce     string
	target    string
	requestTs int64
	createdAt time.Time
	resultCh  chan error
}

type pendingAck struct {
	challengeRef string
	peer         string
	nonce        string
	responseTs   int64
	createdAt    time.Time
}

type Service struct {
	log       *slog.Logger
	peers     *discovery.Registry
	bus       *natsbus.Client
	nodeID    string
	identity  *identity.Identity
	replay    *ReplayGuard
	trustMode string

	mu      sync.Mutex
	pending map[string]*pendingChallenge
	acks    map[string]*pendingAck
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, nodeID string, id *identity.Identity, replay *ReplayGuard, trustMode string) *Service {
	return &Service{
		log:       log,
		peers:     peers,
		bus:       bus,
		nodeID:    nodeID,
		identity:  id,
		replay:    replay,
		trustMode: trustMode,
		pending:   map[string]*pendingChallenge{},
		acks:      map[string]*pendingAck{},
	}
}

func (s *Service) Start() error {
	if _, err := s.bus.Subscribe("clawsynapse.auth."+s.nodeID+".challenge.request", s.handleChallengeRequest); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.auth."+s.nodeID+".challenge.response", s.handleChallengeResponse); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe("clawsynapse.auth."+s.nodeID+".challenge.ack", s.handleChallengeAck); err != nil {
		return err
	}
	return nil
}

func (s *Service) StartChallenge(ctx context.Context, targetNode string) error {
	if targetNode == "" {
		return errors.New("targetNode is required")
	}

	_, ok := s.peers.Get(targetNode)
	if !ok {
		return errors.New("target peer not found")
	}

	if s.trustMode == "open" {
		_ = s.peers.SetAuthStatus(targetNode, types.AuthAuthenticated)
		return nil
	}

	_ = s.peers.SetAuthStatus(targetNode, types.AuthPending)

	challengeID := randID()
	nonce := randID()
	pub := base64.RawURLEncoding.EncodeToString(s.identity.PublicKey)
	req := protocol.AuthChallengeRequest{
		MessageID:   challengeID,
		MessageType: "auth.challenge.request",
		From:        s.nodeID,
		To:          targetNode,
		PublicKey:   pub,
		Nonce:       nonce,
		Ts:          time.Now().UnixMilli(),
		Alg:         "ed25519",
	}
	sigData := []byte(req.Nonce + "|" + req.From + "|" + req.To + "|" + fmt.Sprintf("%d", req.Ts))
	req.Signature = identity.Sign(s.identity.PrivateKey, sigData)

	waitCh := make(chan error, 1)
	s.mu.Lock()
	s.pending[challengeID] = &pendingChallenge{
		requestID: challengeID,
		nonce:     nonce,
		target:    targetNode,
		requestTs: req.Ts,
		createdAt: time.Now(),
		resultCh:  waitCh,
	}
	s.mu.Unlock()

	subject := "clawsynapse.auth." + targetNode + ".challenge.request"
	if err := s.bus.PublishJSON(subject, req); err != nil {
		s.clearPending(challengeID)
		_ = s.peers.SetAuthStatus(targetNode, types.AuthSeen)
		return err
	}

	select {
	case <-ctx.Done():
		s.clearPending(challengeID)
		_ = s.peers.SetAuthStatus(targetNode, types.AuthSeen)
		return ctx.Err()
	case err := <-waitCh:
		if err != nil {
			_ = s.peers.SetAuthStatus(targetNode, types.AuthRejected)
			return err
		}
		_ = s.peers.SetAuthStatus(targetNode, types.AuthAuthenticated)
		return nil
	}
}

func (s *Service) handleChallengeRequest(subject string, data []byte) {
	var req protocol.AuthChallengeRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.log.Warn("decode challenge request failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	if req.To != s.nodeID {
		return
	}

	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: req.MessageType, To: req.To, Ts: req.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid challenge request", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	if s.trustMode != "open" && s.replay != nil {
		if err := s.replay.CheckAndRemember("auth:request:message:"+req.MessageID, req.Ts); err != nil {
			s.log.Warn("replay blocked for challenge request", slog.String("from", req.From), slog.String("error", err.Error()))
			_ = s.peers.SetAuthStatus(req.From, types.AuthRejected)
			return
		}
		if err := s.replay.CheckAndRemember("auth:request:nonce:"+req.From+":"+req.Nonce, req.Ts); err != nil {
			s.log.Warn("replay blocked for challenge nonce", slog.String("from", req.From), slog.String("error", err.Error()))
			_ = s.peers.SetAuthStatus(req.From, types.AuthRejected)
			return
		}
	}

	if s.trustMode != "open" {
		peerPub, err := s.peerPublicKey(req.From, req.PublicKey)
		if err != nil {
			s.log.Warn("peer public key unavailable", slog.String("peer", req.From), slog.String("error", err.Error()))
			return
		}

		sigData := []byte(req.Nonce + "|" + req.From + "|" + req.To + "|" + fmt.Sprintf("%d", req.Ts))
		if !identity.Verify(peerPub, sigData, req.Signature) {
			s.log.Warn("invalid challenge request signature", slog.String("peer", req.From))
			_ = s.peers.SetAuthStatus(req.From, types.AuthRejected)
			return
		}
	}

	nonce := randID()
	proofData := []byte(req.Nonce + "|" + req.From + "|" + s.nodeID + "|" + fmt.Sprintf("%d", req.Ts))
	resp := protocol.AuthChallengeResponse{
		MessageID:    randID(),
		MessageType:  "auth.challenge.response",
		From:         s.nodeID,
		To:           req.From,
		PublicKey:    base64.RawURLEncoding.EncodeToString(s.identity.PublicKey),
		Nonce:        nonce,
		ChallengeRef: req.MessageID,
		Proof:        identity.Sign(s.identity.PrivateKey, proofData),
		Ts:           time.Now().UnixMilli(),
	}

	sub := "clawsynapse.auth." + req.From + ".challenge.response"
	if err := s.bus.PublishJSON(sub, resp); err != nil {
		s.log.Warn("publish challenge response failed", slog.String("peer", req.From), slog.String("error", err.Error()))
	}
}

func (s *Service) handleChallengeResponse(subject string, data []byte) {
	var resp protocol.AuthChallengeResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		s.log.Warn("decode challenge response failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	if resp.To != s.nodeID {
		return
	}
	if s.trustMode == "open" {
		s.ensurePeerState(resp.From, types.AuthAuthenticated)
		return
	}

	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: resp.MessageType, To: resp.To, Ts: resp.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid challenge response", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	if s.replay != nil {
		if err := s.replay.CheckAndRemember("auth:response:message:"+resp.MessageID, resp.Ts); err != nil {
			s.log.Warn("replay blocked for challenge response", slog.String("from", resp.From), slog.String("error", err.Error()))
			return
		}
		if err := s.replay.CheckAndRemember("auth:response:nonce:"+resp.From+":"+resp.Nonce, resp.Ts); err != nil {
			s.log.Warn("replay blocked for challenge response nonce", slog.String("from", resp.From), slog.String("error", err.Error()))
			return
		}
	}

	s.mu.Lock()
	p, ok := s.pending[resp.ChallengeRef]
	s.mu.Unlock()
	if !ok {
		return
	}
	if resp.From != p.target {
		s.failChallenge(resp.ChallengeRef, p, errors.New("challenge response sender mismatch"))
		return
	}

	peerPub, err := s.peerPublicKey(resp.From, resp.PublicKey)
	if err != nil {
		s.failChallenge(resp.ChallengeRef, p, err)
		return
	}

	proofData := []byte(p.nonce + "|" + s.nodeID + "|" + resp.From + "|" + fmt.Sprintf("%d", p.requestTs))
	if !identity.Verify(peerPub, proofData, resp.Proof) {
		s.failChallenge(resp.ChallengeRef, p, errors.New("challenge response signature verification failed"))
		return
	}

	ackProof := identity.Sign(s.identity.PrivateKey, []byte(resp.Nonce+"|"+resp.From+"|"+s.nodeID+"|"+fmt.Sprintf("%d", resp.Ts)))
	ack := protocol.AuthChallengeAck{
		MessageID:    randID(),
		MessageType:  "auth.challenge.ack",
		From:         s.nodeID,
		To:           resp.From,
		ChallengeRef: resp.MessageID,
		Proof:        ackProof,
		Ts:           time.Now().UnixMilli(),
	}

	sub := "clawsynapse.auth." + resp.From + ".challenge.ack"
	if err := s.bus.PublishJSON(sub, ack); err != nil {
		s.failChallenge(resp.ChallengeRef, p, err)
		return
	}

	s.savePendingAck(ack.ChallengeRef, pendingAck{
		challengeRef: ack.ChallengeRef,
		peer:         resp.From,
		nonce:        resp.Nonce,
		responseTs:   resp.Ts,
		createdAt:    time.Now(),
	})

	p.resultCh <- nil
	s.clearPending(resp.ChallengeRef)
}

func (s *Service) handleChallengeAck(_ string, data []byte) {
	var ack protocol.AuthChallengeAck
	if err := json.Unmarshal(data, &ack); err != nil {
		return
	}

	subject := "clawsynapse.auth." + s.nodeID + ".challenge.ack"
	if err := protocol.ValidateMessage(subject, protocol.ControlMessage{MessageType: ack.MessageType, To: ack.To, Ts: ack.Ts}, protocol.ValidateOptions{}); err != nil {
		s.log.Warn("invalid challenge ack", slog.String("error", err.Error()))
		return
	}

	if s.replay != nil {
		if err := s.replay.CheckAndRemember("auth:ack:message:"+ack.MessageID, ack.Ts); err != nil {
			s.log.Warn("replay blocked for challenge ack", slog.String("from", ack.From), slog.String("error", err.Error()))
			return
		}
	}

	if ack.To != s.nodeID {
		return
	}
	if s.trustMode == "open" {
		s.ensurePeerState(ack.From, types.AuthAuthenticated)
		return
	}

	ap, ok := s.getPendingAck(ack.ChallengeRef)
	if !ok {
		return
	}
	defer s.clearPendingAck(ack.ChallengeRef)

	if ack.From != ap.peer {
		s.log.Warn("challenge ack sender mismatch", slog.String("from", ack.From), slog.String("expected", ap.peer))
		return
	}

	peerPub, err := s.peerPublicKey(ack.From, "")
	if err != nil {
		s.log.Warn("challenge ack peer key unavailable", slog.String("peer", ack.From), slog.String("error", err.Error()))
		return
	}

	proofData := []byte(ap.nonce + "|" + s.nodeID + "|" + ack.From + "|" + fmt.Sprintf("%d", ap.responseTs))
	if !identity.Verify(peerPub, proofData, ack.Proof) {
		s.log.Warn("invalid challenge ack proof", slog.String("peer", ack.From), slog.String("challengeRef", ack.ChallengeRef))
		return
	}

	_ = s.peers.SetAuthStatus(ack.From, types.AuthAuthenticated)
}

func (s *Service) failChallenge(challengeRef string, p *pendingChallenge, err error) {
	p.resultCh <- err
	s.clearPending(challengeRef)
}

func (s *Service) clearPending(challengeRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, challengeRef)
}

func (s *Service) savePendingAck(challengeRef string, p pendingAck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := p
	s.acks[challengeRef] = &cp
}

func (s *Service) getPendingAck(challengeRef string) (pendingAck, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.acks[challengeRef]
	if !ok {
		return pendingAck{}, false
	}
	return *p, true
}

func (s *Service) clearPendingAck(challengeRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.acks, challengeRef)
}

func (s *Service) peerPublicKey(peerNode, fallback string) (ed25519.PublicKey, error) {
	peer, ok := s.peers.Get(peerNode)
	if !ok {
		return nil, errors.New("peer not found")
	}

	val := ""
	if peer.Metadata != nil {
		if v, ok := peer.Metadata["publicKey"].(string); ok && v != "" {
			val = v
		}
	}

	if val == "" {
		switch s.trustMode {
		case "explicit":
			return nil, errors.New("peer public key is unavailable in explicit mode")
		case "tofu", "open":
			val = fallback
		}
	}

	if val == "" {
		return nil, errors.New("peer public key is unavailable")
	}

	b, err := base64.RawURLEncoding.DecodeString(val)
	if err != nil {
		return nil, err
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, errors.New("invalid peer public key size")
	}

	if (s.trustMode == "tofu" || s.trustMode == "open") && fallback != "" {
		known := ""
		if peer.Metadata != nil {
			if current, ok := peer.Metadata["publicKey"].(string); ok {
				known = current
			}
		}
		if known == "" {
			s.storePeerPublicKey(peer, fallback)
		}
	}

	return ed25519.PublicKey(b), nil
}

func (s *Service) storePeerPublicKey(peer types.Peer, pub string) {
	if peer.Metadata == nil {
		peer.Metadata = map[string]any{}
	}
	peer.Metadata["publicKey"] = pub
	s.peers.Upsert(peer)
}

func (s *Service) ensurePeerState(nodeID, authStatus string) {
	if _, ok := s.peers.Get(nodeID); !ok {
		s.peers.Upsert(types.Peer{NodeID: nodeID, AuthStatus: authStatus, TrustStatus: types.TrustNone})
		return
	}
	_ = s.peers.SetAuthStatus(nodeID, authStatus)
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
