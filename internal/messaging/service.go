package messaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/config"
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
)

var ErrIdempotencyConflict = errors.New("idempotency key already used for different content")

type PublishRequest struct {
	TargetBot      string
	Type           string
	AgentID        string
	Message        string
	Payload        protocol.MessagePayload
	SessionKey     string
	Metadata       map[string]any
	ExpiresAt      int64
	RequestID      string
	RunID          string
	Seq            uint64
	IdempotencyKey string
}

type PublishResult struct {
	MessageID  string
	SessionKey string
}

type TransferHandler func(env protocol.MessageEnvelope) error

type Service struct {
	mu                  sync.Mutex
	publishMu           sync.Mutex
	log                 *slog.Logger
	peers               *discovery.Registry
	bus                 *natsbus.Client
	botID               string
	identity            *identity.Identity
	trustMode           string
	deliverablePrefixes []string
	role                string
	handler             MessageHandler
	transferHandler     TransferHandler
	ctx                 context.Context
	cancel              context.CancelFunc
	workers             sync.WaitGroup
	wake                [workerCount]chan struct{}
	sub                 *nats.Subscription
	started             bool
	journal             *messageJournal
	limiter             *adapter.BotLimiter
}

func NewService(log *slog.Logger, peers *discovery.Registry, bus *natsbus.Client, botID string, id *identity.Identity, trustMode string, deliverablePrefixes []string) *Service {
	if len(deliverablePrefixes) == 0 {
		deliverablePrefixes = []string{"chat", "task"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{log: log, peers: peers, bus: bus, botID: botID, identity: id, role: config.RoleAgent, trustMode: trustMode, deliverablePrefixes: append([]string(nil), deliverablePrefixes...), ctx: ctx, cancel: cancel}
	for i := range s.wake {
		s.wake[i] = make(chan struct{}, 1)
	}
	return s
}

// SetStatePath opens and exclusively locks the durable journal before Start.
func (s *Service) SetStatePath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.journal != nil {
		return errors.New("messaging state already initialized")
	}
	journal, err := openMessageJournal(path)
	if err != nil {
		return err
	}
	s.journal = journal
	return nil
}

// SetRole selects business dispatch before the durable inbox starts. Console
// persists passive traffic and rejects execution without an execution resource.
func (s *Service) SetRole(role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.role = role
	}
}

func (s *Service) SetMessageHandler(handler MessageHandler) {
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
}

// HandleLocalNotification shares role dispatch with inbox deliveries. The file
// module owns durable file records; Console never turns its callback into a Run.
func (s *Service) HandleLocalNotification(msg IncomingMessage) error {
	s.mu.Lock()
	role, handler := s.role, s.handler
	s.mu.Unlock()
	if role == config.RoleConsole {
		return nil
	}
	if handler == nil {
		return errors.New("message handler unavailable")
	}
	_, err := handler.HandleMessage(msg)
	return err
}

func (s *Service) SetTransferHandler(handler TransferHandler) {
	s.mu.Lock()
	s.transferHandler = handler
	s.mu.Unlock()
}

// SetExecutionLimiter configures bounded durable admission before Start.
func (s *Service) SetExecutionLimiter(limiter *adapter.BotLimiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		s.limiter = limiter
	}
}

