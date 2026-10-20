package messaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"

	"github.com/nats-io/nats.go"
)

const (
	messageTTL  = 10 * time.Minute
	workerCount = 8
	queueDepth  = 16
	maxRecent   = 1000
	maxReplay   = 10000
)

type PublishRequest struct {
	TargetNode string
	Type       string
	AgentID    string
	Message    string
	Payload    protocol.MessagePayload
	SessionKey string
	Metadata   map[string]any
	ExpiresAt  int64
	RequestID  string
	RunID      string
	Seq        uint64
}

type PublishResult struct {
	MessageID  string
	SessionKey string
}

type TransferHandler func(env protocol.MessageEnvelope)

type Service struct {
	mu                  sync.Mutex
	log                 *slog.Logger
	peers               *discovery.Registry
	bus                 *natsbus.Client
	nodeID              string
	identity            *identity.Identity
	trustMode           string
	deliverablePrefixes []string
	inbox               []protocol.MessageEnvelope
	handler             MessageHandler
	transferHandler     TransferHandler
	ctx                 context.Context
	cancel              context.CancelFunc
	workers             sync.WaitGroup
	queues              [workerCount]chan protocol.MessageEnvelope
	sub                 *nats.Subscription
	started             bool
	seen                map[string]int64
	lastReplayGC        int64
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, nodeID string, id *identity.Identity, trustMode string, deliverablePrefixes []string) *Service {
	if len(deliverablePrefixes) == 0 {
		deliverablePrefixes = []string{"chat", "task"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{log: log, peers: peers, bus: bus, nodeID: nodeID, identity: id, trustMode: trustMode, deliverablePrefixes: append([]string(nil), deliverablePrefixes...), inbox: []protocol.MessageEnvelope{}, ctx: ctx, cancel: cancel, seen: make(map[string]int64)}
	for i := range s.queues {
		s.queues[i] = make(chan protocol.MessageEnvelope, queueDepth)
	}
	return s
}

func (s *Service) SetMessageHandler(handler MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}
func (s *Service) SetTransferHandler(h TransferHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transferHandler = h
}

func (s *Service) Start() error {
	if s.bus == nil {
		return errors.New("nats client is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("messaging already started")
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	sub, err := s.bus.Subscribe(inboxSubject(s.nodeID), s.handleInbox)
	if err != nil {
		return err
	}
	s.sub = sub
	s.started = true
	for _, queue := range s.queues {
		s.workers.Add(1)
		go s.work(queue)
	}
	return nil
}

// Close cancels queued and active executions before waiting for adapter cleanup.
func (s *Service) Close() {
	s.cancel()
	s.mu.Lock()
	sub := s.sub
	s.mu.Unlock()
	if sub != nil {
		_ = sub.Unsubscribe()
	}
	s.workers.Wait()
}

func inboxSubject(nodeID string) string { return "clawsynapse.msg." + nodeID + ".inbox" }

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *Service) Publish(req PublishRequest) (PublishResult, error) {
	if !validToken(req.TargetNode) {
		return PublishResult{}, errors.New("invalid targetNode")
	}
	if s.bus == nil {
		return PublishResult{}, errors.New("nats client is required")
	}
	if s.ctx.Err() != nil {
		return PublishResult{}, s.ctx.Err()
	}
	peer, ok := s.peers.Get(req.TargetNode)
	if !ok {
		return PublishResult{}, errors.New("target peer not found")
	}
	if s.trustMode != "open" && (peer.TrustStatus != types.TrustTrusted || peer.AuthStatus != types.AuthAuthenticated) {
		return PublishResult{}, protocol.NewError("control.unauthorized", "peer is not authenticated and trusted")
	}
	if req.Message != "" {
		if req.Payload.Text != "" || req.Payload.Instruction != "" || len(req.Payload.Input) != 0 {
			return PublishResult{}, errors.New("message and payload are mutually exclusive")
		}
		req.Payload.Text = req.Message
	}
	if req.Type == "" {
		req.Type = "chat.message"
	}
	if req.SessionKey == "" {
		req.SessionKey = randID()
	}
	now := time.Now().UnixMilli()
	if req.ExpiresAt == 0 {
		req.ExpiresAt = now + messageTTL.Milliseconds()
	}
	env := protocol.MessageEnvelope{ID: randID(), Type: req.Type, From: s.nodeID, To: req.TargetNode, AgentID: req.AgentID, Payload: req.Payload, SessionKey: req.SessionKey, Metadata: req.Metadata, Ts: now, ExpiresAt: req.ExpiresAt, RequestID: req.RequestID, RunID: req.RunID, Seq: req.Seq, ProtocolVersion: protocol.WireVersion}
	if err := validateEnvelope(env, now); err != nil {
		return PublishResult{}, err
	}
	if s.identity == nil {
		return PublishResult{}, errors.New("signing identity is required")
	}
	subject := inboxSubject(req.TargetNode)
	data, err := protocol.SignedBytes(subject, env)
	if err != nil {
		return PublishResult{}, err
	}
	env.Sig = identity.Sign(s.identity.PrivateKey, data)
	encoded, err := json.Marshal(env)
	if err != nil {
		return PublishResult{}, err
	}
	if len(encoded) > protocol.MaxMessageBytes {
		return PublishResult{}, errors.New("message exceeds size limit")
	}
	if err := s.bus.Publish(subject, encoded); err != nil {
		return PublishResult{}, err
	}
	s.log.Info("message published", "messageId", env.ID, "type", env.Type, "to", env.To)
	return PublishResult{MessageID: env.ID, SessionKey: env.SessionKey}, nil
}

func validateEnvelope(env protocol.MessageEnvelope, now int64) error {
	if env.ProtocolVersion != protocol.WireVersion {
		return errors.New("unsupported protocol version")
	}
	if !validToken(env.ID) || !validToken(env.From) || !validToken(env.To) || len(env.Type) == 0 || len(env.Type) > 128 || len(env.SessionKey) > 256 || len(env.AgentID) > 128 {
		return errors.New("invalid message identity or route")
	}
	if env.AgentID != "" && !validToken(env.AgentID) {
		return errors.New("invalid agentId")
	}
	for _, c := range env.Type {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_') {
			return errors.New("invalid message type")
		}
	}
	if env.Ts <= 0 || env.Ts > now+30000 || env.ExpiresAt <= now || env.ExpiresAt <= env.Ts || env.ExpiresAt-env.Ts > messageTTL.Milliseconds() || env.Ts < now-messageTTL.Milliseconds() {
		return errors.New("message expired or timestamp outside window")
	}
	if strings.HasPrefix(env.Type, "run.") && (env.RequestID == "" || env.RunID == "" || env.Seq == 0) {
		return errors.New("execution event requires requestId, runId and seq")
	}
	return nil
}

func (s *Service) RecentMessages(limit int) []protocol.MessageEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.inbox) {
		limit = len(s.inbox)
	}
	// Decode copies nested metadata/Input too, so API clients cannot mutate state.
	data, _ := json.Marshal(s.inbox[len(s.inbox)-limit:])
	var out []protocol.MessageEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&out)
	return out
}

