package messaging

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

func progressFixture(t *testing.T) (*Service, admittedWork, PublishRequest, string) {
	t.Helper()
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	sender, _ := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, nil, peers, bot, id, path)
	now := time.Now().UnixMilli()
	original := protocol.MessageEnvelope{ID: "original", From: sender, To: bot, AgentID: "worker", Type: "task.assign", SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	work := admittedWork{Key: messageKey(original), Message: original, State: "admitted", RunID: "active-run", Seq: 1}
	if err := svc.journal.append(journalRecord{Received: &StoredMessage{MessageEnvelope: original, Cursor: 1}, Work: &work}); err != nil {
		t.Fatal(err)
	}
	req := PublishRequest{TargetBot: sender, AgentID: "worker", Type: "chat.progress", RequestID: original.ID, RunID: work.RunID, SessionKey: "topic", Message: "Reading the relevant files.", IdempotencyKey: "phase-one"}
	return svc, work, req, path
}

func TestProgressOrderingDedupAndTerminalAcrossReplay(t *testing.T) {
	svc, work, req, path := progressFixture(t)
	first, err := svc.enqueue(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := svc.enqueue(req, nil)
	if err != nil || duplicate.Message.ID != first.Message.ID || first.Message.Seq != 1 {
		t.Fatalf("progress replay = %#v / %v", duplicate, err)
	}
	if svc.journal.work[work.Key].Seq != 1 {
		t.Fatal("progress consumed run sequence")
	}
	conflict := req
	conflict.Message = "Different work."
	if _, err := svc.enqueue(conflict, nil); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry accepted: %v", err)
	}
	if err := svc.emit(&work, "run.output.delta", protocol.MessagePayload{Text: "answer", Channel: "answer"}, "admitted"); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey, req.Message = "phase-two", "Applying the agreed changes."
	second, err := svc.enqueue(req, nil)
	if err != nil || second.Message.Seq != 2 || second.Previous != first.Message.ID || work.Seq != 2 {
		t.Fatalf("independent sequence = %#v / %v", second, err)
	}
	if err := svc.flushPublication(second.Message.ID); err == nil || !strings.Contains(err.Error(), "previous") {
		t.Fatalf("outbox allowed progress reordering: %v", err)
	}
	if err := svc.emit(&work, "run.completed", protocol.MessagePayload{}, "done"); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey = "late"
	if _, err := svc.enqueue(req, nil); err == nil {
		t.Fatal("terminal work published new progress")
	}
	bot, id, peers := svc.botID, svc.identity, svc.peers
	svc.Close()
	restarted := durableBot(t, nil, peers, bot, id, path)
	req.IdempotencyKey = "phase-two"
	replay, err := restarted.enqueue(req, nil)
	if err != nil || replay.Message.ID != second.Message.ID || replay.Message.Seq != 2 || replay.Previous != first.Message.ID {
		t.Fatalf("restart lost progress identity/order: %#v / %v", replay, err)
	}
	req.IdempotencyKey = "late-after-restart"
	if _, err := restarted.enqueue(req, nil); err == nil {
		t.Fatal("restarted terminal work published progress")
	}
}

func TestProgressConcurrentSequenceIsAtomic(t *testing.T) {
	svc, _, req, _ := progressFixture(t)
	const count = 12
	results := make(chan publication, count)
	errors := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request := req
			request.IdempotencyKey = ""
			pub, err := svc.enqueue(request, nil)
			results <- pub
			errors <- err
		}()
	}
	workers.Wait()
	seen := make(map[uint64]bool)
	for range count {
		pub, err := <-results, <-errors
		if err != nil || pub.Message.Seq < 1 || pub.Message.Seq > count || seen[pub.Message.Seq] {
			t.Fatalf("non-atomic progress sequence: %#v / %v", pub, err)
		}
		seen[pub.Message.Seq] = true
	}
}

func TestProgressRequiresExactActiveCorrelation(t *testing.T) {
	for _, field := range []string{"target", "request", "run", "session", "agent", "seq", "queued", "terminal", "expired", "console", "payload"} {
		t.Run(field, func(t *testing.T) {
			svc, work, req, _ := progressFixture(t)
			switch field {
			case "target":
				req.TargetBot = "wrong"
			case "request":
				req.RequestID = "wrong"
			case "run":
				req.RunID = "wrong"
			case "session":
				req.SessionKey = "wrong"
			case "agent":
				req.AgentID = "wrong"
			case "seq":
				req.Seq = 9
			case "queued":
				work.State = "queued"
			case "terminal":
				work.State = "done"
			case "expired":
				work.Message.ExpiresAt = 1
			case "console":
				svc.SetRole(config.RoleConsole)
			case "payload":
				req.Message = "not\na sentence"
			}
			svc.journal.work[work.Key] = &work
			if _, err := svc.enqueue(req, nil); err == nil {
				t.Fatal("invalid progress was accepted")
			}
		})
	}
}

