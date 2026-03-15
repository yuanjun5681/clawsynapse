package messaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/logging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

const contentPreviewLimit = 160

type PublishRequest struct {
	TargetNode string
	Message    string
	SessionKey string
	Metadata   map[string]any
}

type PublishResult struct {
	MessageID  string
	SessionKey string
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
	handler   MessageHandler
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, nodeID string, id *identity.Identity, trustMode string) *Service {
	return &Service{log: log, peers: peers, bus: bus, nodeID: nodeID, identity: id, trustMode: trustMode, inbox: []protocol.MessageEnvelope{}}
}

func (s *Service) SetMessageHandler(handler MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

func (s *Service) Start() error {
	inboxSubject := "clawsynapse.msg." + s.nodeID + ".inbox"
	if s.bus == nil {
		return errors.New("nats client is required")
	}
	if _, err := s.bus.Subscribe(inboxSubject, s.handleInbox); err != nil {
		return err
	}
	s.log.Debug("subscribed to inbox", logging.Event("message.subscribe"), logging.Subject(inboxSubject))
	return nil
}

func (s *Service) Publish(req PublishRequest) (PublishResult, error) {
	if req.TargetNode == "" {
		return PublishResult{}, errors.New("targetNode is required")
	}
	if s.bus == nil {
		return PublishResult{}, errors.New("nats client is required")
	}
	peer, ok := s.peers.Get(req.TargetNode)
	if !ok {
		return PublishResult{}, errors.New("target peer not found")
	}
	if s.trustMode != "open" {
		if peer.TrustStatus != types.TrustTrusted {
			return PublishResult{}, protocol.NewError("control.unauthorized", "peer is not trusted")
		}
		if peer.AuthStatus != types.AuthAuthenticated {
			return PublishResult{}, errors.New("peer is not authenticated")
		}
	}

	sessionKey := req.SessionKey
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = newSessionKey()
	}

	env := protocol.MessageEnvelope{
		ID:              randID(),
		Type:            "chat.message",
		From:            s.nodeID,
		To:              req.TargetNode,
		Content:         req.Message,
		SessionKey:      sessionKey,
		Metadata:        req.Metadata,
		Ts:              time.Now().UnixMilli(),
		ProtocolVersion: "v1",
	}
	env.Sig = identity.Sign(s.identity.PrivateKey, []byte(s.signatureInput(env)))

	subject := "clawsynapse.msg." + req.TargetNode + ".inbox"
	if err := s.bus.PublishJSON(subject, env); err != nil {
		return PublishResult{}, err
	}
	s.log.Info("message published",
		logging.Event("message.sent"),
		logging.To(req.TargetNode),
		logging.MessageID(env.ID),
		logging.MessageType(env.Type),
		logging.SessionKey(sessionKey),
		logging.ContentLength(req.Message),
		logging.ContentPreview(req.Message, contentPreviewLimit),
	)
	return PublishResult{MessageID: env.ID, SessionKey: sessionKey}, nil
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
		s.log.Warn("decode inbox message failed", logging.Subject(subject), logging.Error(err))
		return
	}

	if env.To != "" && env.To != s.nodeID {
		return
	}

	if s.trustMode == "open" {
		s.log.Info("message received",
			logging.Event("message.received"),
			logging.From(env.From),
			logging.MessageID(env.ID),
			logging.MessageType(env.Type),
			logging.SessionKey(env.SessionKey),
			logging.ContentLength(env.Content),
			logging.ContentPreview(env.Content, contentPreviewLimit),
		)
		s.acceptInbox(env)
		s.maybeDeliver(env)
		return
	}

	peer, ok := s.peers.Get(env.From)
	if !ok {
		s.log.Warn("message sender not found", logging.From(env.From))
		return
	}
	if peer.TrustStatus != types.TrustTrusted {
		s.log.Warn("reject message from untrusted peer", logging.From(env.From), logging.TrustStatus(peer.TrustStatus))
		return
	}

	pub, err := s.peerPublicKey(env.From)
	if err != nil {
		s.log.Warn("sender public key unavailable", logging.From(env.From), logging.Error(err))
		return
	}
	if !identity.Verify(pub, []byte(s.signatureInput(env)), env.Sig) {
		s.log.Warn("invalid message signature", logging.From(env.From), logging.MessageID(env.ID))
		return
	}

	s.log.Info("message received",
		logging.Event("message.received"),
		logging.From(env.From),
		logging.MessageID(env.ID),
		logging.MessageType(env.Type),
		logging.SessionKey(env.SessionKey),
		logging.ContentLength(env.Content),
		logging.ContentPreview(env.Content, contentPreviewLimit),
	)
	s.acceptInbox(env)
	s.maybeDeliver(env)
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

func (s *Service) maybeDeliver(env protocol.MessageEnvelope) {
	if env.Type != "chat.message" {
		return
	}
	content := strings.TrimSpace(env.Content)
	if strings.HasPrefix(content, "[reply]") || strings.HasPrefix(content, "[end]") {
		s.log.Debug("skip deliver: reply/end message",
			logging.Event("message.deliver.skipped"),
			logging.From(env.From),
			logging.MessageID(env.ID),
			logging.SessionKey(env.SessionKey),
		)
		return
	}
	handler := s.messageHandler()
	if handler == nil {
		return
	}

	go func() {
		_, err := handler.HandleMessage(IncomingMessage{
			MessageID:  env.ID,
			From:       env.From,
			To:         env.To,
			Message:    env.Content,
			SessionKey: env.SessionKey,
			Metadata:   cloneMetadata(env.Metadata),
		})
		if err != nil {
			s.log.Warn("deliver message to agent failed",
				logging.Event("message.deliver.failed"),
				logging.From(env.From),
				logging.MessageID(env.ID),
				logging.SessionKey(env.SessionKey),
				logging.Error(err),
			)
			return
		}
		s.log.Info("message delivered to agent",
			logging.Event("message.deliver.ok"),
			logging.From(env.From),
			logging.MessageID(env.ID),
			logging.SessionKey(env.SessionKey),
		)
	}()
}

func (s *Service) messageHandler() MessageHandler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handler
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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

func newSessionKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		ts := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(ts >> ((i % 8) * 8))
		}
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