func (s *Service) handleInbox(subject string, data []byte) {
	var env protocol.MessageEnvelope
	if err := protocol.DecodeStrict(data, &env); err != nil {
		s.log.Warn("reject invalid message", "error", err)
		return
	}
	now := time.Now().UnixMilli()
	if subject != inboxSubject(s.nodeID) || env.To != s.nodeID {
		return
	}
	if err := validateEnvelope(env, now); err != nil {
		s.log.Warn("reject message contract", "messageId", env.ID, "error", err)
		return
	}
	peer, ok := s.peers.Get(env.From)
	if env.From != s.nodeID && (!ok || (s.trustMode != "open" && (peer.TrustStatus != types.TrustTrusted || peer.AuthStatus != types.AuthAuthenticated))) {
		return
	}
	pub, err := s.peerPublicKey(env.From)
	if err != nil {
		return
	}
	input, err := protocol.SignedBytes(subject, env)
	if err != nil || !identity.Verify(pub, input, env.Sig) {
		s.log.Warn("reject invalid message signature", "messageId", env.ID)
		return
	}
	s.mu.Lock()
	key := env.From + "/" + env.ID
	if now-s.lastReplayGC >= 1000 || len(s.seen) >= maxReplay {
		for id, expires := range s.seen {
			if expires <= now {
				delete(s.seen, id)
			}
		}
		s.lastReplayGC = now
	}
	if _, exists := s.seen[key]; exists {
		s.mu.Unlock()
		return
	}
	if len(s.seen) >= maxReplay {
		s.mu.Unlock()
		s.log.Warn("message replay window full")
		return
	}
	s.seen[key] = env.ExpiresAt
	s.inbox = append(s.inbox, env)
	if len(s.inbox) > maxRecent {
		copy(s.inbox, s.inbox[len(s.inbox)-maxRecent:])
		s.inbox = s.inbox[:maxRecent]
	}
	s.mu.Unlock()
	s.maybeDeliver(env)
}

func (s *Service) maybeDeliver(env protocol.MessageEnvelope) {
	if env.Type != "transfer.available" && !isDeliverableType(env.Type, s.deliverablePrefixes) && !strings.HasPrefix(env.Type, "run.") {
		return
	}
	// Run events can be consumed by webhook integrations, never sent back to LLMs.
	key := env.From + "/" + env.AgentID + "/" + env.SessionKey
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	queue := s.queues[h.Sum32()%workerCount]
	select {
	case <-s.ctx.Done():
	case queue <- env:
	default:
		if !isFeedbackType(env.Type) {
			_ = s.publishEvent(env, randID(), 1, "run.failed", protocol.MessagePayload{Code: "execution.overloaded", Stage: "queue"})
		}
	}
}

