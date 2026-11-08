package messaging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

func TestIdleStateTracksPendingWorkAndBecomesIdle(t *testing.T) {
	svc := NewService(slog.Default(), nil, nil, "bot-alpha", nil, "tofu", nil)
	if err := svc.SetStatePath(filepath.Join(t.TempDir(), "messages.jsonl")); err != nil {
		t.Fatalf("set state path: %v", err)
	}
	journal := svc.journal

	published := protocol.MessageEnvelope{ProtocolVersion: "v2", ID: "pub-1", From: "bot-alpha", To: "bot-beta", Type: "chat.send"}
	if err := journal.append(journalRecord{Publication: &publication{Message: published, Fingerprint: "fp-1"}}); err != nil {
		t.Fatalf("append publication: %v", err)
	}
	command := protocol.MessageEnvelope{ProtocolVersion: "v2", ID: "cmd-1", From: "bot-alpha", To: "bot-alpha", Type: "task.request", RequestID: "req-1"}
	if err := journal.append(journalRecord{
		Received: &StoredMessage{MessageEnvelope: command, Cursor: 1},
		Work:     &admittedWork{Key: messageKey(command), Message: command, State: "queued"},
	}); err != nil {
		t.Fatalf("append work: %v", err)
	}

	state := svc.IdleState()
	if state.PendingPublications != 1 || state.PendingCommands != 1 || state.ActiveWork != 1 || state.Inbox != 1 {
		t.Fatalf("state = %+v, want one pending publication, command, work item, and inbox entry", state)
	}
	if state.Idle() {
		t.Fatal("state with in-flight work reported idle")
	}

	if err := journal.append(journalRecord{Published: "pub-1"}); err != nil {
		t.Fatalf("append published: %v", err)
	}
	if err := journal.append(journalRecord{Work: &admittedWork{Key: messageKey(command), State: "done"}}); err != nil {
		t.Fatalf("append done: %v", err)
	}

	state = svc.IdleState()
	if state.PendingPublications != 0 || state.PendingCommands != 0 || state.ActiveWork != 0 {
		t.Fatalf("terminal state = %+v, want cleared work", state)
	}
	if state.Inbox != 1 {
		t.Fatalf("inbox = %d, want retained history", state.Inbox)
	}
	if !state.Idle() {
		t.Fatal("terminal state did not report idle; inbox must be diagnostic only")
	}
}

func TestIdleStateWithoutJournalIsIdle(t *testing.T) {
	svc := NewService(slog.Default(), nil, nil, "bot-alpha", nil, "tofu", nil)
	state := svc.IdleState()
	if !state.Idle() || state != (IdleState{}) {
		t.Fatalf("state = %+v, want zero and idle", state)
	}
}

func TestFreezeAdmissionsDefersSignedInboxUntilResume(t *testing.T) {
	peers := discovery.NewRegistry()
	sender, senderID := durableIdentity(t, peers)
	bot, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "frozen-command", Type: "task.request", From: sender, To: bot, AgentID: "default", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	input, err := protocol.SignedBytes(inboxSubject(bot), env)
	if err != nil {
		t.Fatal(err)
	}
	env.Sig = identity.Sign(senderID.PrivateKey, input)
	encoded, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	svc.FreezeAdmissions()
	if svc.handleInbox(inboxSubject(bot), encoded) {
		t.Fatal("frozen signed command acknowledged instead of deferred")
	}
	if state := svc.IdleState(); state != (IdleState{}) {
		t.Fatalf("frozen delivery changed idle state: %+v", state)
	}
	svc.ResumeAdmissions()
	if !svc.handleInbox(inboxSubject(bot), encoded) {
		t.Fatal("resumed signed command not admitted")
	}
	if state := svc.IdleState(); state.PendingCommands != 1 || state.ActiveWork != 1 || state.Inbox != 1 {
		t.Fatalf("resumed command accounting = %+v", state)
	}

	svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) {
		return HandlerResult{Reply: "completed while frozen"}, nil
	}))
	svc.FreezeAdmissions()
	svc.mu.Lock()
	work := *svc.journal.work[messageKey(env)]
	svc.mu.Unlock()
	if !svc.deliver(work) {
		t.Fatal("freeze prevented existing command completion")
	}
	if state := svc.IdleState(); state.ActiveWork != 0 || state.PendingCommands != 0 || state.PendingPublications != 3 || state.Inbox != 1 {
		t.Fatalf("existing command did not finish with durable events: %+v", state)
	}
	if svc.ctx.Err() != nil {
		t.Fatal("freeze canceled the runtime")
	}
}

func TestFreezeAdmissionsTracksAndExcludesLocalNotifications(t *testing.T) {
	// Local callback accounting must work even without a messaging journal.
	svc := NewService(slog.Default(), nil, nil, "bot-alpha", nil, "tofu", nil)
	t.Cleanup(svc.Close)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	callbackErr := errors.New("callback failed")
	var executions atomic.Int32
	svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		executions.Add(1)
		if msg.MessageID == "active" {
			close(entered)
			<-release
			return HandlerResult{}, callbackErr
		}
		return HandlerResult{}, nil
	}))
	go func() {
		done <- svc.HandleLocalNotification(IncomingMessage{MessageID: "active", Type: "transfer.received"})
	}()
	defer close(release)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("local notification did not enter callback")
	}
	svc.FreezeAdmissions()
	if state := svc.IdleState(); state.ActiveWork != 1 || state.Idle() {
		t.Fatalf("active local callback missing from idle state: %+v", state)
	}
	if err := svc.HandleLocalNotification(IncomingMessage{MessageID: "frozen", Type: "transfer.received"}); err == nil {
		t.Fatal("frozen local notification admitted")
	}
	if executions.Load() != 1 {
		t.Fatal("frozen local notification executed adapter")
	}
	// Let the admitted callback finish without resuming new admissions.
	release <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, callbackErr) {
			t.Fatalf("callback error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("freeze blocked an admitted callback")
	}
	if state := svc.IdleState(); state != (IdleState{}) {
		t.Fatalf("completed local callback retained work: %+v", state)
	}
	svc.ResumeAdmissions()
	if err := svc.HandleLocalNotification(IncomingMessage{MessageID: "resumed", Type: "transfer.received"}); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 2 || !svc.IdleState().Idle() {
		t.Fatal("local notifications did not resume cleanly")
	}
}

func TestFreezeAdmissionsRejectsExternalPublishWithoutJournalMutation(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, nil, peers, bot, id, path)
	req := PublishRequest{TargetBot: bot, Type: "task.request", Message: "external", IdempotencyKey: "external"}
	if _, err := svc.enqueue(req, nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	state := svc.IdleState()
	svc.FreezeAdmissions()
	for _, key := range []string{"external", "new-external"} {
		req.IdempotencyKey = key
		if _, err := svc.Publish(req); err == nil {
			t.Fatalf("frozen external publish %q accepted", key)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || svc.IdleState() != state {
		t.Fatal("frozen external publish changed durable messaging state")
	}
	svc.ResumeAdmissions()
	if _, err := svc.enqueue(req, nil); err != nil {
		t.Fatalf("external publish did not resume: %v", err)
	}
	if got := svc.IdleState(); got.PendingPublications != 2 {
		t.Fatalf("resumed external publish accounting = %+v", got)
	}
}