func (s *Service) Start() error {
	if s.bus == nil {
		return errors.New("nats client is required")
	}
	s.mu.Lock()
	if s.started || s.journal == nil || s.ctx.Err() != nil {
		s.mu.Unlock()
		return errors.New("messaging already started, closed, or durable state uninitialized")
	}
	sub, err := s.bus.MessagingInbox(s.botID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.sub, s.started = sub, true
	var interrupted []admittedWork
	for _, work := range s.journal.work {
		if work.State == "admitted" {
			interrupted = append(interrupted, *work)
		}
	}
	s.mu.Unlock()
	// Never blindly re-execute work admitted before a process crash. Feedback
	// delivery is non-executing and can safely be retried with its stable ID.
	for _, work := range interrupted {
		if isFeedbackType(work.Message.Type) || work.Message.Type == "transfer.available" {
			if err := s.setWork(work, "queued"); err != nil {
				s.Close()
				return err
			}
		} else {
			if err := s.emit(&work, "run.failed", protocol.MessagePayload{Code: "execution.interrupted", Stage: "restart", Indeterminate: true}, "done"); err != nil {
				s.Close()
				return err
			}
		}
	}
	for i := range s.wake {
		s.workers.Add(1)
		go s.work(i)
	}
	s.workers.Add(2)
	go s.receive()
	go s.publishPending()
	return nil
}

func (s *Service) Close() {
	s.cancel()
	s.workers.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	// Unsubscribe would delete a pull consumer created by this subscription.
	// Drain/connection close is sufficient; keep the durable consumer on server.
	if s.journal != nil && s.journal.file != nil {
		_ = s.journal.file.Close()
		s.journal.file = nil
	}
}

func inboxSubject(botID string) string { return "clawsynapse.msg." + botID + ".inbox" }

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

func normalizeRequest(req PublishRequest) (PublishRequest, string, error) {
	if !validToken(req.TargetBot) {
		return req, "", errors.New("invalid targetBot")
	}
	if len(req.IdempotencyKey) > 512 {
		return req, "", errors.New("idempotency key exceeds limit")
	}
	if req.Message != "" {
		if req.Payload.Text != "" || req.Payload.Instruction != "" || len(req.Payload.Input) != 0 {
			return req, "", errors.New("message and payload are mutually exclusive")
		}
		req.Payload.Text, req.Message = req.Message, ""
	}
	if req.Type == "" {
		req.Type = "chat.message"
	}
	if len(req.Payload.Input) != 0 {
		var input any
		decoder := json.NewDecoder(bytes.NewReader(req.Payload.Input))
		decoder.UseNumber()
		if err := decoder.Decode(&input); err != nil {
			return req, "", err
		}
		canonical, err := json.Marshal(input)
		if err != nil {
			return req, "", err
		}
		req.Payload.Input = canonical
	}
	canonical := req
	canonical.IdempotencyKey = ""
	data, err := json.Marshal(canonical)
	if err != nil {
		return req, "", err
	}
	digest := sha256.Sum256(data)
	return req, hex.EncodeToString(digest[:]), nil
}

func (s *Service) Publish(req PublishRequest) (PublishResult, error) {
	pub, err := s.enqueue(req, nil)
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{MessageID: pub.Message.ID, SessionKey: pub.Message.SessionKey}
	if err := s.flushPublication(pub.Message.ID); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Service) enqueue(req PublishRequest, work *admittedWork) (publication, error) {
	req, fingerprint, err := normalizeRequest(req)
	if err != nil {
		return publication{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return publication{}, s.ctx.Err()
	}
	if s.journal == nil || s.journal.file == nil {
		return publication{}, errors.New("durable messaging state is required")
	}
	if id, ok := s.journal.keys[req.IdempotencyKey]; req.IdempotencyKey != "" && ok {
		pub := s.journal.publications[id]
		if pub.Fingerprint != fingerprint {
			return publication{}, ErrIdempotencyConflict
		}
		return *pub, nil
	}
	if work == nil {
		peer, ok := s.peers.Get(req.TargetBot)
		if !ok {
			return publication{}, errors.New("target peer not found")
		}
		if s.trustMode != "open" && (peer.TrustStatus != types.TrustTrusted || peer.AuthStatus != types.AuthAuthenticated) {
			return publication{}, protocol.NewError("control.unauthorized", "peer is not authenticated and trusted")
		}
	}
	if req.SessionKey == "" {
		req.SessionKey = randID()
	}
	now := time.Now().UnixMilli()
	if req.ExpiresAt == 0 {
		req.ExpiresAt = now + messageTTL.Milliseconds()
	}
	env := protocol.MessageEnvelope{ID: randID(), Type: req.Type, From: s.botID, To: req.TargetBot, AgentID: req.AgentID, Payload: req.Payload, SessionKey: req.SessionKey, Metadata: req.Metadata, Ts: now, ExpiresAt: req.ExpiresAt, RequestID: req.RequestID, RunID: req.RunID, Seq: req.Seq, ProtocolVersion: protocol.WireVersion}
	if err := validateEnvelope(env, now); err != nil {
		return publication{}, err
	}
	if s.identity == nil {
		return publication{}, errors.New("signing identity is required")
	}
	data, err := protocol.SignedBytes(inboxSubject(env.To), env)
	if err != nil {
		return publication{}, err
	}
	env.Sig = identity.Sign(s.identity.PrivateKey, data)
	encoded, err := json.Marshal(env)
	if err != nil {
		return publication{}, err
	}
	if len(encoded) > protocol.MaxMessageBytes {
		return publication{}, errors.New("message exceeds size limit")
	}
	pub := publication{Message: cloneJSON(env), Key: req.IdempotencyKey, Fingerprint: fingerprint}
	if env.RunID != "" {
		pub.Previous = s.journal.tails[eventKey(env)]
	}
	if err := s.journal.append(journalRecord{Publication: &pub, Work: work}); err != nil {
		return publication{}, err
	}
	return pub, nil
}

func (s *Service) flushPublication(id string) error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	s.mu.Lock()
	pub := s.journal.publications[id]
	if pub.Published {
		s.mu.Unlock()
		return nil
	}
	if previous := s.journal.publications[pub.Previous]; previous != nil && !previous.Published {
		s.mu.Unlock()
		return errors.New("previous execution event is still pending")
	}
	env := pub.Message
	s.mu.Unlock()
	if s.bus == nil {
		return errors.New("nats client is required")
	}
	if !s.trusted(env.To) {
		return protocol.NewError("control.unauthorized", "peer is no longer authenticated and trusted")
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if err := s.bus.PublishMessage(ctx, inboxSubject(env.To), data, env.From+"/"+env.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal.append(journalRecord{Published: id})
}

func (s *Service) publishPending() {
	defer s.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		pending := s.journal.pending
		s.journal.pending = nil
		s.mu.Unlock()
		for _, id := range pending {
			if s.ctx.Err() != nil {
				return
			}
			if err := s.flushPublication(id); err != nil {
				s.log.Debug("durable publication pending", "messageId", id, "error", err)
				s.mu.Lock()
				s.journal.pending = append(s.journal.pending, id)
				s.mu.Unlock()
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
	if env.Payload.Message != "" && (env.Type != "run.failed" || len(env.Payload.Message) > 512 || !utf8.ValidString(env.Payload.Message)) {
		return errors.New("failure message must be a bounded UTF-8 run.failed diagnostic")
	}
	return nil
}

func (s *Service) RecentMessages(limit int) []protocol.MessageEnvelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []protocol.MessageEnvelope{}
	if s.journal == nil {
		return out
	}
	inbox := s.journal.inbox
	if limit <= 0 || limit > len(inbox) {
		limit = len(inbox)
	}
	for _, item := range inbox[len(inbox)-limit:] {
		out = append(out, item.MessageEnvelope)
	}
	return cloneJSON(out)
}

func (s *Service) RecentMessagesPage(limit int) MessagesPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := MessagesPage{Items: []StoredMessage{}}
	if s.journal == nil {
		return page
	}
	inbox := s.journal.inbox
	if limit <= 0 || limit > len(inbox) {
		limit = len(inbox)
	}
	if len(inbox) != 0 {
		page.Cursor = inbox[len(inbox)-1].Cursor
	}
	page.Items = append(page.Items, inbox[len(inbox)-limit:]...)
	return cloneJSON(page)
}

func (s *Service) MessagesAfter(after uint64, limit int) MessagesPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := MessagesPage{Items: []StoredMessage{}, Cursor: after}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if s.journal == nil {
		return page
	}
	start := int(min(after, uint64(len(s.journal.inbox))))
	end := min(start+limit, len(s.journal.inbox))
	page.Items = append(page.Items, s.journal.inbox[start:end]...)
	if len(page.Items) != 0 {
		page.Cursor = page.Items[len(page.Items)-1].Cursor
	}
	return cloneJSON(page)
}

func (s *Service) Request(messageID string) (RequestState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journal == nil {
		return RequestState{}, false
	}
	pub := s.journal.publications[messageID]
	if pub == nil {
		return RequestState{}, false
	}
	state := RequestState{Message: pub.Message, Publication: "pending", Status: "sending", Events: []protocol.MessageEnvelope{}}
	if pub.Published {
		state.Publication, state.Status = "published", "sent"
	}
	for _, item := range s.journal.inbox {
		event := item.MessageEnvelope
		if event.RequestID != messageID || event.From != pub.Message.To || event.To != pub.Message.From || event.AgentID != pub.Message.AgentID || event.SessionKey != pub.Message.SessionKey || !strings.HasPrefix(event.Type, "run.") {
			continue
		}
		state.Events = append(state.Events, event)
		switch event.Type {
		case "run.started", "run.output.delta":
			state.Status = "running"
		case "run.accepted":
			state.Status = "accepted"
		case "run.completed":
			state.Status = "completed"
		case "run.failed":
			state.Status = "failed"
			if event.Payload.Indeterminate {
				state.Status = "indeterminate"
			}
		}
	}
	return cloneJSON(state), true
}

func (s *Service) receive() {
	defer s.workers.Done()
	for s.ctx.Err() == nil {
		messages, err := s.sub.Fetch(32, nats.MaxWait(time.Second))
		if err != nil && !errors.Is(err, nats.ErrTimeout) && s.ctx.Err() == nil {
			s.log.Debug("durable inbox waiting", "error", err)
		}
		for _, message := range messages {
			if s.handleInbox(message.Subject, message.Data) {
				_ = message.AckSync()
			} else {
				_ = message.NakWithDelay(time.Second)
			}
		}
	}
}

// handleInbox returns false for transient persistence or discovery/authentication
// delays. Invalid or explicitly revoked deliveries are discarded.
func (s *Service) handleInbox(subject string, data []byte) bool {
	var env protocol.MessageEnvelope
	if protocol.DecodeStrict(data, &env) != nil || subject != inboxSubject(s.botID) || env.To != s.botID {
		return true
	}
	now := time.Now().UnixMilli()
	validationTime := now
	// Historical signed messages remain auditable. Expired commands are stored
	// but fail in the worker before adapter admission; feedback never executes.
	if env.Ts <= now {
		validationTime = env.Ts
	}
	if err := validateEnvelope(env, validationTime); err != nil {
		return true
	}
	if !s.trusted(env.From) {
		return s.revoked(env.From)
	}
	pub, err := s.peerPublicKey(env.From)
	if err != nil {
		return true
	}
	input, err := protocol.SignedBytes(subject, env)
	if err != nil || !identity.Verify(pub, input, env.Sig) {
		return true
	}
	s.mu.Lock()
	if s.journal == nil {
		s.mu.Unlock()
		return false
	}
	key := messageKey(env)
	if s.journal.received[key] {
		s.mu.Unlock()
		return true
	}
	if s.role != config.RoleConsole && s.limiter != nil && !isFeedbackType(env.Type) && env.Type != "transfer.available" &&
		isDeliverableType(env.Type, s.deliverablePrefixes) && s.journal.pendingCommands >= s.limiter.Capacity() {
		s.mu.Unlock()
		return false
	}
	item := StoredMessage{MessageEnvelope: env, Cursor: uint64(len(s.journal.inbox)) + 1}
	record := journalRecord{Received: &item}
	if env.Type == "transfer.available" || isDeliverableType(env.Type, s.deliverablePrefixes) || isFeedbackType(env.Type) {
		record.Work = &admittedWork{Key: key, Message: env, State: "queued"}
		if !isFeedbackType(env.Type) && env.Type != "transfer.available" && (s.role != config.RoleConsole || env.Type != "chat.message") {
			record.Work.RunID = randID()
		}
	}
	err = s.journal.append(record)
	s.mu.Unlock()
	if err != nil {
		s.log.Error("persist durable inbox failed", "error", err)
		return false
	}
	select {
	case s.wake[workShard(env)] <- struct{}{}:
	default:
	}
	return true
}

func workShard(env protocol.MessageEnvelope) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(env.From + "/" + env.AgentID + "/" + env.SessionKey))
	return int(h.Sum32() % workerCount)
}