func (s *Service) work(queue <-chan protocol.MessageEnvelope) {
	defer s.workers.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case env := <-queue:
			if s.ctx.Err() != nil {
				return
			}
			s.deliver(env)
		}
	}
}

func (s *Service) deliver(env protocol.MessageEnvelope) {
	s.mu.Lock()
	handler, transfer := s.handler, s.transferHandler
	s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	if env.ExpiresAt <= time.Now().UnixMilli() {
		if !isFeedbackType(env.Type) {
			_ = s.publishEvent(env, randID(), 1, "run.failed", protocol.MessagePayload{Code: "execution.expired", Stage: "queue"})
		}
		return
	}
	peer, known := s.peers.Get(env.From)
	if env.From != s.nodeID && (!known || (s.trustMode != "open" && (peer.TrustStatus != types.TrustTrusted || peer.AuthStatus != types.AuthAuthenticated))) {
		return
	}
	if env.Type == "transfer.available" {
		if transfer != nil {
			transfer(env)
		}
		return
	}
	if handler == nil {
		return
	}
	feedback := isFeedbackType(env.Type)
	runID := randID()
	var seq uint64
	emit := func(kind string, payload protocol.MessagePayload) error {
		if feedback {
			return nil
		}
		seq++
		return s.publishEvent(env, runID, seq, kind, payload)
	}
	streamed := false
	ctx, cancel := context.WithDeadline(s.ctx, time.UnixMilli(env.ExpiresAt))
	defer cancel()
	if !feedback {
		if err := emit("run.started", protocol.MessagePayload{}); err != nil {
			s.log.Warn("publish execution start failed", "error", err)
			return
		}
	}
	result, err := handler.HandleMessage(IncomingMessage{Context: ctx, MessageID: env.ID, RequestID: env.RequestID, RunID: env.RunID, Seq: env.Seq, Type: env.Type, AgentID: env.AgentID, From: env.From, To: env.To, Message: env.Payload.Text, Payload: env.Payload, SessionKey: env.SessionKey, Metadata: env.Metadata, Emit: func(event adapter.OutputEvent) error {
		streamed = true
		return emit("run.output.delta", protocol.MessagePayload{Text: event.Text, Channel: "answer"})
	}})
	if feedback {
		if err != nil {
			s.log.Warn("deliver execution feedback failed", "error", err)
		}
		return
	}
	if err != nil {
		payload := protocol.MessagePayload{Code: "execution.failed", Stage: "adapter"}
		var execErr *adapter.ExecutionError
		if errors.As(err, &execErr) {
			payload.Code = execErr.Code
			payload.Stage = execErr.Stage
			payload.Indeterminate = execErr.Indeterminate
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			payload.Code = "execution.interrupted"
			payload.Indeterminate = true
		}
		if sendErr := emit("run.failed", payload); sendErr != nil {
			s.log.Warn("publish execution failure failed", "error", sendErr)
		}
		return
	}
	kind := "run.completed"
	if result.Accepted {
		kind = "run.accepted"
	}
	if !streamed && result.Reply != "" {
		for rest := result.Reply; rest != ""; {
			size := min(len(rest), 8192)
			for size < len(rest) && size > 0 && !utf8.RuneStart(rest[size]) {
				size--
			}
			if err := emit("run.output.delta", protocol.MessagePayload{Text: rest[:size], Channel: "answer"}); err != nil {
				s.log.Warn("publish execution output failed", "error", err)
				return
			}
			rest = rest[size:]
		}
	}
	if err := emit(kind, protocol.MessagePayload{AdapterRunID: result.RunID}); err != nil {
		s.log.Warn("publish execution result failed", "error", err)
	}
}

func (s *Service) publishEvent(orig protocol.MessageEnvelope, runID string, seq uint64, kind string, payload protocol.MessagePayload) error {
	_, err := s.Publish(PublishRequest{TargetNode: orig.From, Type: kind, SessionKey: orig.SessionKey, RequestID: orig.ID, RunID: runID, Seq: seq, Payload: payload})
	return err
}

func isDeliverableType(t string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(t, p+".") {
			return true
		}
	}
	return false
}

func (s *Service) peerPublicKey(peerNode string) (ed25519.PublicKey, error) {
	if peerNode == s.nodeID {
		if s.identity == nil {
			return nil, errors.New("identity missing")
		}
		return s.identity.PublicKey, nil
	}
	peer, ok := s.peers.Get(peerNode)
	if !ok {
		return nil, errors.New("peer not found")
	}
	value, _ := peer.Metadata["publicKey"].(string)
	return identity.DecodePublicKey(value)
}

func randID() string { return rand.Text() }
