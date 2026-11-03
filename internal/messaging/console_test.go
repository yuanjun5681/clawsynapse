package messaging

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

func signedConsoleDelivery(t *testing.T, svc *Service, id *identity.Identity, env protocol.MessageEnvelope) []byte {
	t.Helper()
	data, err := protocol.SignedBytes(inboxSubject(env.To), env)
	if err != nil {
		t.Fatal(err)
	}
	env.Sig = identity.Sign(id.PrivateKey, data)
	encoded, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.handleInbox(inboxSubject(env.To), encoded) {
		t.Fatal("Console did not persist delivery")
	}
	return encoded
}

func TestConsolePersistsChatAndFeedbackWithoutExecutionOrEchoAcrossRestart(t *testing.T) {
	peers := discovery.NewRegistry()
	sender, senderID := durableIdentity(t, peers)
	receiver, receiverID := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, nil, peers, receiver, receiverID, path)
	svc.SetRole(config.RoleConsole)
	for i, kind := range []string{"chat.message", "run.started", "run.output.delta", "run.completed"} {
		// Even expired passive traffic must not generate a synthetic Run.
		now := time.Now().Add(-2 * time.Minute).UnixMilli()
		env := protocol.MessageEnvelope{ID: strings.ReplaceAll(kind, ".", "-"), Type: kind, From: sender, To: receiver, AgentID: "default", SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion, Payload: protocol.MessagePayload{Text: "persisted content"}}
		if i > 0 {
			env.RequestID, env.RunID, env.Seq = "outbound-request", "remote-run", uint64(i)
			if kind == "run.output.delta" {
				env.Payload.Channel = "answer"
			}
		}
		encoded := signedConsoleDelivery(t, svc, senderID, env)
		work := svc.journal.work[messageKey(env)]
		if work == nil || work.RunID != "" || !svc.deliver(*work) || svc.journal.work[messageKey(env)].State != "done" {
			t.Fatalf("Console did not finish passive delivery %s", kind)
		}
		if !svc.handleInbox(inboxSubject(receiver), encoded) {
			t.Fatal("duplicate durable admission was not acknowledged")
		}
	}
	if len(svc.journal.publications) != 0 {
		t.Fatal("Console emitted an echo or fabricated execution event")
	}
	svc.Close()
	restored := durableBot(t, nil, peers, receiver, receiverID, path)
	restored.SetRole(config.RoleConsole)
	page := restored.MessagesAfter(0, 100)
	if len(page.Items) != 4 || page.Items[0].Type != "chat.message" || page.Items[0].Payload.Text != "persisted content" || page.Items[3].Type != "run.completed" {
		t.Fatalf("passive inbox or feedback lost across restart: %#v", page)
	}
	for _, work := range restored.journal.work {
		if work.State != "done" {
			t.Fatal("passive delivery would replay after restart")
		}
	}
}

func TestConsoleRejectionIsSingleDurableEventBeforeAdmissionAndRecoversOutbox(t *testing.T) {
	bus := startMessagingBus(t)
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported", true: "expired"}[expired], func(t *testing.T) {
			peers := discovery.NewRegistry()
			sender, senderID := durableIdentity(t, peers)
			receiver, id := durableIdentity(t, peers)
			path := filepath.Join(t.TempDir(), "messages.jsonl")
			svc := durableBot(t, nil, peers, receiver, id, path)
			svc.SetRole(config.RoleConsole)
			// A closed execution budget cannot prevent durable receipt or refusal.
			scheduler := adapter.NewScheduler(1)
			limiter := scheduler.ForBot(receiver, 1, 0)
			limiter.Close()
			svc.SetExecutionLimiter(limiter)
			svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) {
				t.Fatal("Console called an execution handler")
				return HandlerResult{}, nil
			}))
			now := time.Now().UnixMilli()
			if expired {
				now -= 120000
			}
			env := protocol.MessageEnvelope{ID: "execution-request", Type: "task.assign", From: sender, To: receiver, AgentID: "default", SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
			encoded := signedConsoleDelivery(t, svc, senderID, env)
			work := *svc.journal.work[messageKey(env)]
			if !svc.deliver(work) {
				t.Fatal("Console execution rejection was not durable")
			}
			wantCode := "console.execution_unsupported"
			if expired {
				wantCode = "execution.expired"
			}
			if len(svc.journal.publications) != 1 || svc.journal.work[work.Key].State != "done" {
				t.Fatal("Console did not atomically commit one terminal rejection")
			}
			var original protocol.MessageEnvelope
			var originalKey string
			for _, publication := range svc.journal.publications {
				original, originalKey = publication.Message, publication.Key
			}
			if original.Type != "run.failed" || original.Payload.Code != wantCode || original.Payload.Indeterminate || original.RequestID != env.ID || original.RunID == "" || original.Seq != 1 || original.SessionKey != "topic" {
				t.Fatalf("invalid refusal event: %#v", original)
			}
			if !svc.handleInbox(inboxSubject(receiver), encoded) || len(svc.journal.publications) != 1 {
				t.Fatal("duplicate generated another terminal event")
			}
			// Simulate a crash after local commit but before any broker publication.
			svc.Close()
			restored := durableBot(t, bus, peers, receiver, id, path)
			restored.SetRole(config.RoleConsole)
			if !restored.handleInbox(inboxSubject(receiver), encoded) || len(restored.journal.publications) != 1 {
				t.Fatal("restart forgot durable deduplication")
			}
			if err := restored.Start(); err != nil {
				t.Fatal(err)
			}
			awaitDurable(t, func() bool {
				restored.mu.Lock()
				defer restored.mu.Unlock()
				pub := restored.journal.publications[original.ID]
				return pub != nil && pub.Published && pub.Key == originalKey && pub.Message.Sig == original.Sig && len(restored.journal.publications) == 1
			})
		})
	}
}

