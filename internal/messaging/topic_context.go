package messaging

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

// Only references to immutable received envelopes are journaled. Advancing a
// cursor or freezing a request never serializes a copy of the topic cache.
type topicUpdate struct {
	Scope   string `json:"scope"`
	Source  string `json:"source"`
	Command bool   `json:"command,omitempty"`
}

type topicLedger struct {
	events     map[uint64]protocol.TopicEvent
	identities map[string]uint64
	head       uint64
}

type topicBasis struct {
	Epoch                   string `json:"epoch"`
	MaterializedThrough     uint64 `json:"materializedThrough"`
	RequireResume           bool   `json:"requireResume"`
	NativePreviousRequestID string `json:"nativePreviousRequestId,omitempty"`
	Verified                bool   `json:"verified,omitempty"`
	ReplyDigest             string `json:"replyDigest,omitempty"`
	ReaderToken             string `json:"readerToken,omitempty"`
	ReaderThrough           uint64 `json:"readerThrough,omitempty"`
	Paused                  bool   `json:"paused,omitempty"`
}

type topicNativeState struct {
	Epoch       string                     `json:"epoch"`
	Scope       string                     `json:"scope"`
	Through     uint64                     `json:"through"`
	LastRequest string                     `json:"lastRequest,omitempty"`
	LastRun     string                     `json:"lastRun,omitempty"`
	Receipt     *adapter.TopicInputReceipt `json:"receipt,omitempty"`
	Paused      bool                       `json:"paused,omitempty"`
}

func topicScope(env protocol.MessageEnvelope) string {
	data, _ := json.Marshal([]string{env.From, env.AgentID, env.SessionKey})
	return string(data)
}

func (s *Service) topicEpochLocked(env protocol.MessageEnvelope) string {
	capable, ok := s.handler.(interface{ TopicContextIdentity() string })
	if !ok || s.role == config.RoleConsole {
		return ""
	}
	binding := capable.TopicContextIdentity()
	if binding == "" {
		return ""
	}
	data, _ := json.Marshal([]string{s.botID, binding, topicScope(env)})
	digest := sha256.Sum256(data)
	return "topic-" + hex.EncodeToString(digest[:])
}

func currentTopicEvent(env protocol.MessageEnvelope, turn protocol.TopicTurn) protocol.TopicEvent {
	return protocol.TopicEvent{Revision: turn.TurnRevision, Kind: "user.message", MessageID: turn.TurnID, Author: env.From, Text: env.Payload.Text}
}

func (j *messageJournal) applyTopic(record journalRecord) {
	if update := record.Topic; update != nil {
		env := j.receivedMessages[update.Source]
		ledger := j.topics[update.Scope]
		if ledger == nil {
			ledger = &topicLedger{events: make(map[uint64]protocol.TopicEvent), identities: make(map[string]uint64)}
			j.topics[update.Scope] = ledger
		}
		var events []protocol.TopicEvent
		if update.Command {
			turn, _, _ := protocol.ParseTopicTurn(env.Payload.Input)
			events = turn.Context.Events
			for _, event := range events {
				ledger.events[event.Revision] = event
				ledger.identities[topicEventIdentity(event)] = event.Revision
			}
			current := currentTopicEvent(env, turn)
			ledger.events[turn.TurnRevision] = current
			ledger.identities[topicEventIdentity(current)] = current.Revision
			j.topicTurns[update.Scope+"/"+turn.TurnID] = update.Source
		} else {
			sync, _ := protocol.ParseTopicSync(env.Payload.Input)
			events = sync.Events
			for _, event := range events {
				ledger.events[event.Revision] = event
				ledger.identities[topicEventIdentity(event)] = event.Revision
			}
		}
		for ledger.head != ^uint64(0) {
			if _, ok := ledger.events[ledger.head+1]; !ok {
				break
			}
			ledger.head++
		}
	}
	if state := record.TopicState; state != nil {
		j.topicStates[state.Epoch] = state
	}
}