func (s *Service) work(shard int) {
	defer s.workers.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for s.ctx.Err() == nil {
			s.mu.Lock()
			var next *admittedWork
			for s.journal.workHeads[shard] < len(s.journal.workQueues[shard]) {
				key := s.journal.workQueues[shard][s.journal.workHeads[shard]]
				work := s.journal.work[key]
				if work.State == "done" {
					s.journal.workHeads[shard]++
					continue
				}
				copy := *work
				next = &copy
				break
			}
			s.mu.Unlock()
			if next == nil {
				break
			}
			if next.State == "admitted" {
				if isFeedbackType(next.Message.Type) || next.Message.Type == "transfer.available" {
					if err := s.setWork(*next, "queued"); err != nil {
						break
					}
				} else if err := s.emit(next, "run.failed", protocol.MessagePayload{Code: "execution.interrupted", Stage: "journal", Indeterminate: true}, "done"); err != nil {
					break
				}
				continue
			}
			if !s.deliver(*next) {
				break
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake[shard]:
		case <-ticker.C:
		}
	}
}

func (s *Service) setWork(work admittedWork, state string) error {
	work.State = state
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.journal.append(journalRecord{Work: &work})
}

func (s *Service) trusted(bot string) bool {
	if bot == s.botID {
		return true
	}
	peer, ok := s.peers.Get(bot)
	return ok && (s.trustMode == "open" || (peer.TrustStatus == types.TrustTrusted && peer.AuthStatus == types.AuthAuthenticated))
}

