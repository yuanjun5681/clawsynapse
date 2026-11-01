package messaging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

func durableIdentity(t *testing.T, peers *discovery.Registry) (string, *identity.Identity) {
	t.Helper()
	dir := t.TempDir()
	id, err := identity.Create(filepath.Join(dir, "identity.key"), filepath.Join(dir, "identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	bot := identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey))
	peers.Upsert(types.Peer{BotID: bot, AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted, Metadata: map[string]any{"publicKey": base64.RawURLEncoding.EncodeToString(id.PublicKey)}})
	return bot, id
}

func durableBot(t *testing.T, bus *natsbus.Client, peers *discovery.Registry, bot string, id *identity.Identity, path string) *Service {
	t.Helper()
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), peers, bus, bot, id, "explicit", nil)
	if err := svc.SetStatePath(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return svc
}

func awaitDurable(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("durable messaging condition not reached")
}

func TestDurableIdempotencyCursorAndConflictAcrossRestart(t *testing.T) {
	bus := startMessagingBus(t)
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	svc := durableBot(t, bus, peers, bot, id, path)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	request := PublishRequest{TargetBot: bot, Type: "event.forward", AgentID: "default", Payload: protocol.MessagePayload{Input: []byte(`{"b":2,"a":1}`)}, Metadata: map[string]any{"nested": map[string]any{"value": "original"}}, IdempotencyKey: "persisted-request"}
	first, err := svc.Publish(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Payload.Input = []byte(`{ "a": 1, "b": 2 }`)
	duplicate, err := svc.Publish(request)
	if err != nil || duplicate != first {
		t.Fatalf("canonical replay = %#v / %v, want %#v", duplicate, err, first)
	}
	for _, key := range []string{"second", "third"} {
		if _, err := svc.Publish(PublishRequest{TargetBot: bot, Type: "event.forward", Message: key, IdempotencyKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	awaitDurable(t, func() bool { return len(svc.MessagesAfter(0, 100).Items) == 3 })
	page := svc.MessagesAfter(0, 2)
	if len(page.Items) != 2 || page.Cursor != 2 || page.Items[0].Cursor != 1 {
		t.Fatalf("invalid page: %#v", page)
	}
	page.Items[0].Metadata["nested"].(map[string]any)["value"] = "mutated"
	svc.Close()

	restarted := durableBot(t, bus, peers, bot, id, path)
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	duplicate, err = restarted.Publish(request)
	if err != nil || duplicate != first {
		t.Fatalf("restart replay = %#v / %v", duplicate, err)
	}
	request.Payload.Input = []byte(`{"a":999,"b":2}`)
	if _, err := restarted.Publish(request); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting key accepted: %v", err)
	}
	last := restarted.MessagesAfter(page.Cursor, 2)
	if len(last.Items) != 1 || last.Cursor != 3 {
		t.Fatalf("cursor failed across restart: %#v", last)
	}
	original := restarted.MessagesAfter(0, 1).Items[0]
	if original.Metadata["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("API snapshot mutated durable metadata")
	}
	if empty := restarted.MessagesAfter(last.Cursor, 100); len(empty.Items) != 0 || empty.Cursor != last.Cursor {
		t.Fatalf("empty replay advanced cursor: %#v", empty)
	}
}

func TestOfflineInboxRecoversAdmittedCrashAsIndeterminateWithoutRerun(t *testing.T) {
	bus := startMessagingBus(t)
	peers := discovery.NewRegistry()
	senderBot, senderID := durableIdentity(t, peers)
	receiverBot, receiverID := durableIdentity(t, peers)
	sender := durableBot(t, bus, peers, senderBot, senderID, filepath.Join(t.TempDir(), "sender.jsonl"))
	if err := sender.Start(); err != nil {
		t.Fatal(err)
	}
	// No receiver process or consumer exists yet. Success must still be durable.
	published, err := sender.Publish(PublishRequest{TargetBot: receiverBot, Type: "task.request", AgentID: "agent-a", Message: "one external action", SessionKey: "chat-session", IdempotencyKey: "offline-command", Metadata: map[string]any{"dsynapseChat": map[string]any{"chatId": "chat", "messageId": "input"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receiver.jsonl")
	receiver := durableBot(t, bus, peers, receiverBot, receiverID, path)
	entered := make(chan struct{}, 1)
	var executions atomic.Int32
	receiver.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		executions.Add(1)
		if msg.RequestID != published.MessageID || msg.RunID == "" {
			t.Error("command lacks durable execution identity")
		}
		entered <- struct{}{}
		<-msg.Context.Done()
		return HandlerResult{}, context.Canceled
	}))
	if err := receiver.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("offline inbox was lost")
	}
	receiver.Close() // interruption after admission must not replay the action
	restarted := durableBot(t, bus, peers, receiverBot, receiverID, path)
	restarted.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) { executions.Add(1); return HandlerResult{}, nil }))
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	awaitDurable(t, func() bool {
		state, ok := sender.Request(published.MessageID)
		return ok && state.Status == "indeterminate"
	})
	state, _ := sender.Request(published.MessageID)
	if state.Publication != "published" || len(state.Events) < 2 || executions.Load() != 1 {
		t.Fatalf("unsafe crash recovery: %#v, executions=%d", state, executions.Load())
	}
	terminal := state.Events[len(state.Events)-1]
	if terminal.Type != "run.failed" || terminal.AgentID != "agent-a" || !terminal.Payload.Indeterminate || terminal.Metadata["dsynapseChat"].(map[string]any)["chatId"] != "chat" {
		t.Fatalf("lost terminal identity: %#v", terminal)
	}
	if got := restarted.MessagesAfter(0, 100); len(got.Items) != 1 || got.Items[0].ID != published.MessageID {
		t.Fatalf("duplicate inbox admission after restart: %#v", got)
	}
}

func TestDurableInboxWaitsForDiscoveryAndRechecksTrustBeforeExecution(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	remotePeers := discovery.NewRegistry()
	remote, remoteID := durableIdentity(t, remotePeers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	now := time.Now().UnixMilli()
	env := protocol.MessageEnvelope{ID: "signed-command", Type: "task.request", From: remote, To: bot, AgentID: "default", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	data, _ := protocol.SignedBytes(inboxSubject(bot), env)
	env.Sig = identity.Sign(remoteID.PrivateKey, data)
	encoded, _ := json.Marshal(env)
	if svc.handleInbox(inboxSubject(bot), encoded) {
		t.Fatal("undiscovered sender discarded instead of deferred")
	}
	peer, _ := remotePeers.Get(remote)
	peers.Upsert(peer)
	if !svc.handleInbox(inboxSubject(bot), encoded) {
		t.Fatal("trusted message not persisted")
	}
	peers.SetTrustStatus(remote, types.TrustRevoked)
	var executions atomic.Int32
	svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) { executions.Add(1); return HandlerResult{}, nil }))
	work := *svc.journal.work[messageKey(env)]
	if !svc.deliver(work) || executions.Load() != 0 {
		t.Fatal("revoked queued message executed")
	}
}

func TestJournalRecoversOnlyIncompleteFinalAppendAndLocksWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	journal, err := openMessageJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openMessageJournal(path); err == nil {
		t.Fatal("concurrent journal writer permitted")
	}
	env := protocol.MessageEnvelope{ID: "committed", From: "sender", To: "receiver"}
	if err := journal.append(journalRecord{Received: &StoredMessage{MessageEnvelope: env, Cursor: 1}, Work: &admittedWork{Key: messageKey(env), Message: env, State: "queued", RunID: "run"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.file.Write([]byte(`{"received":`)); err != nil {
		t.Fatal(err)
	}
	_ = journal.file.Close()
	journal, err = openMessageJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.file.Close()
	if len(journal.inbox) != 1 || journal.work[messageKey(env)].Message.ID != env.ID {
		t.Fatal("committed inbox/work lost during tail recovery")
	}
	if stat, err := os.Stat(path); err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("journal permissions: %v / %v", stat, err)
	}
}