func topicEventIdentity(event protocol.TopicEvent) string {
	if event.Kind == "bot.reply.completed" {
		event.MessageID = ""
	}
	data, _ := json.Marshal([]string{event.Kind, event.MessageID, event.BotID, event.RequestID, event.RunID})
	return string(data)
}

func (s *Service) validateTopicFactsLocked(env protocol.MessageEnvelope, events []protocol.TopicEvent) error {
	ledger := s.journal.topics[topicScope(env)]
	for _, event := range events {
		if event.Kind == "user.message" && event.Author != env.From {
			return errors.New("topic.source_scope_mismatch")
		}
		if ledger != nil {
			if old, exists := ledger.events[event.Revision]; exists && old != event {
				return errors.New("topic.revision_conflict")
			}
			if revision, exists := ledger.identities[topicEventIdentity(event)]; exists && revision != event.Revision {
				return errors.New("topic.event_identity_conflict")
			}
		}
	}
	return nil
}

func topicPayloadOnly(env protocol.MessageEnvelope) bool {
	p := env.Payload
	return env.SessionKey != "" && p.Text == "" && p.Instruction == "" && p.Channel == "" && p.Code == "" && p.Stage == "" && p.Message == "" && !p.Indeterminate && p.AdapterRunID == "" && env.RunID == "" && env.Seq == 0
}

// processTopicLocked handles recognized non-executing sync independently of
// worker shards. A command waiting for a gap can never block its own sync.
func (s *Service) processTopicLocked(env protocol.MessageEnvelope, item StoredMessage) (bool, error) {
	schema := protocol.TopicInputSchema(env.Payload.Input)
	if !protocol.IsTopicSchema(schema) || env.Type != "chat.context" {
		return false, nil
	}
	key := messageKey(env)
	if old, exists := s.journal.receivedMessages[key]; exists {
		if !sameTopicEnvelope(old, env) {
			return true, nil
		}
		return true, nil // cache and original receipt outbox committed together
	}
	record := journalRecord{Received: &item}
	if schema == protocol.TopicReceiptSchema {
		var receipt protocol.TopicReceipt
		if err := protocol.DecodeStrict(env.Payload.Input, &receipt); err != nil || !topicPayloadOnly(env) || receipt.SyncMessageID != env.RequestID {
			return true, nil
		}
		// Receipts are observable signed data, never adapter work. The sender
		// validates their exact publication correlation before using a cursor.
		return true, s.journal.append(record)
	}
	epoch := s.topicEpochLocked(env)
	code := ""
	var sync protocol.TopicSync
	if schema != protocol.TopicSyncSchema {
		code = "topic.unsupported_schema"
	} else if parsed, err := protocol.ParseTopicSync(env.Payload.Input); err != nil {
		code = "topic.invalid_sync"
		if strings.HasPrefix(err.Error(), "topic.") {
			code = err.Error()
		}
	} else {
		sync = parsed
		if !topicPayloadOnly(env) || env.RequestID != "" {
			code = "topic.invalid_sync_route"
		} else if epoch == "" {
			code = "topic.unsupported_adapter"
		} else if err := s.validateTopicFactsLocked(env, sync.Events); err != nil {
			code = err.Error()
		} else {
			record.Topic = &topicUpdate{Scope: topicScope(env), Source: key}
		}
	}
	through := uint64(0)
	if code == "" {
		through = s.prospectiveTopicHeadLocked(env, sync.Events, nil)
		through = min(through, sync.ThroughRevision)
	}
	pub, err := s.topicReceiptLocked(env, epoch, through, code)
	if err != nil {
		return true, err
	}
	record.Publication = &pub
	return true, s.journal.append(record)
}

