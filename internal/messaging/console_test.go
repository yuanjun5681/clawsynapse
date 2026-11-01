package messaging

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

func TestConsolePersistsChatAndFeedbackWithoutExecutionOrEchoAcrossRestart(t *testing.T) {
	peers := discovery.NewRegistry()
	sender, senderID := durableIdentity(t, peers)
	receiver, receiverID := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, nil, peers, receiver, receiverID, path)
	svc.SetMessageHandler(NewAdapterMessageHandler(adapter.ConsoleAdapter{}, time.Second))
	for i, kind := range []string{"chat.message", "run.started", "run.output.delta", "run.completed"} {
		now := time.Now().UnixMilli()
		env := protocol.MessageEnvelope{ID: strings.ReplaceAll(kind, ".", "-"), Type: kind, From: sender, To: receiver, AgentID: "default", SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion, Payload: protocol.MessagePayload{Text: "persisted content"}}
		if i > 0 {
			env.RequestID, env.RunID, env.Seq = "outbound-request", "remote-run", uint64(i)
			if kind == "run.output.delta" {
				env.Payload.Channel = "answer"
			}
		}
		data, err := protocol.SignedBytes(inboxSubject(receiver), env)
		if err != nil {
			t.Fatal(err)
		}
		env.Sig = identity.Sign(senderID.PrivateKey, data)
		encoded, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if !svc.handleInbox(inboxSubject(receiver), encoded) {
			t.Fatalf("Console did not persist %s", kind)
		}
		work := svc.journal.work[messageKey(env)]
		if work == nil || !svc.deliver(*work) || svc.journal.work[messageKey(env)].State != "done" {
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

func TestConsoleExecutionRequestProducesExplicitFailureNotCompletion(t *testing.T) {
	peers := discovery.NewRegistry()
	sender, _ := durableIdentity(t, peers)
	receiver, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, receiver, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	svc.SetMessageHandler(NewAdapterMessageHandler(adapter.ConsoleAdapter{}, time.Second))
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "execution-request", Type: "task.assign", From: sender, To: receiver, AgentID: "default", SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	work := admittedWork{Key: messageKey(env), Message: env, State: "queued", RunID: "console-rejection"}
	if err := svc.journal.append(journalRecord{Received: &StoredMessage{MessageEnvelope: env, Cursor: 1}, Work: &work}); err != nil {
		t.Fatal(err)
	}
	if !svc.deliver(work) {
		t.Fatal("Console execution rejection was not durable")
	}
	failed := false
	for _, publication := range svc.journal.publications {
		event := publication.Message
		if event.Type == "run.completed" || event.Type == "run.accepted" || event.Type == "run.output.delta" {
			t.Fatal("Console falsely claimed execution")
		}
		if event.Type == "run.failed" {
			failed = event.Payload.Code == "console.execution_unsupported" && !event.Payload.Indeterminate && event.RequestID == env.ID && event.SessionKey == "topic"
		}
	}
	if !failed || svc.journal.work[work.Key].State != "done" {
		t.Fatal("Console rejection lacks explicit durable failure")
	}
}
