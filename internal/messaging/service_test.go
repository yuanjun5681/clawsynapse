package messaging

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

type resultAdapter struct{ result *adapter.DeliverMessageResult }

func (a resultAdapter) DeliverMessage(context.Context, adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
	return a.result, nil
}
func (a resultAdapter) GetStatus(context.Context) (*adapter.AgentStatus, error) {
	return &adapter.AgentStatus{Healthy: true}, nil
}

func TestHandlerDistinguishesAcceptanceFromCompletion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   *adapter.DeliverMessageResult
		accepted bool
		failure  bool
	}{
		{"asynchronous", &adapter.DeliverMessageResult{Success: true, Accepted: true, RunID: "external-run"}, true, false},
		{"completed", &adapter.DeliverMessageResult{Success: true, Completed: true}, false, false},
		{"missing-state", &adapter.DeliverMessageResult{Success: true}, false, true},
		{"rejected", &adapter.DeliverMessageResult{Success: false, Accepted: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewAdapterMessageHandler(resultAdapter{tc.result}, time.Second)
			got, err := h.HandleMessage(IncomingMessage{Type: "task.request"})
			if (err != nil) != tc.failure {
				t.Fatalf("error = %v", err)
			}
			if !tc.failure && got.Accepted != tc.accepted {
				t.Fatalf("accepted=%v", got.Accepted)
			}
		})
	}
}

func startMessagingBus(t *testing.T) *natsbus.Client {
	t.Helper()
	binary := os.Getenv("NATS_SERVER_BIN")
	if binary == "" {
		binary, _ = exec.LookPath("nats-server")
	}
	if binary == "" {
		t.Skip("nats-server unavailable; set NATS_SERVER_BIN to exercise broker integration")
	}
	cmd := exec.Command(binary, "-a", "127.0.0.1", "-p", "-1", "-js", "-sd", t.TempDir())
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		pattern := regexp.MustCompile(`Listening for client connections on (127\.0\.0\.1:[0-9]+)`)
		for scanner.Scan() {
			if match := pattern.FindStringSubmatch(scanner.Text()); len(match) > 1 {
				select {
				case ready <- "nats://" + match[1]:
				default:
				}
			}
		}
	}()
	var address string
	select {
	case address = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("NATS did not become ready")
	}
	bus, err := natsbus.Connect(context.Background(), []string{address}, "messaging-regression")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	return bus
}