func sameTopicEnvelope(a, b protocol.MessageEnvelope) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func (s *Service) prospectiveTopicHeadLocked(env protocol.MessageEnvelope, events []protocol.TopicEvent, current *protocol.TopicEvent) uint64 {
	ledger := s.journal.topics[topicScope(env)]
	head := uint64(0)
	if ledger != nil {
		head = ledger.head
	}
	// A validated chunk is contiguous; no complete ledger copy is necessary.
	for {
		if head == ^uint64(0) {
			return head
		}
		next := head + 1
		found := ledger != nil && ledger.events[next].Revision == next
		if !found && len(events) != 0 && next >= events[0].Revision && next <= events[len(events)-1].Revision {
			found = true
		}
		if !found && current != nil && current.Revision == next {
			found = true
		}
		if !found {
			return head
		}
		head = next
	}
}

func (s *Service) topicReceiptLocked(env protocol.MessageEnvelope, epoch string, through uint64, code string) (publication, error) {
	key := "topic-receipt/" + messageKey(env)
	if id := s.journal.keys[key]; id != "" {
		return *s.journal.publications[id], nil
	}
	if s.identity == nil {
		return publication{}, errors.New("signing identity is required")
	}
	input, _ := json.Marshal(protocol.TopicReceipt{Schema: protocol.TopicReceiptSchema, SyncMessageID: env.ID, StoredThroughRevision: through, NativeSessionEpoch: epoch, PolicyVersion: protocol.TopicPolicyVersion, Error: code})
	now := time.Now().UnixMilli()
	id := sha256.Sum256([]byte(s.botID + "/" + key))
	reply := protocol.MessageEnvelope{ID: hex.EncodeToString(id[:]), Type: "chat.context", From: s.botID, To: env.From, AgentID: env.AgentID, SessionKey: env.SessionKey, RequestID: env.ID, Payload: protocol.MessagePayload{Input: input}, Ts: now, ExpiresAt: now + messageTTL.Milliseconds(), ProtocolVersion: protocol.WireVersion}
	data, err := protocol.SignedBytes(inboxSubject(reply.To), reply)
	if err != nil {
		return publication{}, err
	}
	reply.Sig = identity.Sign(s.identity.PrivateKey, data)
	digest := sha256.Sum256(input)
	return publication{Message: reply, Key: key, Fingerprint: hex.EncodeToString(digest[:])}, nil
}

// prepareTopicCommandLocked attaches only immutable envelope references. Invalid
// topic schemas remain recognized commands and can never fall through to chat.
func (s *Service) prepareTopicCommandLocked(env protocol.MessageEnvelope, record *journalRecord) {
	turn, recognized, err := protocol.ParseTopicTurn(env.Payload.Input)
	if !recognized || record.Work == nil || isFeedbackType(env.Type) {
		return
	}
	fail := func(code string) { record.Work.TopicError = code }
	if err != nil {
		fail("topic.invalid_turn")
		return
	}
	if env.Type != "chat.message" || env.SessionKey == "" || env.Payload.Instruction != "" || env.RequestID != "" || env.RunID != "" || env.Seq != 0 {
		fail("topic.invalid_turn_route")
		return
	}
	current := currentTopicEvent(env, turn)
	for _, event := range turn.Context.Events {
		if event.Kind == "user.message" && event.MessageID == current.MessageID {
			fail("topic.event_identity_conflict")
			return
		}
	}
	if err := protocol.ValidateTopicBatch(protocol.TopicContextBatch{BaseRevision: turn.TurnRevision - 1, ThroughRevision: turn.TurnRevision, Events: []protocol.TopicEvent{current}, Digest: protocol.TopicEventsDigest([]protocol.TopicEvent{current})}); err != nil {
		fail("topic.invalid_current_event")
		return
	}
	if err := s.validateTopicFactsLocked(env, turn.Context.Events); err != nil {
		fail(err.Error())
		return
	}
	if err := s.validateTopicFactsLocked(env, []protocol.TopicEvent{current}); err != nil {
		fail(err.Error())
		return
	}
	scope := topicScope(env)
	if original := s.journal.topicTurns[scope+"/"+turn.TurnID]; original != "" && original != messageKey(env) {
		fail("topic.duplicate_turn")
		return
	}
	record.Topic = &topicUpdate{Scope: scope, Source: messageKey(env), Command: true}
}

