package messaging

import (
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

type PublishRequest struct {
	TargetNode string
	Message    string
	SessionKey string
	Metadata   map[string]any
}

type Service struct {
	mu        sync.Mutex
	log       *slog.Logger
	peers     *discovery.Registry
	bus       *natsbus.Client
	nodeID    string
	identity  *identity.Identity
	trustMode string
	inbox     []protocol.MessageEnvelope
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, nodeID string, id *identity.Identity, trustMode string) *Service {
	return &Service{log: log, peers: peers, bus: bus, nodeID: nodeID, identity: id, trustMode: trustMode, inbox: []protocol.MessageEnvelope{}}
}

func (s *Service) Start() error {
	inboxSubject := "clawsynapse.msg." + s.nodeID + ".inbox"
	_, err := s.bus.Subscribe(inboxSubject, s.handleInbox)
	return err
}

func (s *Service) Publish(req PublishRequest) (string, error) {
	if req.TargetNode == "" {
		return "", errors.New("targetNode is required")
	}
	peer, ok := s.peers.Get(req.TargetNode)
	if !ok {
		return "", errors.New("target peer not found")
	}
	if s.trustMode != "open" {
		if peer.TrustStatus != types.TrustTrusted {
			return "", protocol.NewError("control.unauthorized", "peer is not trusted")
		}
		if peer.AuthStatus != types.AuthAuthenticated {
			return "", errors.New("peer is not authenticated")
		}
	}

	env := protocol.MessageEnvelope{
		ID:              randID(),
		Type:            "chat.message",
		From:            s.nodeID,
		To:              req.TargetNode,
		Content:         req.Message,
		SessionKey:      req.SessionKey,
		Metadata:        req.Metadata,
		Ts:              time.Now().UnixMilli(),
		ProtocolVersion: "v1",
	}
	env.Sig = identity.Sign(s.identity.PrivateKey, []byte(s.signatureInput(env)))

	subject := "clawsynapse.msg." + req.TargetNode + ".inbox"
	if err := s.bus.PublishJSON(subject, env); err != nil {
		return "", err
	}
	return env.ID, nil
}

func (s *Service) RecentMessages(limit int) []protocol.MessageEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.inbox) {
		limit = len(s.inbox)
	}
	start := len(s.inbox) - limit
	out := make([]protocol.MessageEnvelope, limit)
	copy(out, s.inbox[start:])
	return out
}

func (s *Service) handleInbox(subject string, data []byte) {
	var env protocol.MessageEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		s.log.Warn("decode inbox message failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}

	if env.To != "" && env.To != s.nodeID {
		return
	}

	if s.trustMode == "open" {
		s.acceptInbox(env)
		return
	}

	peer, ok := s.peers.Get(env.From)
	if !ok {
		s.log.Warn("message sender not found", slog.String("from", env.From))
		return
	}
	if peer.TrustStatus != types.TrustTrusted {
		s.log.Warn("reject message from untrusted peer", slog.String("from", env.From), slog.String("trustStatus", peer.TrustStatus))
		return
	}

	pub, err := s.peerPublicKey(env.From)
	if err != nil {
		s.log.Warn("sender public key unavailable", slog.String("from", env.From), slog.String("error", err.Error()))
		return
	}
	if !identity.Verify(pub, []byte(s.signatureInput(env)), env.Sig) {
		s.log.Warn("invalid message signature", slog.String("from", env.From), slog.String("id", env.ID))
		return
	}

	s.acceptInbox(env)
}

func (s *Service) acceptInbox(env protocol.MessageEnvelope) {
	if s.trustMode == "open" {
		if _, ok := s.peers.Get(env.From); !ok && env.From != "" {
			s.peers.Upsert(types.Peer{NodeID: env.From, AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted})
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbox = append(s.inbox, env)
	if len(s.inbox) > 1000 {
		s.inbox = s.inbox[len(s.inbox)-1000:]
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

func (s *Service) signatureInput(env protocol.MessageEnvelope) string {
	return join(env.Type, env.From, env.To, fmt.Sprintf("%d", env.Ts), env.Content, env.ID)
}

func join(parts ...string) string {
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