func TestProgressIsPassiveForEveryRoleAndReplay(t *testing.T) {
	for _, role := range []string{config.RoleAgent, config.RoleConsole} {
		t.Run(role, func(t *testing.T) {
			svc, _, _, path := progressFixture(t)
			svc.SetRole(role)
			_, signingID := durableIdentity(t, svc.peers)
			from := identity.DeriveBotID(identity.DeriveBotDID(signingID.PublicKey))
			now := time.Now().UnixMilli()
			env := protocol.MessageEnvelope{ID: "progress", Type: "chat.progress", From: from, To: svc.botID, RequestID: "request", RunID: "run", Seq: 1, SessionKey: "topic", Payload: protocol.MessagePayload{Text: "Reading the files."}, Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
			data, _ := protocol.SignedBytes(inboxSubject(svc.botID), env)
			env.Sig = identity.Sign(signingID.PrivateKey, data)
			encoded, _ := json.Marshal(env)
			before := len(svc.journal.inbox)
			if !svc.handleInbox(inboxSubject(svc.botID), encoded) || !svc.handleInbox(inboxSubject(svc.botID), encoded) || len(svc.journal.inbox) != before+1 {
				t.Fatal("progress inbox replay not deduplicated")
			}
			calls := 0
			svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) {
				calls++
				return HandlerResult{Reply: "must not echo"}, nil
			}))
			work := *svc.journal.work[messageKey(env)]
			if work.RunID != "" || !svc.deliver(work) || calls != 0 || len(svc.journal.publications) != 0 {
				t.Fatal("progress caused execution or feedback")
			}
			if err := svc.HandleLocalNotification(IncomingMessage{Type: "chat.progress"}); err != nil || calls != 0 {
				t.Fatal("local progress invoked handler")
			}
			if _, err := NewAdapterMessageHandler(nil, time.Second, WithFeedbackDelivery()).HandleMessage(IncomingMessage{Type: "chat.progress"}); err != nil {
				t.Fatal("progress reached webhook adapter")
			}
			bot, id, peers := svc.botID, svc.identity, svc.peers
			svc.Close()
			restarted := durableBot(t, nil, peers, bot, id, path)
			restarted.SetRole(role)
			if !restarted.handleInbox(inboxSubject(bot), encoded) || len(restarted.journal.inbox) != before+1 || restarted.journal.work[messageKey(env)].State != "done" {
				t.Fatal("restart replay lost passive progress state")
			}
		})
	}
}

func TestProgressRequestEventsRemainRunOnly(t *testing.T) {
	svc, _, req, _ := progressFixture(t)
	request, err := svc.enqueue(PublishRequest{TargetBot: req.TargetBot, Message: "question", SessionKey: "topic"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	progress := protocol.MessageEnvelope{ID: "feedback", From: req.TargetBot, To: svc.botID, Type: "chat.progress", RequestID: request.Message.ID, RunID: "run", Seq: 1, SessionKey: "topic", Payload: protocol.MessagePayload{Text: "Reading."}}
	if err := svc.journal.append(journalRecord{Received: &StoredMessage{MessageEnvelope: progress, Cursor: 2}}); err != nil {
		t.Fatal(err)
	}
	state, ok := svc.Request(request.Message.ID)
	if !ok || len(state.Events) != 0 || state.Status != "sending" {
		t.Fatalf("progress entered run events: %#v", state)
	}
}

func TestProgressHandlerContextUsesExactVisibleRequest(t *testing.T) {
	a := &captureAdapter{}
	handler := NewAdapterMessageHandler(a, time.Second, WithPublishContext("http://127.0.0.1:18082", "/private/bin/clawsynapse"))
	_, err := handler.HandleMessage(IncomingMessage{MessageID: "original", RequestID: "original", RunID: "active", Type: "task.assign", From: "sender", To: "local", AgentID: "worker", SessionKey: "visible-topic"})
	if err != nil {
		t.Fatal(err)
	}
	execution := a.request.ExecutionContext
	if execution == nil || execution.BotID != "local" || execution.TargetBot != "sender" || execution.RequestID != "original" || execution.RunID != "active" || execution.SessionKey != "visible-topic" || execution.AgentID != "worker" || execution.APIAddr != "http://127.0.0.1:18082" || a.request.SessionKey == execution.SessionKey {
		t.Fatalf("progress used scoped or guessed context: %#v", a.request)
	}
}

func TestProgressPayloadIsBoundedVisibleTextOnly(t *testing.T) {
	for _, payload := range []protocol.MessagePayload{
		{},
		{Text: "   "},
		{Text: strings.Repeat("x", 513)},
		{Text: "Reading\nfiles."},
		{Text: string([]byte{0xff})},
		{Text: "Reading.", Input: json.RawMessage(`{}`)},
		{Text: "Reading.", Channel: "answer"},
		{Text: "Reading.", Instruction: "execute me"},
	} {
		if err := validateProgressPayload(payload); err == nil {
			t.Fatalf("invalid progress payload accepted: %#v", payload)
		}
	}
}
