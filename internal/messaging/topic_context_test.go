package messaging

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

type topicConsumer struct {
	binding atomic.Value
	handle  func(IncomingMessage) (HandlerResult, error)
}

func newTopicConsumer(handle func(IncomingMessage) (HandlerResult, error)) *topicConsumer {
	h := &topicConsumer{handle: handle}
	h.binding.Store("binding-a")
	return h
}

func (h *topicConsumer) TopicContextIdentity() string { return h.binding.Load().(string) }
func (h *topicConsumer) HandleMessage(msg IncomingMessage) (HandlerResult, error) {
	if h.handle != nil {
		return h.handle(msg)
	}
	return topicResult(msg, ""), nil
}

func topicResult(msg IncomingMessage, answer string) HandlerResult {
	return HandlerResult{Reply: answer, TopicReceipt: &adapter.TopicInputReceipt{NativeSessionID: "native-session", UserEntryID: msg.RequestID, InputDigest: "verified-native-input", LastEntryID: msg.RunID, ThroughRevision: msg.TopicContext.Turn.TurnRevision}}
}

func topicInput(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func topicSync(t *testing.T, sender *Service, target, session, turn, key string, base, through uint64, events []protocol.TopicEvent) PublishResult {
	t.Helper()
	result, err := sender.Publish(PublishRequest{TargetBot: target, Type: "chat.context", SessionKey: session, IdempotencyKey: key, Payload: protocol.MessagePayload{Input: topicInput(t, protocol.TopicSync{Schema: protocol.TopicSyncSchema, TurnID: turn, BaseRevision: base, ThroughRevision: through, Digest: protocol.TopicEventsDigest(events), Events: events})}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func topicCommand(t *testing.T, sender *Service, target, session, turnID, epoch, previous, text string, through uint64) PublishResult {
	t.Helper()
	turn := protocol.TopicTurn{Schema: protocol.TopicTurnSchema, TurnID: turnID, TurnRevision: through + 1, NativeSessionEpoch: epoch, PreviousRequestID: previous, Context: protocol.TopicContextBatch{BaseRevision: through, ThroughRevision: through, Digest: protocol.TopicEventsDigest(nil), Events: []protocol.TopicEvent{}}}
	result, err := sender.Publish(PublishRequest{TargetBot: target, SessionKey: session, IdempotencyKey: "command/" + session + "/" + turnID, Payload: protocol.MessagePayload{Text: text, Input: topicInput(t, turn)}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func awaitTopicReceipt(t *testing.T, sender *Service, messageID string) protocol.TopicReceipt {
	t.Helper()
	var receipt protocol.TopicReceipt
	awaitDurable(t, func() bool {
		for _, item := range sender.MessagesAfter(0, 1000).Items {
			if item.Type == "chat.context" && item.RequestID == messageID && protocol.TopicInputSchema(item.Payload.Input) == protocol.TopicReceiptSchema {
				if err := protocol.DecodeStrict(item.Payload.Input, &receipt); err != nil {
					t.Fatal(err)
				}
				return true
			}
		}
		return false
	})
	return receipt
}

func topicPair(t *testing.T, h MessageHandler) (*Service, *Service, string) {
	t.Helper()
	bus := startMessagingBus(t)
	peers := discovery.NewRegistry()
	source, sourceID := durableIdentity(t, peers)
	target, targetID := durableIdentity(t, peers)
	sender := durableBot(t, bus, peers, source, sourceID, filepath.Join(t.TempDir(), "source.jsonl"))
	receiver := durableBot(t, bus, peers, target, targetID, filepath.Join(t.TempDir(), "target.jsonl"))
	receiver.SetMessageHandler(h)
	if err := sender.Start(); err != nil {
		t.Fatal(err)
	}
	if err := receiver.Start(); err != nil {
		t.Fatal(err)
	}
	return sender, receiver, target
}

func TestTopicSyncIsNonExecutingConflictCheckedAndReceiptReplaysAfterRestart(t *testing.T) {
	bus := startMessagingBus(t)
	peers := discovery.NewRegistry()
	source, sourceID := durableIdentity(t, peers)
	target, targetID := durableIdentity(t, peers)
	sender := durableBot(t, bus, peers, source, sourceID, filepath.Join(t.TempDir(), "source.jsonl"))
	if err := sender.Start(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "target.jsonl")
	receiver := durableBot(t, nil, peers, target, targetID, path)
	var executions atomic.Int32
	h := newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) { executions.Add(1); return topicResult(msg, ""), nil })
	receiver.SetMessageHandler(h)
	events := []protocol.TopicEvent{{Revision: 1, Kind: "user.message", MessageID: "old-input", Author: source, Text: "public fact"}}
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "sync-before-crash", Type: "chat.context", From: source, To: target, SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion, Payload: protocol.MessagePayload{Input: topicInput(t, protocol.TopicSync{Schema: protocol.TopicSyncSchema, TurnID: "next", ThroughRevision: 1, Digest: protocol.TopicEventsDigest(events), Events: events})}}
	data, _ := protocol.SignedBytes(inboxSubject(target), env)
	env.Sig = identity.Sign(sourceID.PrivateKey, data)
	encoded, _ := json.Marshal(env)
	if !receiver.handleInbox(inboxSubject(target), encoded) || !receiver.handleInbox(inboxSubject(target), encoded) {
		t.Fatal("sync was not durably accepted")
	}
	receiver.Close() // receipt is still in the local outbox; no broker was configured
	restarted := durableBot(t, bus, peers, target, targetID, path)
	restarted.SetMessageHandler(h)
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	receipt := awaitTopicReceipt(t, sender, env.ID)
	if receipt.Error != "" || receipt.StoredThroughRevision != 1 || receipt.NativeSessionEpoch == "" || executions.Load() != 0 {
		t.Fatalf("invalid recovered sync receipt: %#v, executions=%d", receipt, executions.Load())
	}
	if got := restarted.MessagesAfter(0, 100); len(got.Items) != 1 {
		t.Fatalf("duplicate sync persisted %d times", len(got.Items))
	}
	changed := append([]protocol.TopicEvent(nil), events...)
	changed[0].Text = "rewritten public fact"
	conflict := topicSync(t, sender, target, "topic", "next", "conflict", 0, 1, changed)
	if got := awaitTopicReceipt(t, sender, conflict.MessageID); got.Error != "topic.revision_conflict" {
		t.Fatalf("conflicting revision accepted: %#v", got)
	}
	unknown, err := sender.Publish(PublishRequest{TargetBot: target, Type: "chat.context", SessionKey: "topic", Payload: protocol.MessagePayload{Input: []byte(`{"schema":"clawsynapse.topic-sync/999"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := awaitTopicReceipt(t, sender, unknown.MessageID); got.Error != "topic.unsupported_schema" {
		t.Fatalf("unknown schema accepted: %#v", got)
	}
	tooLarge := []protocol.TopicEvent{{Revision: 2, Kind: "user.message", MessageID: "oversized", Author: source, Text: strings.Repeat("x", protocol.TopicReaderMaxBytes)}}
	large := topicSync(t, sender, target, "topic", "next", "oversized", 1, 2, tooLarge)
	if got := awaitTopicReceipt(t, sender, large.MessageID); got.Error != "topic.event_exceeds_reader_limit" {
		t.Fatalf("unreadable required event accepted: %#v", got)
	}
	probe := topicSync(t, sender, target, "topic", "next", "nonzero-probe", 1, 1, []protocol.TopicEvent{})
	if got := awaitTopicReceipt(t, sender, probe.MessageID); got.Error != "" || got.StoredThroughRevision != 1 || got.NativeSessionEpoch != receipt.NativeSessionEpoch {
		t.Fatalf("nonzero durable cursor probe failed: %#v", got)
	}
	h.binding.Store("")
	unsupported := topicSync(t, sender, target, "topic", "next", "unsupported-probe", 1, 1, []protocol.TopicEvent{})
	if got := awaitTopicReceipt(t, sender, unsupported.MessageID); got.Error != "topic.unsupported_adapter" || got.NativeSessionEpoch != "" {
		t.Fatalf("unsupported binding claimed capability: %#v", got)
	}
}

func TestTopicGapSyncFrozenReaderAndOwnReplyRecognition(t *testing.T) {
	entered := make(chan IncomingMessage, 4)
	release := make(chan struct{})
	secondRelease := make(chan struct{})
	h := newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) {
		entered <- msg
		if msg.Message == "current instruction" {
			select {
			case <-release:
			case <-msg.Context.Done():
				return HandlerResult{}, msg.Context.Err()
			}
			result := topicResult(msg, "own answer")
			result.TopicReceipt.RequiredReadThrough = msg.TopicContext.Turn.Context.ThroughRevision
			return result, nil
		}
		select {
		case <-secondRelease:
		case <-msg.Context.Done():
			return HandlerResult{}, msg.Context.Err()
		}
		return topicResult(msg, "next answer"), nil
	})
	sender, receiver, target := topicPair(t, h)
	probe := topicSync(t, sender, target, "topic", "turn-3", "probe", 0, 0, []protocol.TopicEvent{})
	epoch := awaitTopicReceipt(t, sender, probe.MessageID).NativeSessionEpoch
	command := topicCommand(t, sender, target, "topic", "turn-3", epoch, "", "current instruction", 2)
	awaitDurable(t, func() bool {
		for _, item := range receiver.MessagesAfter(0, 100).Items {
			if item.ID == command.MessageID {
				return true
			}
		}
		return false
	})
	event2 := protocol.TopicEvent{Revision: 2, Kind: "user.message", MessageID: "old-2", Author: sender.botID, Text: "second fact"}
	chunk2 := topicSync(t, sender, target, "topic", "turn-3", "chunk2", 1, 2, []protocol.TopicEvent{event2})
	if got := awaitTopicReceipt(t, sender, chunk2.MessageID); got.StoredThroughRevision != 0 {
		t.Fatalf("gap acknowledged: %#v", got)
	}
	select {
	case <-entered:
		t.Fatal("gap command executed")
	default:
	}
	event1 := protocol.TopicEvent{Revision: 1, Kind: "user.message", MessageID: "old-1", Author: sender.botID, Text: "first fact"}
	chunk1 := topicSync(t, sender, target, "topic", "turn-3", "chunk1", 0, 1, []protocol.TopicEvent{event1})
	if got := awaitTopicReceipt(t, sender, chunk1.MessageID); got.StoredThroughRevision != 1 {
		t.Fatalf("receipt exceeded correlated chunk cut: %#v", got)
	}
	var active IncomingMessage
	select {
	case active = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("sync could not unblock same-shard command")
	}
	if active.Message != "current instruction" || active.TopicContext == nil || len(active.TopicContext.Events) != 2 || active.TopicContext.Events[0] != event1 || active.TopicContext.Events[1] != event2 {
		t.Fatalf("wrong frozen execution basis: %#v", active.TopicContext)
	}
	if _, err := receiver.ReadTopicContext(active.From, active.RequestID, active.RunID, "wrong-token", 0, 1); err == nil {
		t.Fatal("reader accepted a known request without its capability")
	}
	if _, err := receiver.ReadTopicContext(active.From, active.RequestID, "wrong-run", active.TopicContext.ReaderToken, 0, 1); err == nil {
		t.Fatal("reader accepted another run")
	}
	page1, err := receiver.ReadTopicContext(active.From, active.RequestID, active.RunID, active.TopicContext.ReaderToken, 0, 1)
	if err != nil || len(page1.Events) != 1 || page1.Events[0] != event1 || page1.Complete {
		t.Fatalf("bad first page: %#v / %v", page1, err)
	}
	future := protocol.TopicEvent{Revision: 5, Kind: "bot.reply.completed", MessageID: "old-1", BotID: "other-bot", RequestID: "old-request", RunID: "old-run", Text: "late other answer"}
	later := topicSync(t, sender, target, "topic", "next", "future-chunk", 4, 5, []protocol.TopicEvent{future})
	_ = awaitTopicReceipt(t, sender, later.MessageID)
	page2, err := receiver.ReadTopicContext(active.From, active.RequestID, active.RunID, active.TopicContext.ReaderToken, page1.NextRevision, 1)
	if err != nil || len(page2.Events) != 1 || page2.Events[0] != event2 || !page2.Complete || page2.ThroughRevision != 2 {
		t.Fatalf("reader changed old basis: %#v / %v", page2, err)
	}
	close(release)
	awaitDurable(t, func() bool { state, ok := sender.Request(command.MessageID); return ok && state.Status == "completed" })
	if _, err := receiver.ReadTopicContext(active.From, active.RequestID, active.RunID, active.TopicContext.ReaderToken, 0, 1); err == nil {
		t.Fatal("completed reader capability remained valid")
	}
	own := protocol.TopicEvent{Revision: 4, Kind: "bot.reply.completed", MessageID: "turn-3", BotID: target, RequestID: active.RequestID, RunID: active.RunID, Text: "own answer"}
	ownSync := topicSync(t, sender, target, "topic", "turn-6", "own-reply", 3, 4, []protocol.TopicEvent{own})
	_ = awaitTopicReceipt(t, sender, ownSync.MessageID)
	next := topicCommand(t, sender, target, "topic", "turn-6", epoch, command.MessageID, "next instruction", 5)
	var second IncomingMessage
	select {
	case second = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("next command did not execute")
	}
	if !second.TopicContext.RequireResume || second.TopicContext.MaterializedThrough != 3 || len(second.TopicContext.Events) != 1 || second.TopicContext.Events[0] != future {
		t.Fatalf("own native history was injected again: %#v", second.TopicContext)
	}
	required, err := receiver.ReadTopicContext(second.From, second.RequestID, second.RunID, second.TopicContext.ReaderToken, 0, 100)
	if err != nil || len(required.Events) != 1 || required.Events[0] != future || !required.Complete {
		t.Fatalf("required view repeated native-known facts: %#v / %v", required, err)
	}
	history, err := receiver.ReadTopicHistory(second.From, second.RequestID, second.RunID, second.TopicContext.ReaderToken, 0, 100)
	if err != nil || len(history.Events) != 5 || history.Events[0] != event1 || history.Events[1] != event2 || history.Events[2].MessageID != "turn-3" || history.Events[3] != own || history.Events[4] != future || !history.Complete || history.ThroughRevision != 5 {
		t.Fatalf("retained public history was unavailable: %#v / %v", history, err)
	}
	if _, err := receiver.ReadTopicHistory(second.From, second.RequestID, active.RunID, second.TopicContext.ReaderToken, 0, 100); err == nil {
		t.Fatal("history reader accepted another run")
	}
	if _, err := receiver.ReadTopicHistory(second.From, second.RequestID, second.RunID, active.TopicContext.ReaderToken, 0, 100); err == nil {
		t.Fatal("history reader accepted another run's capability")
	}
	receiver.peers.SetTrustStatus(second.From, types.TrustRevoked)
	if _, err := receiver.ReadTopicHistory(second.From, second.RequestID, second.RunID, second.TopicContext.ReaderToken, 0, 100); err == nil {
		t.Fatal("revoked peer retained history access")
	}
	receiver.peers.SetTrustStatus(second.From, types.TrustTrusted)
	close(secondRelease)
	awaitDurable(t, func() bool { state, ok := sender.Request(next.MessageID); return ok && state.Status == "completed" })
	duplicate, err := sender.Publish(PublishRequest{TargetBot: target, SessionKey: "topic", Payload: protocol.MessagePayload{Text: "next instruction", Input: topicInput(t, second.TopicContext.Turn)}})
	if err != nil {
		t.Fatal(err)
	}
	awaitDurable(t, func() bool { state, ok := sender.Request(duplicate.MessageID); return ok && state.Status == "failed" })
	select {
	case <-entered:
		t.Fatal("same turn executed twice under another envelope ID")
	default:
	}
}

func TestTopicRequiredUnreadPausesSessionAndBindingChangeRejectsOldEpoch(t *testing.T) {
	var calls atomic.Int32
	var reader atomic.Pointer[Service]
	h := newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) {
		calls.Add(1)
		if msg.Message == "unverified" {
			return HandlerResult{Reply: "completion is not persistence evidence"}, nil
		}
		if msg.Message == "needs reader" {
			// Jumping directly to the fixed cut does not prove earlier pages read.
			_, _ = reader.Load().ReadTopicContext(msg.From, msg.RequestID, msg.RunID, msg.TopicContext.ReaderToken, msg.TopicContext.Turn.Context.ThroughRevision, 1)
			result := topicResult(msg, "must not complete")
			_, _ = reader.Load().ReadTopicHistory(msg.From, msg.RequestID, msg.RunID, msg.TopicContext.ReaderToken, 0, 100)
			result.TopicReceipt.RequiredReadThrough = msg.TopicContext.Turn.Context.ThroughRevision
			return result, nil
		}
		return topicResult(msg, ""), nil
	})
	sender, receiver, target := topicPair(t, h)
	reader.Store(receiver)
	fact := protocol.TopicEvent{Revision: 1, Kind: "user.message", MessageID: "fact", Author: sender.botID, Text: "must be read"}
	sync := topicSync(t, sender, target, "paused", "turn-2", "read-sync", 0, 1, []protocol.TopicEvent{fact})
	epoch := awaitTopicReceipt(t, sender, sync.MessageID).NativeSessionEpoch
	first := topicCommand(t, sender, target, "paused", "turn-2", epoch, "", "needs reader", 1)
	awaitDurable(t, func() bool {
		state, ok := sender.Request(first.MessageID)
		return ok && state.Status == "indeterminate"
	})
	state, _ := sender.Request(first.MessageID)
	if terminal := state.Events[len(state.Events)-1]; terminal.Payload.Code != "context.required_unread" {
		t.Fatalf("unread context completed: %#v", terminal)
	}
	second := topicCommand(t, sender, target, "paused", "turn-3", epoch, first.MessageID, "must stay queued", 2)
	awaitDurable(t, func() bool {
		for _, item := range receiver.MessagesAfter(0, 100).Items {
			if item.ID == second.MessageID {
				return true
			}
		}
		return false
	})
	// A different source scope sharing the worker shard must remain runnable.
	other := topicSameShardSession(t, sender.botID, "paused")
	probe := topicSync(t, sender, target, other, "barrier", "barrier-probe", 0, 0, []protocol.TopicEvent{})
	otherEpoch := awaitTopicReceipt(t, sender, probe.MessageID).NativeSessionEpoch
	barrier := topicCommand(t, sender, target, other, "barrier", otherEpoch, "", "unrelated", 0)
	awaitDurable(t, func() bool { state, ok := sender.Request(barrier.MessageID); return ok && state.Status == "completed" })
	if calls.Load() != 2 {
		t.Fatalf("paused native context executed: calls=%d", calls.Load())
	}
	h.binding.Store("binding-b")
	stale := topicCommand(t, sender, target, other, "stale", otherEpoch, barrier.MessageID, "wrong binding", 1)
	awaitDurable(t, func() bool { state, ok := sender.Request(stale.MessageID); return ok && state.Status == "failed" })
	if calls.Load() != 2 {
		t.Fatal("old binding epoch executed")
	}
	unknown, err := sender.Publish(PublishRequest{TargetBot: target, SessionKey: "unknown", Payload: protocol.MessagePayload{Input: []byte(`{"schema":"clawsynapse.topic-turn/999"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	awaitDurable(t, func() bool { state, ok := sender.Request(unknown.MessageID); return ok && state.Status == "failed" })
	if calls.Load() != 2 {
		t.Fatal("unknown topic command reached the handler")
	}
	unverifiedProbe := topicSync(t, sender, target, "unverified", "input", "unverified-probe", 0, 0, []protocol.TopicEvent{})
	unverifiedEpoch := awaitTopicReceipt(t, sender, unverifiedProbe.MessageID).NativeSessionEpoch
	unverified := topicCommand(t, sender, target, "unverified", "input", unverifiedEpoch, "", "unverified", 0)
	awaitDurable(t, func() bool {
		state, ok := sender.Request(unverified.MessageID)
		return ok && state.Status == "indeterminate"
	})
	unverifiedState, _ := sender.Request(unverified.MessageID)
	if terminal := unverifiedState.Events[len(unverifiedState.Events)-1]; terminal.Payload.Code != "topic.unverified_native_receipt" {
		t.Fatalf("unverified native completion advanced context: %#v", terminal)
	}
}

func topicSameShardSession(t *testing.T, source, session string) string {
	t.Helper()
	for i := range 1000 {
		candidate := "other-" + stringSeq(uint64(i))
		if workShard(protocol.MessageEnvelope{From: source, SessionKey: candidate}) == workShard(protocol.MessageEnvelope{From: source, SessionKey: session}) {
			return candidate
		}
	}
	t.Fatal("failed to find another same-shard scope")
	return ""
}

func TestTopicInterruptedAdmissionSurvivesRestartAndSuspendsLaterWork(t *testing.T) {
	entered := make(chan struct{}, 1)
	h := newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) {
		entered <- struct{}{}
		<-msg.Context.Done()
		return HandlerResult{}, context.Canceled
	})
	sender, receiver, target := topicPair(t, h)
	probe := topicSync(t, sender, target, "interrupted", "first", "interrupt-probe", 0, 0, []protocol.TopicEvent{})
	epoch := awaitTopicReceipt(t, sender, probe.MessageID).NativeSessionEpoch
	first := topicCommand(t, sender, target, "interrupted", "first", epoch, "", "external action", 0)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("command did not enter adapter")
	}
	path := receiver.journal.file.Name()
	receiver.Close()
	restarted := durableBot(t, sender.bus, sender.peers, target, receiver.identity, path)
	var calls atomic.Int32
	restarted.SetMessageHandler(newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) { calls.Add(1); return topicResult(msg, ""), nil }))
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	awaitDurable(t, func() bool {
		state, ok := sender.Request(first.MessageID)
		return ok && state.Status == "indeterminate"
	})
	second := topicCommand(t, sender, target, "interrupted", "second", epoch, first.MessageID, "must not replay", 1)
	awaitDurable(t, func() bool {
		for _, item := range restarted.MessagesAfter(0, 100).Items {
			if item.ID == second.MessageID {
				return true
			}
		}
		return false
	})
	other := topicSameShardSession(t, sender.botID, "interrupted")
	barrierProbe := topicSync(t, sender, target, other, "barrier", "restart-barrier-probe", 0, 0, []protocol.TopicEvent{})
	barrierEpoch := awaitTopicReceipt(t, sender, barrierProbe.MessageID).NativeSessionEpoch
	barrier := topicCommand(t, sender, target, other, "barrier", barrierEpoch, "", "unrelated", 0)
	awaitDurable(t, func() bool { state, ok := sender.Request(barrier.MessageID); return ok && state.Status == "completed" })
	if calls.Load() != 1 {
		t.Fatal("interrupted admitted work or later native command re-executed")
	}
}

func TestTopicCertainFailureKeepsFrozenPredecessorWithoutReplayingInstruction(t *testing.T) {
	entered := make(chan IncomingMessage, 2)
	h := newTopicConsumer(func(msg IncomingMessage) (HandlerResult, error) {
		entered <- msg
		if msg.Message == "reject before execution" {
			return HandlerResult{}, &adapter.ExecutionError{Code: "context_capacity", Stage: "validation"}
		}
		return topicResult(msg, ""), nil
	})
	sender, _, target := topicPair(t, h)
	probe := topicSync(t, sender, target, "certain", "first", "certain-probe", 0, 0, []protocol.TopicEvent{})
	epoch := awaitTopicReceipt(t, sender, probe.MessageID).NativeSessionEpoch
	first := topicCommand(t, sender, target, "certain", "first", epoch, "", "reject before execution", 0)
	awaitDurable(t, func() bool { state, ok := sender.Request(first.MessageID); return ok && state.Status == "failed" })
	next := topicCommand(t, sender, target, "certain", "second", epoch, first.MessageID, "new instruction", 1)
	awaitDurable(t, func() bool { state, ok := sender.Request(next.MessageID); return ok && state.Status == "completed" })
	<-entered
	second := <-entered
	if second.Message != "new instruction" || second.TopicContext.RequireResume || second.TopicContext.NativePreviousRequestID != "" || second.TopicContext.Turn.PreviousRequestID != first.MessageID || len(second.TopicContext.Events) != 1 || second.TopicContext.Events[0].MessageID != "first" {
		t.Fatalf("certain failed instruction was replayed or frozen source rewritten: %#v", second.TopicContext)
	}
}