func (s *Service) revoked(bot string) bool {
	peer, ok := s.peers.Get(bot)
	return ok && (peer.TrustStatus == types.TrustRevoked || peer.TrustStatus == types.TrustRejected || peer.AuthStatus == types.AuthRejected)
}

// deliver returns false when this shard must wait before retrying. Only a proven
// pre-admission transport failure can move a command back to queued.
func (s *Service) deliver(work admittedWork) bool {
	env := work.Message
	feedback := isFeedbackType(env.Type)
	if !s.trusted(env.From) {
		if s.revoked(env.From) {
			return s.setWork(work, "done") == nil
		}
		return false
	}
	s.mu.Lock()
	handler, transfer, role := s.handler, s.transferHandler, s.role
	s.mu.Unlock()
	if role == config.RoleConsole && (feedback || env.Type == "chat.message") {
		return s.setWork(work, "done") == nil
	}
	if !feedback && env.Type != "transfer.available" && env.ExpiresAt <= time.Now().UnixMilli() {
		return s.emit(&work, "run.failed", protocol.MessagePayload{Code: "execution.expired", Stage: "queue"}, "done") == nil
	}
	if env.Type == "transfer.available" {
		if env.ExpiresAt <= time.Now().UnixMilli() {
			return s.setWork(work, "done") == nil
		}
		if transfer == nil {
			return false
		}
		if err := s.setWork(work, "admitted"); err != nil {
			return false
		}
		if err := transfer(env); err != nil {
			_ = s.setWork(work, "queued")
			return false
		}
		return s.setWork(work, "done") == nil
	}
	if role == config.RoleConsole {
		return s.emit(&work, "run.failed", protocol.MessagePayload{Code: "console.execution_unsupported", Stage: "role"}, "done") == nil
	}
	if handler == nil {
		return false
	}
	if validator, ok := handler.(interface{ ValidateMessage(IncomingMessage) error }); ok && !feedback {
		if err := validator.ValidateMessage(IncomingMessage{Type: env.Type, AgentID: env.AgentID}); err != nil {
			return s.emit(&work, "run.failed", protocol.MessagePayload{Code: "unsupported_agent", Stage: "validation"}, "done") == nil
		}
	}
	ctx := s.ctx
	cancel := func() {}
	if !feedback {
		ctx, cancel = context.WithDeadline(ctx, time.UnixMilli(env.ExpiresAt))
	}
	defer cancel()
	if !feedback {
		executionCtx, release, err := acquireExecution(ctx, s.limiter)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, adapter.ErrQueueFull) || errors.Is(err, adapter.ErrSchedulerClosed) {
				return false
			}
			return s.emit(&work, "run.failed", protocol.MessagePayload{Code: "execution.expired", Stage: "queue"}, "done") == nil
		}
		ctx = executionCtx
		defer release()
		// Cancellation or expiry while waiting must never become admitted work.
		if ctx.Err() != nil || env.ExpiresAt <= time.Now().UnixMilli() {
			if s.ctx.Err() != nil {
				return false
			}
			return s.emit(&work, "run.failed", protocol.MessagePayload{Code: "execution.expired", Stage: "queue"}, "done") == nil
		}
		if !s.trusted(env.From) {
			if s.revoked(env.From) {
				return s.setWork(work, "done") == nil
			}
			return false
		}
	}
	if err := s.setWork(work, "admitted"); err != nil {
		return false
	}
	if !feedback && work.Seq == 0 {
		if err := s.emit(&work, "run.started", protocol.MessagePayload{}, "admitted"); err != nil {
			return false
		}
	}
	streamed := false
	requestID, runID, sequence := env.RequestID, env.RunID, env.Seq
	if !feedback {
		requestID, runID, sequence = env.ID, work.RunID, work.Seq
	}
	result, err := handler.HandleMessage(IncomingMessage{Context: ctx, MessageID: env.ID, RequestID: requestID, RunID: runID, Seq: sequence, Type: env.Type, AgentID: env.AgentID, From: env.From, To: env.To, Message: env.Payload.Text, Payload: env.Payload, SessionKey: env.SessionKey, Metadata: env.Metadata, Emit: func(event adapter.OutputEvent) error {
		if feedback {
			return nil
		}
		streamed = true
		return s.emit(&work, "run.output.delta", protocol.MessagePayload{Text: event.Text, Channel: "answer"}, "admitted")
	}})
	if feedback {
		if err != nil {
			var retryable interface{ Retryable() bool }
			if errors.As(err, &retryable) && retryable.Retryable() {
				_ = s.setWork(work, "queued")
				return false
			}
			s.log.Warn("discard permanent feedback delivery failure", "type", env.Type, "error", err)
		}
		return s.setWork(work, "done") == nil
	}
	if err != nil {
		var retryable interface{ Retryable() bool }
		if !streamed && errors.As(err, &retryable) && retryable.Retryable() {
			_ = s.setWork(work, "queued")
			return false
		}
		payload := protocol.MessagePayload{Code: "execution.failed", Stage: "adapter"}
		var execErr *adapter.ExecutionError
		if errors.As(err, &execErr) {
			payload.Code, payload.Stage, payload.Indeterminate = execErr.Code, execErr.Stage, execErr.Indeterminate
			payload.Message = execErr.SafeMessage()
			if execErr.Code == "webhook_failed" && execErr.Err != nil {
				payload.Text = execErr.Err.Error()
				if len(payload.Text) > 1000 {
					payload.Text = payload.Text[:1000]
				}
			}
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			payload.Code, payload.Indeterminate = "execution.interrupted", true
		}
		// Published answer output is evidence of possible effects, even when an
		// adapter incorrectly reports a later validation rejection as certain.
		payload.Indeterminate = payload.Indeterminate || streamed
		return s.emit(&work, "run.failed", payload, "done") == nil
	}
	if !streamed && result.Reply != "" {
		for rest := result.Reply; rest != ""; {
			size := min(len(rest), 8192)
			for size < len(rest) && size > 0 && !utf8.RuneStart(rest[size]) {
				size--
			}
			if err := s.emit(&work, "run.output.delta", protocol.MessagePayload{Text: rest[:size], Channel: "answer"}, "admitted"); err != nil {
				return false
			}
			rest = rest[size:]
		}
	}
	kind := "run.completed"
	if result.Accepted {
		kind = "run.accepted"
	}
	return s.emit(&work, kind, protocol.MessagePayload{AdapterRunID: result.RunID}, "done") == nil
}