func TestBrokerStreamingRejectsReplayAndRouteTampering(t *testing.T) {
	bus := startMessagingBus(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	peers := discovery.NewRegistry()
	ids := make([]*identity.Identity, 2)
	nodes := make([]string, 2)
	services := make([]*Service, 2)
	for i := range ids {
		dir := t.TempDir()
		id, err := identity.LoadOrCreate(dir+"/identity.key", dir+"/identity.pub")
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
		nodes[i] = identity.DeriveNodeID(identity.DeriveNodeDID(id.PublicKey))
		peers.Upsert(types.Peer{NodeID: nodes[i], AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted, Metadata: map[string]any{"publicKey": base64.RawURLEncoding.EncodeToString(id.PublicKey)}})
		services[i] = NewService(log, peers, bus, nodes[i], id, "explicit", nil)
		if err := services[i].SetStatePath(dir + "/messages.jsonl"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services[i].Close)
	}
	events := make(chan protocol.MessageEnvelope, 32)
	requests := make(chan protocol.MessageEnvelope, 8)
	_, err := bus.Subscribe(inboxSubject(nodes[0]), func(_ string, data []byte) {
		var env protocol.MessageEnvelope
		if protocol.DecodeStrict(data, &env) == nil {
			events <- env
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bus.Subscribe(inboxSubject(nodes[1]), func(_ string, data []byte) {
		var env protocol.MessageEnvelope
		if protocol.DecodeStrict(data, &env) == nil {
			requests <- env
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var executions atomic.Int32
	services[1].SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		executions.Add(1)
		if msg.Message == "barrier" {
			return HandlerResult{}, nil
		}
		if err := msg.Emit(adapter.OutputEvent{Text: "early output"}); err != nil {
			return HandlerResult{}, err
		}
		select {
		case <-release:
		case <-msg.Context.Done():
			return HandlerResult{}, msg.Context.Err()
		}
		return HandlerResult{}, &adapter.ExecutionError{Code: "timeout", Stage: "execution", Indeterminate: true, Err: context.DeadlineExceeded}
	}))
	for _, svc := range services {
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
	}
	if err := bus.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	result, err := services[0].Publish(PublishRequest{TargetNode: nodes[1], Type: "task.request", SessionKey: "same-session", Message: "analyze"})
	if err != nil {
		t.Fatal(err)
	}
	var request protocol.MessageEnvelope
	select {
	case request = <-requests:
	case <-time.After(3 * time.Second):
		t.Fatal("request not received")
	}
	next := func() protocol.MessageEnvelope {
		t.Helper()
		select {
		case event := <-events:
			return event
		case <-time.After(3 * time.Second):
			t.Fatal("execution event missing")
			return protocol.MessageEnvelope{}
		}
	}
	start := next()
	delta := next()
	if start.Type != "run.started" || delta.Type != "run.output.delta" || delta.Payload.Text != "early output" || delta.RequestID != result.MessageID || delta.RunID != start.RunID || delta.Seq != start.Seq+1 {
		t.Fatalf("invalid stream: %#v %#v", start, delta)
	}
	// The product is still blocked: this delta proves delivery before completion.
	select {
	case event := <-events:
		t.Fatalf("premature terminal event: %#v", event)
	default:
	}
	if err := bus.PublishJSON(inboxSubject(nodes[1]), request); err != nil {
		t.Fatal(err)
	}
	tampered := request
	tampered.ID = "tampered"
	tampered.AgentID = "another-agent"
	if err := bus.PublishJSON(inboxSubject(nodes[1]), tampered); err != nil {
		t.Fatal(err)
	}
	expired := request
	expired.ID = "expired"
	expired.Ts = time.Now().Add(-time.Minute).UnixMilli()
	expired.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	input, err := protocol.SignedBytes(inboxSubject(nodes[1]), expired)
	if err != nil {
		t.Fatal(err)
	}
	expired.Sig = identity.Sign(ids[0].PrivateKey, input)
	if err := bus.PublishJSON(inboxSubject(nodes[1]), expired); err != nil {
		t.Fatal(err)
	}
	close(release)
	terminal := next()
	for terminal.RequestID == expired.ID {
		if terminal.Type != "run.failed" || terminal.Payload.Code != "execution.expired" {
			t.Fatalf("expired command admitted: %#v", terminal)
		}
		terminal = next()
	}
	if terminal.Type != "run.failed" || terminal.Payload.Code != "timeout" || !terminal.Payload.Indeterminate || terminal.RequestID != result.MessageID || terminal.Seq != delta.Seq+1 {
		t.Fatalf("partial failure misreported: %#v", terminal)
	}
	barrier, err := services[0].Publish(PublishRequest{TargetNode: nodes[1], Type: "task.request", SessionKey: "same-session", Message: "barrier"})
	if err != nil {
		t.Fatal(err)
	}
	for {
		event := next()
		if event.RequestID == expired.ID && event.Type == "run.failed" && event.Payload.Code == "execution.expired" {
			continue
		}
		if event.RequestID == barrier.MessageID && event.Type == "run.completed" {
			break
		}
		if event.RequestID != barrier.MessageID {
			t.Fatalf("replayed or tampered execution: %#v", event)
		}
	}
	if executions.Load() != 2 {
		t.Fatalf("unexpected executions: %d", executions.Load())
	}
	stored := services[1].RecentMessages(10)
	if len(stored) != 3 {
		data, _ := json.Marshal(stored)
		t.Fatalf("rejected messages stored: %s", data)
	}
}

func TestSharedChatContextNeverExecutesNativeAdapter(t *testing.T) {
	message := IncomingMessage{Type: "chat.context", Payload: protocol.MessagePayload{Text: "untrusted shared history"}}
	native := NewAdapterMessageHandler(nil, time.Second)
	if result, err := native.HandleMessage(message); err != nil || result != (HandlerResult{}) {
		t.Fatalf("context observation reached native execution: %#v / %v", result, err)
	}
	webhook := NewAdapterMessageHandler(resultAdapter{&adapter.DeliverMessageResult{Success: true, Accepted: true, Reply: "observation stored"}}, time.Second, WithFeedbackDelivery())
	if result, err := webhook.HandleMessage(message); err != nil || result.Reply != "observation stored" {
		t.Fatalf("context observation not delivered to webhook consumer: %#v / %v", result, err)
	}
}