func TestConsoleLocalFileNotificationDoesNotExecute(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	svc.SetRole(config.RoleConsole)
	svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) {
		t.Fatal("file completion bypassed Console dispatch")
		return HandlerResult{}, nil
	}))
	if err := svc.HandleLocalNotification(IncomingMessage{MessageID: "transfer.received:file", Type: "transfer.received"}); err != nil {
		t.Fatal(err)
	}
	if len(svc.journal.work) != 0 || len(svc.journal.publications) != 0 {
		t.Fatal("file notification created execution state")
	}
}

func TestConsoleRecoveryPreservesHistoricalAdmittedUncertainty(t *testing.T) {
	bus := startMessagingBus(t)
	peers := discovery.NewRegistry()
	sender, _ := durableIdentity(t, peers)
	receiver, id := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, nil, peers, receiver, id, path)
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "historical-request", Type: "task.assign", From: sender, To: receiver, SessionKey: "old-session", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	work := admittedWork{Key: messageKey(env), Message: env, State: "admitted", RunID: "historical-run", Seq: 1}
	if err := svc.journal.append(journalRecord{Received: &StoredMessage{MessageEnvelope: env, Cursor: 1}, Work: &work}); err != nil {
		t.Fatal(err)
	}
	svc.Close()
	restored := durableBot(t, bus, peers, receiver, id, path)
	restored.SetRole(config.RoleConsole)
	if err := restored.Start(); err != nil {
		t.Fatal(err)
	}
	restored.mu.Lock()
	defer restored.mu.Unlock()
	if len(restored.journal.publications) != 1 {
		t.Fatal("historical admission did not produce one terminal recovery event")
	}
	for _, pub := range restored.journal.publications {
		event := pub.Message
		if event.Type != "run.failed" || event.Payload.Code != "execution.interrupted" || !event.Payload.Indeterminate || event.RunID != "historical-run" || event.Seq != 2 {
			t.Fatalf("Console falsely rewrote an admitted action as an unsupported refusal: %#v", event)
		}
	}
}

func TestConsoleTransferNotificationUsesCommonFileReceiverWithoutExecution(t *testing.T) {
	peers := discovery.NewRegistry()
	sender, senderID := durableIdentity(t, peers)
	receiver, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, receiver, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	svc.SetRole(config.RoleConsole)
	received := 0
	svc.SetTransferHandler(func(protocol.MessageEnvelope) error {
		received++
		return nil
	})
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "file-available", Type: "transfer.available", From: sender, To: receiver, Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	signedConsoleDelivery(t, svc, senderID, env)
	work := *svc.journal.work[messageKey(env)]
	if work.RunID != "" || !svc.deliver(work) || received != 1 || len(svc.journal.publications) != 0 || svc.journal.work[work.Key].State != "done" {
		t.Fatal("Console file reception created execution or skipped the common receiver")
	}
}
