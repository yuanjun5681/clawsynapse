package messaging

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

func signLocalCommand(t *testing.T, svc *Service, id string, expires time.Time) protocol.MessageEnvelope {
	t.Helper()
	env := protocol.MessageEnvelope{ID: id, Type: "task.request", From: svc.botID, To: svc.botID, SessionKey: id, Ts: time.Now().UnixMilli(), ExpiresAt: expires.UnixMilli(), ProtocolVersion: protocol.WireVersion}
	data, err := protocol.SignedBytes(inboxSubject(svc.botID), env)
	if err != nil {
		t.Fatal(err)
	}
	env.Sig = identity.Sign(svc.identity.PrivateKey, data)
	return env
}

func persistLocalCommand(t *testing.T, svc *Service, env protocol.MessageEnvelope) bool {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return svc.handleInbox(inboxSubject(svc.botID), data)
}

func TestDurableExecutionAdmissionIsBoundedAndDuplicatesStillAck(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	scheduler := adapter.NewScheduler(1)
	defer scheduler.Close()
	svc.SetExecutionLimiter(scheduler.ForBot(bot, 1, 1))
	expires := time.Now().Add(time.Minute)
	first := signLocalCommand(t, svc, "first", expires)
	second := signLocalCommand(t, svc, "second", expires)
	third := signLocalCommand(t, svc, "third", expires)
	if !persistLocalCommand(t, svc, first) || !persistLocalCommand(t, svc, second) {
		t.Fatal("bounded capacity was not accepted")
	}
	if persistLocalCommand(t, svc, third) {
		t.Fatal("execution queue overflow was acknowledged")
	}
	if !persistLocalCommand(t, svc, first) {
		t.Fatal("duplicate could not be acknowledged at capacity")
	}
	if len(svc.journal.inbox) != 2 || svc.journal.pendingCommands != 2 {
		t.Fatal("overflow changed durable state")
	}
	if err := svc.setWork(*svc.journal.work[messageKey(first)], "done"); err != nil {
		t.Fatal(err)
	}
	if !persistLocalCommand(t, svc, third) || svc.journal.pendingCommands != 2 {
		t.Fatal("completed work did not free capacity")
	}
}

func TestExpiryWhileWaitingNeverBecomesAdmitted(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	scheduler := adapter.NewScheduler(1)
	defer scheduler.Close()
	_, release, err := scheduler.ForBot("other-bot", 1, 0).Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	svc.SetExecutionLimiter(scheduler.ForBot(bot, 1, 1))
	var executions atomic.Int32
	svc.SetMessageHandler(MessageHandlerFunc(func(IncomingMessage) (HandlerResult, error) { executions.Add(1); return HandlerResult{}, nil }))
	env := signLocalCommand(t, svc, "expires-in-queue", time.Now().Add(100*time.Millisecond))
	if !persistLocalCommand(t, svc, env) {
		t.Fatal("command not persisted")
	}
	if !svc.deliver(*svc.journal.work[messageKey(env)]) {
		t.Fatal("expired work did not settle")
	}
	if executions.Load() != 0 {
		t.Fatal("expired queued command executed")
	}
	work := svc.journal.work[messageKey(env)]
	if work.State != "done" || work.Seq != 1 || svc.journal.pendingCommands != 0 {
		t.Fatalf("expired work state = %+v", work)
	}
	for _, publication := range svc.journal.publications {
		if publication.Message.Type != "run.failed" || publication.Message.Payload.Code != "execution.expired" || publication.Message.Payload.Indeterminate {
			t.Fatalf("queued expiry became admitted/uncertain: %+v", publication.Message)
		}
	}
}

func TestInboxAndDirectHandlerShareOneAcquisition(t *testing.T) {
	peers := discovery.NewRegistry()
	bot, id := durableIdentity(t, peers)
	svc := durableBot(t, nil, peers, bot, id, filepath.Join(t.TempDir(), "messages.jsonl"))
	scheduler := adapter.NewScheduler(1)
	defer scheduler.Close()
	limiter := scheduler.ForBot(bot, 1, 0)
	svc.SetExecutionLimiter(limiter)
	svc.SetMessageHandler(NewAdapterMessageHandler(resultAdapter{&adapter.DeliverMessageResult{Success: true, Completed: true}}, time.Second, WithExecutionLimiter(limiter)))
	env := signLocalCommand(t, svc, "single-acquisition", time.Now().Add(time.Minute))
	if !persistLocalCommand(t, svc, env) || !svc.deliver(*svc.journal.work[messageKey(env)]) {
		t.Fatal("delivery failed")
	}
	for _, publication := range svc.journal.publications {
		if publication.Message.Type == "run.failed" {
			t.Fatalf("nested limiter rejected admitted execution: %+v", publication.Message)
		}
	}
	if svc.journal.work[messageKey(env)].State != "done" {
		t.Fatal("single execution not completed")
	}
}