func (s *Service) emit(work *admittedWork, kind string, payload protocol.MessagePayload, state string) error {
	next := *work
	next.Seq++
	next.State = state
	orig := work.Message
	_, err := s.enqueue(PublishRequest{TargetBot: orig.From, AgentID: orig.AgentID, Type: kind, SessionKey: orig.SessionKey, RequestID: orig.ID, RunID: work.RunID, Seq: next.Seq, Payload: payload, Metadata: eventMetadata(orig.Metadata), IdempotencyKey: "run/" + orig.From + "/" + orig.ID + "/" + work.RunID + "/" + stringSeq(next.Seq)}, &next)
	if err != nil {
		return err
	}
	*work = next
	// Event durability is local first. The outbox retries broker outages without
	// forcing an already-admitted adapter execution to run again.
	return nil
}

func eventMetadata(metadata map[string]any) map[string]any {
	chat, ok := metadata["dsynapseChat"].(map[string]any)
	if !ok {
		return nil
	}
	identity := map[string]any{}
	for _, key := range []string{"chatId", "messageId"} {
		if value, ok := chat[key].(string); ok {
			identity[key] = value
		}
	}
	return map[string]any{"dsynapseChat": identity}
}

func stringSeq(seq uint64) string {
	const digits = "0123456789"
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = digits[seq%10]
		seq /= 10
		if seq == 0 {
			return string(b[i:])
		}
	}
}

func isDeliverableType(t string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(t, prefix+".") {
			return true
		}
	}
	return false
}

func (s *Service) peerPublicKey(bot string) (ed25519.PublicKey, error) {
	if bot == s.botID {
		if s.identity == nil {
			return nil, errors.New("identity missing")
		}
		return s.identity.PublicKey, nil
	}
	peer, ok := s.peers.Get(bot)
	if !ok {
		return nil, errors.New("peer not found")
	}
	value, _ := peer.Metadata["publicKey"].(string)
	return identity.DecodePublicKey(value)
}

func randID() string { return rand.Text() }
