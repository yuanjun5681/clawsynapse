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

type RequestRequest struct {
	TargetNode string
	Message    string
	SessionKey string
	Metadata   map[string]any
	Timeout    time.Duration
}

type RequestResult struct {
	RequestID string
	MessageID string
	From      string
	Reply     string
	RunID     string
}

type pendingRequest struct {
	requestID string
	resultCh  chan RequestResult
	createdAt time.Time
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
	pending   map[string]*pendingRequest
	handler   RequestHandler
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, nodeID string, id *identity.Identity, trustMode string) *Service {
	return &Service{log: log, peers: peers, bus: bus, nodeID: nodeID, identity: id, trustMode: trustMode, inbox: []protocol.MessageEnvelope{}, pending: map[string]*pendingRequest{}}
}

func (s *Service) SetRequestHandler(handler RequestHandler) {
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
	replySubject := s.replySubject()
	_, err := s.bus.Subscribe(replySubject, s.handleReply)
	if err == nil {
		s.log.Debug("subscribed to reply inbox", logging.Event("message.subscribe"), logging.Subject(replySubject))
	}
	return err
}

func (s *Service) Publish(req PublishRequest) (string, error) {
	if req.TargetNode == "" {
		return "", errors.New("targetNode is required")
	}
	if s.bus == nil {
		return "", errors.New("nats client is required")
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
	s.log.Info("message published",
		logging.Event("message.sent"),
		logging.To(req.TargetNode),
		logging.MessageID(env.ID),
		logging.MessageType(env.Type),
		logging.ContentLength(req.Message),
		logging.ContentPreview(req.Message, contentPreviewLimit),
	)
	return env.ID, nil
}

func (s *Service) Request(req RequestRequest) (RequestResult, error) {
	if req.TargetNode == "" {
		return RequestResult{}, errors.New("targetNode is required")
	}
	if req.Message == "" {
		return RequestResult{}, errors.New("message is required")
	}
	if s.bus == nil {
		return RequestResult{}, errors.New("nats client is required")
	}

	peer, ok := s.peers.Get(req.TargetNode)
	if !ok {
		return RequestResult{}, errors.New("target peer not found")
	}
	if s.trustMode != "open" {
		if peer.TrustStatus != types.TrustTrusted {
			return RequestResult{}, protocol.NewError("control.unauthorized", "peer is not trusted")
		}
		if peer.AuthStatus != types.AuthAuthenticated {
			return RequestResult{}, errors.New("peer is not authenticated")
		}
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	requestID := randID()
	messageID := randID()
	pending := &pendingRequest{
		requestID: requestID,
		resultCh:  make(chan RequestResult, 1),
		createdAt: time.Now(),
	}

	s.mu.Lock()
	s.pending[requestID] = pending
	s.mu.Unlock()

	env := protocol.MessageEnvelope{
		ID:              messageID,
		Type:            "chat.request",
		From:            s.nodeID,
		To:              req.TargetNode,
		Content:         req.Message,
		SessionKey:      req.SessionKey,
		ReplyTo:         s.replySubject(),
		RequestID:       requestID,
		Metadata:        req.Metadata,
		Ts:              time.Now().UnixMilli(),
		ProtocolVersion: "v1",
	}
	env.Sig = identity.Sign(s.identity.PrivateKey, []byte(s.signatureInput(env)))

	if err := s.bus.PublishJSON("clawsynapse.msg."+req.TargetNode+".inbox", env); err != nil {
		s.clearPendingRequest(requestID)
		return RequestResult{}, err
	}
	s.log.Info("request message sent",
		logging.Event("message.request.sent"),
		logging.To(req.TargetNode),
		logging.MessageID(messageID),
		logging.RequestID(requestID),
		logging.MessageType(env.Type),
		logging.ContentLength(req.Message),
		logging.ContentPreview(req.Message, contentPreviewLimit),
	)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-pending.resultCh:
		return result, nil
	case <-timer.C:
		s.clearPendingRequest(requestID)
		return RequestResult{}, protocol.NewError("msg.request_timeout", "reply timed out")
	}
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
			logging.RequestID(env.RequestID),
			logging.ContentLength(env.Content),
			logging.ContentPreview(env.Content, contentPreviewLimit),
		)
		s.acceptInbox(env)
		s.maybeReply(env)
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
		logging.RequestID(env.RequestID),
		logging.ContentLength(env.Content),
		logging.ContentPreview(env.Content, contentPreviewLimit),
	)
	s.acceptInbox(env)
	s.maybeReply(env)
	s.maybeDeliver(env)
}