func (s *Service) topicExecution(work *admittedWork) (*protocol.TopicExecution, bool, error) {
	if work.TopicError != "" {
		return nil, false, errors.New(work.TopicError)
	}
	turn, recognized, err := protocol.ParseTopicTurn(work.Message.Payload.Input)
	if !recognized {
		s.mu.Lock()
		defer s.mu.Unlock()
		if state := s.journal.topicStates[s.topicEpochLocked(work.Message)]; state != nil && state.Paused {
			return nil, true, nil
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("topic.invalid_turn")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	epoch := s.topicEpochLocked(work.Message)
	if epoch == "" {
		return nil, false, errors.New("topic.unsupported_adapter")
	}
	if epoch != turn.NativeSessionEpoch {
		return nil, false, errors.New("topic.session_changed")
	}
	state := s.journal.topicStates[epoch]
	if state != nil && state.Paused {
		return nil, true, nil
	}
	ledger := s.journal.topics[topicScope(work.Message)]
	if ledger == nil || ledger.head < turn.Context.ThroughRevision {
		return nil, true, nil
	}
	basis := work.Topic
	if basis == nil {
		basis = &topicBasis{Epoch: epoch}
		if state != nil && state.Receipt != nil {
			basis.MaterializedThrough, basis.RequireResume = state.Through, true
			basis.NativePreviousRequestID = state.LastRequest
			if turn.Context.ThroughRevision < state.Through || !s.topicPredecessorLocked(*work, turn, state.LastRequest) {
				return nil, false, errors.New("topic.stale_native_basis")
			}
		} else if !s.topicPredecessorLocked(*work, turn, "") {
			return nil, false, errors.New("topic.missing_native_basis")
		}
		work.Topic = basis
	} else if basis.Epoch != epoch || (state != nil && state.Through != basis.MaterializedThrough) {
		return nil, false, errors.New("topic.stale_native_basis")
	}
	events := s.requiredTopicEventsLocked(*work, turn, basis.MaterializedThrough, turn.Context.ThroughRevision)
	// The adapter decides whether facts fit inline; a real, scoped daemon reader
	// is available throughout this admitted request, never a synthetic summary.
	return &protocol.TopicExecution{Turn: turn, Events: events, MaterializedThrough: basis.MaterializedThrough, RequireResume: basis.RequireResume, NativePreviousRequestID: basis.NativePreviousRequestID, ReaderAvailable: true}, false, nil
}

// Certain failures do not move the native cursor. A frozen later request can
// name one as its predecessor, but only a durable same-epoch chain back to the
// last verified input is sufficient; an unknown or indeterminate link is not.
func (s *Service) topicPredecessorLocked(work admittedWork, turn protocol.TopicTurn, verified string) bool {
	requestID := turn.PreviousRequestID
	before := turn.TurnRevision
	for requestID != verified {
		if requestID == "" {
			return false
		}
		previous := s.journal.work[work.Message.From+"/"+requestID]
		if previous == nil || previous.State != "done" || previous.TerminalKind != "run.failed" || previous.Indeterminate || topicScope(previous.Message) != topicScope(work.Message) {
			return false
		}
		prior, recognized, err := protocol.ParseTopicTurn(previous.Message.Payload.Input)
		if err != nil || !recognized || prior.NativeSessionEpoch != turn.NativeSessionEpoch || prior.TurnRevision >= before {
			return false
		}
		requestID, before = prior.PreviousRequestID, prior.TurnRevision
	}
	return true
}

func (s *Service) knownOwnReplyLocked(work admittedWork, event protocol.TopicEvent) bool {
	if event.Kind != "bot.reply.completed" || event.BotID != s.botID {
		return false
	}
	previous := s.journal.work[work.Message.From+"/"+event.RequestID]
	if previous == nil || previous.State != "done" || previous.TerminalKind != "run.completed" || previous.RunID != event.RunID || topicScope(previous.Message) != topicScope(work.Message) || work.Topic == nil || previous.Topic == nil || previous.Topic.Epoch != work.Topic.Epoch || !previous.Topic.Verified || previous.Topic.ReplyDigest == "" {
		return false
	}
	digest := sha256.Sum256([]byte(event.Text))
	return previous.Topic.ReplyDigest == hex.EncodeToString(digest[:])
}

func (s *Service) requiredTopicEventsLocked(work admittedWork, turn protocol.TopicTurn, after, through uint64) []protocol.TopicEvent {
	out := []protocol.TopicEvent{}
	ledger := s.journal.topics[topicScope(work.Message)]
	if ledger == nil {
		return out
	}
	for revision := after; revision < through; {
		revision++
		event, ok := ledger.events[revision]
		if ok && !s.knownOwnReplyLocked(work, event) {
			out = append(out, event)
		}
	}
	return out
}

func (s *Service) confirmTopicReceipt(work *admittedWork, result HandlerResult) error {
	if work.Topic == nil {
		return nil
	}
	turn, _, err := protocol.ParseTopicTurn(work.Message.Payload.Input)
	if err != nil {
		return err
	}
	receipt := result.TopicReceipt
	if result.Accepted || receipt == nil || receipt.NativeSessionID == "" || receipt.UserEntryID == "" || receipt.InputDigest == "" || receipt.LastEntryID == "" || receipt.ThroughRevision != turn.TurnRevision {
		return errors.New("topic.unverified_native_receipt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.topicEpochLocked(work.Message) != work.Topic.Epoch {
		return errors.New("topic.session_changed")
	}
	s.mergeTopicReaderLocked(work)
	if receipt.RequiredReadThrough != 0 && (receipt.RequiredReadThrough != turn.Context.ThroughRevision || work.Topic.ReaderThrough < receipt.RequiredReadThrough) {
		return errors.New("context.required_unread")
	}
	basis := *work.Topic
	basis.Verified = true
	hash := sha256.New()
	for seq := uint64(1); seq <= work.Seq; seq++ {
		key := "run/" + work.Message.From + "/" + work.Message.ID + "/" + work.RunID + "/" + stringSeq(seq)
		if pub := s.journal.publications[s.journal.keys[key]]; pub != nil && pub.Message.Type == "run.output.delta" {
			_, _ = hash.Write([]byte(pub.Message.Payload.Text))
		}
	}
	basis.ReplyDigest = hex.EncodeToString(hash.Sum(nil))
	work.Topic = &basis
	copyReceipt := *receipt
	state := &topicNativeState{Epoch: basis.Epoch, Scope: topicScope(work.Message), Through: receipt.ThroughRevision, LastRequest: work.Message.ID, LastRun: work.RunID, Receipt: &copyReceipt}
	pub, err := s.topicReceiptLocked(work.Message, basis.Epoch, turn.TurnRevision, "")
	if err != nil {
		return err
	}
	return s.journal.append(journalRecord{Work: work, TopicState: state, Publication: &pub})
}

func (s *Service) pauseTopicLocked(work admittedWork) *topicNativeState {
	if work.Topic == nil {
		return nil
	}
	state := topicNativeState{Epoch: work.Topic.Epoch, Scope: topicScope(work.Message)}
	if previous := s.journal.topicStates[state.Epoch]; previous != nil {
		state = *previous
	}
	state.Paused = true
	return &state
}

func (s *Service) mergeTopicReaderLocked(work *admittedWork) {
	if work == nil || work.Topic == nil || work.Topic.ReaderToken == "" {
		return
	}
	current := s.journal.work[work.Key]
	if current == nil || current.Topic == nil || current.Topic.ReaderToken != work.Topic.ReaderToken || current.Topic.ReaderThrough <= work.Topic.ReaderThrough {
		return
	}
	basis := *work.Topic
	basis.ReaderThrough = current.Topic.ReaderThrough
	work.Topic = &basis
}

// ReadTopicContext serves only the fixed, public execution basis. It never
// exposes native transcripts, arbitrary topics, or post-admission revisions.
func (s *Service) ReadTopicContext(from, requestID, runID, readerToken string, after uint64, limit int) (protocol.TopicContextPage, error) {
	return s.readTopicContext(from, requestID, runID, readerToken, after, limit, false)
}

// ReadTopicHistory exposes retained public sources within the same active
// capability and frozen cut, including facts already present in native history.
// Optional history inspection never satisfies mandatory new-context coverage.
func (s *Service) ReadTopicHistory(from, requestID, runID, readerToken string, after uint64, limit int) (protocol.TopicContextPage, error) {
	return s.readTopicContext(from, requestID, runID, readerToken, after, limit, true)
}

func (s *Service) readTopicContext(from, requestID, runID, readerToken string, after uint64, limit int, history bool) (protocol.TopicContextPage, error) {
	page := protocol.TopicContextPage{Schema: protocol.TopicPageSchema, Events: []protocol.TopicEvent{}}
	if !validToken(from) || !validToken(requestID) || !validToken(runID) || limit < 1 || limit > protocol.TopicReaderMaxEvents {
		return page, errors.New("topic.reader_invalid_range")
	}
	if !s.trusted(from) {
		return page, errors.New("topic.reader_denied")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.journal == nil || s.role != config.RoleAgent {
		return page, errors.New("topic.reader_denied")
	}
	work := s.journal.work[from+"/"+requestID]
	if work == nil || work.State != "admitted" || work.RunID != runID || work.Topic == nil || work.Topic.ReaderToken == "" || subtle.ConstantTimeCompare([]byte(work.Topic.ReaderToken), []byte(readerToken)) != 1 || work.Message.To != s.botID || work.Message.ExpiresAt <= time.Now().UnixMilli() || work.Topic.Epoch != s.topicEpochLocked(work.Message) {
		return page, errors.New("topic.reader_denied")
	}
	if state := s.journal.topicStates[work.Topic.Epoch]; state != nil && state.Paused {
		return page, errors.New("topic.reader_denied")
	}
	turn, _, err := protocol.ParseTopicTurn(work.Message.Payload.Input)
	if err != nil || after > turn.Context.ThroughRevision {
		return page, errors.New("topic.reader_invalid_range")
	}
	page.ThroughRevision = turn.Context.ThroughRevision
	page.NextRevision = after
	if !history {
		page.NextRevision = max(after, work.Topic.MaterializedThrough)
	}
	ledger := s.journal.topics[topicScope(work.Message)]
	if ledger == nil || ledger.head < page.ThroughRevision {
		return page, errors.New("topic.reader_gap")
	}
	bytes := 512
	for revision := page.NextRevision; revision < page.ThroughRevision; {
		revision++
		event := ledger.events[revision]
		if !history && (revision <= work.Topic.MaterializedThrough || s.knownOwnReplyLocked(*work, event)) {
			page.NextRevision = revision
			continue
		}
		data, _ := json.Marshal(event)
		if len(page.Events) == limit || bytes+len(data)+1 > protocol.TopicReaderMaxBytes {
			break
		}
		page.Events = append(page.Events, event)
		bytes += len(data) + 1
		page.NextRevision = revision
	}
	page.Complete = page.NextRevision == page.ThroughRevision
	if !page.Complete && len(page.Events) == 0 {
		return page, errors.New("topic.reader_event_too_large")
	}
	if !history && after <= work.Topic.ReaderThrough && page.NextRevision > work.Topic.ReaderThrough {
		basis := *work.Topic
		basis.ReaderThrough = page.NextRevision
		copy := *work
		copy.Topic = &basis
		s.journal.work[work.Key] = &copy
	}
	return page, nil
}

func topicFailureCode(err error) string {
	if err != nil && (strings.HasPrefix(err.Error(), "topic.") || err.Error() == "context.required_unread") {
		return err.Error()
	}
	return "topic.invalid_turn"
}