func (s *Service) handleReply(subject string, data []byte) {
	var env protocol.MessageEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		s.log.Warn("decode reply message failed", logging.Subject(subject), logging.Error(err))
		return
	}
	if env.To != "" && env.To != s.nodeID {
		return
	}

	if s.trustMode != "open" {
		peer, ok := s.peers.Get(env.From)
		if !ok {
			s.log.Warn("reply sender not found", logging.From(env.From))
			return
		}
		if peer.TrustStatus != types.TrustTrusted {
			s.log.Warn("reject reply from untrusted peer", logging.From(env.From), logging.TrustStatus(peer.TrustStatus))
			return
		}
		pub, err := s.peerPublicKey(env.From)
		if err != nil {
			s.log.Warn("reply sender public key unavailable", logging.From(env.From), logging.Error(err))
			return
		}
		if !identity.Verify(pub, []byte(s.signatureInput(env)), env.Sig) {
			s.log.Warn("invalid reply signature", logging.From(env.From), logging.MessageID(env.ID))
			return
		}
	}

	s.log.Info("reply received",
		logging.Event("message.reply.received"),
		logging.From(env.From),
		logging.MessageID(env.ID),
		logging.RequestID(env.RequestID),
		logging.CorrelationID(env.CorrelationID),
		logging.ContentLength(env.Content),
		logging.ContentPreview(env.Content, contentPreviewLimit),
	)
	s.dispatchReply(env)
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

func (s *Service) maybeReply(env protocol.MessageEnvelope) {
	if env.RequestID == "" || env.ReplyTo == "" {
		return
	}
	if s.bus == nil {
		return
	}

	reply, err := s.buildReply(env)
	if err != nil {
		s.log.Warn("handle request failed", logging.From(env.From), logging.RequestID(env.RequestID), logging.Error(err))
		return
	}
	if err := s.bus.PublishJSON(env.ReplyTo, reply); err != nil {
		s.log.Warn("publish reply failed", logging.To(env.From), logging.RequestID(env.RequestID), logging.Error(err))
	}
	s.log.Info("reply sent",
		logging.Event("message.reply.sent"),
		logging.To(env.From),
		logging.MessageID(reply.ID),
		logging.RequestID(env.RequestID),
		logging.CorrelationID(env.ID),
		logging.ContentLength(reply.Content),
		logging.ContentPreview(reply.Content, contentPreviewLimit),
	)
}

func (s *Service) maybeDeliver(env protocol.MessageEnvelope) {
	if env.Type != "chat.message" {
		return
	}
	handler := s.requestHandler()
	if handler == nil {
		return
	}

	go func() {
		_, err := handler.HandleRequest(IncomingRequest{
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
				logging.Error(err),
			)
			return
		}
		s.log.Info("message delivered to agent",
			logging.Event("message.deliver.ok"),
			logging.From(env.From),
			logging.MessageID(env.ID),
		)
	}()
}

func (s *Service) buildReply(env protocol.MessageEnvelope) (protocol.MessageEnvelope, error) {
	replyContent := "ack: " + env.Content
	if handler := s.requestHandler(); handler != nil {
		result, err := handler.HandleRequest(IncomingRequest{
			RequestID:  env.RequestID,
			MessageID:  env.ID,
			From:       env.From,
			To:         env.To,
			Message:    env.Content,
			SessionKey: env.SessionKey,
			Metadata:   cloneMetadata(env.Metadata),
		})
		if err != nil {
			return protocol.MessageEnvelope{}, err
		}
		replyContent = result.Reply
		if result.RunID != "" {
			if env.Metadata == nil {
				env.Metadata = map[string]any{}
			}
			env.Metadata["runId"] = result.RunID
		}
	}

	replyMetadata := map[string]any(nil)
	if runID, ok := env.Metadata["runId"].(string); ok && runID != "" {
		replyMetadata = map[string]any{"runId": runID}
	}

	reply := protocol.MessageEnvelope{
		ID:              randID(),
		Type:            "chat.reply",
		From:            s.nodeID,
		To:              env.From,
		Content:         replyContent,
		SessionKey:      env.SessionKey,
		RequestID:       env.RequestID,
		CorrelationID:   env.ID,
		Metadata:        replyMetadata,
		Ts:              time.Now().UnixMilli(),
		ProtocolVersion: "v1",
	}
	reply.Sig = identity.Sign(s.identity.PrivateKey, []byte(s.signatureInput(reply)))
	return reply, nil
}

func (s *Service) dispatchReply(env protocol.MessageEnvelope) {
	if env.RequestID == "" {
		return
	}

	s.mu.Lock()
	pending, ok := s.pending[env.RequestID]
	if ok {
		delete(s.pending, env.RequestID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}

	select {
	case pending.resultCh <- RequestResult{RequestID: env.RequestID, MessageID: env.CorrelationID, From: env.From, Reply: env.Content, RunID: runIDFromMetadata(env.Metadata)}:
		s.log.Info("reply dispatched to pending request",
			logging.Event("message.reply.dispatched"),
			logging.From(env.From),
			logging.RequestID(env.RequestID),
			logging.CorrelationID(env.CorrelationID),
		)
	default:
	}
}

func (s *Service) clearPendingRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, requestID)
}

func (s *Service) requestHandler() RequestHandler {
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

func runIDFromMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	runID, _ := metadata["runId"].(string)
	return runID
}

func (s *Service) replySubject() string {
	return "clawsynapse.msg." + s.nodeID + ".reply"
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
