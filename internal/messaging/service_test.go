package messaging

import (
	"context"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

type stubAgentAdapter struct {
	deliver func(ctx context.Context, req adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error)
}

func (s stubAgentAdapter) DeliverMessage(ctx context.Context, req adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
	return s.deliver(ctx, req)
}

func (s stubAgentAdapter) GetStatus(_ context.Context) (*adapter.AgentStatus, error) {
	return &adapter.AgentStatus{Healthy: true}, nil
}

func TestPublishRejectsUntrustedPeer(t *testing.T) {
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{NodeID: "node-beta", AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustNone})

	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "tofu", nil)
	if _, err := svc.Publish(PublishRequest{TargetNode: "node-beta", Message: "hello"}); err == nil {
		t.Fatal("expected publish to fail for untrusted peer")
	}
}

func TestAdapterMessageHandlerUsesAgentAdapter(t *testing.T) {
	handler := NewAdapterMessageHandler(stubAgentAdapter{
		deliver: func(_ context.Context, req adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
			if req.MessageID != "msg-123" {
				t.Fatalf("adapter messageId = %q, want msg-123", req.MessageID)
			}
			if req.AgentID != "agent-42" {
				t.Fatalf("adapter agentId = %q, want agent-42", req.AgentID)
			}
			if req.From != "node-alpha" {
				t.Fatalf("adapter from = %q, want node-alpha", req.From)
			}
			if req.Message != "hello" {
				t.Fatalf("adapter message = %q, want hello", req.Message)
			}
			if req.Metadata["source"] != "test" {
				t.Fatalf("adapter metadata source = %v, want test", req.Metadata["source"])
			}
			return &adapter.DeliverMessageResult{Success: true, Accepted: true, RunID: "run-123", Reply: "handled-by-adapter"}, nil
		},
	}, time.Second)

	result, err := handler.HandleMessage(IncomingMessage{
		MessageID: "msg-123",
		AgentID:   "agent-42",
		From:      "node-alpha",
		Message:   "hello",
		Metadata: map[string]any{
			"source": "test",
		},
	})
	if err != nil {
		t.Fatalf("HandleMessage failed: %v", err)
	}
	if result.Reply != "handled-by-adapter (runId=run-123)" {
		t.Fatalf("reply = %q, want handled-by-adapter with runId", result.Reply)
	}
	if result.RunID != "run-123" {
		t.Fatalf("runId = %q, want run-123", result.RunID)
	}
}

func TestAdapterMessageHandlerReturnsAcceptedWithRunID(t *testing.T) {
	handler := NewAdapterMessageHandler(stubAgentAdapter{
		deliver: func(_ context.Context, _ adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
			return &adapter.DeliverMessageResult{Success: true, Accepted: true, RunID: "run-456"}, nil
		},
	}, time.Second)

	result, err := handler.HandleMessage(IncomingMessage{From: "node-alpha", Message: "hello"})
	if err != nil {
		t.Fatalf("HandleMessage failed: %v", err)
	}
	if result.Reply != "accepted (runId=run-456)" {
		t.Fatalf("reply = %q, want accepted with runId", result.Reply)
	}
	if result.RunID != "run-456" {
		t.Fatalf("runId = %q, want run-456", result.RunID)
	}
}

func TestMaybeDeliverForwardsDeliverableTypes(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	delivered := make(chan string, 10)
	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open", nil)
	svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		delivered <- msg.Type + "|" + msg.Message
		return HandlerResult{Reply: "ok"}, nil
	}))

	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[reply] Task completed."})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[end] Closing conversation."})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "event.forward", Content: "ignored"})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "task.assign", Content: "do this"})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[request] Do something."})

	want := map[string]bool{
		"chat.message|[reply] Task completed.":     false,
		"chat.message|[end] Closing conversation.": false,
		"chat.message|[request] Do something.":     false,
		"task.assign|do this":                      false,
	}

	deadline := time.After(1 * time.Second)
	for i := 0; i < len(want); i++ {
		select {
		case msg := <-delivered:
			if _, ok := want[msg]; !ok {
				t.Fatalf("unexpected delivered message: %q", msg)
			}
			want[msg] = true
		case <-deadline:
			t.Fatal("expected message delivery")
		}
	}

	for msg, got := range want {
		if !got {
			t.Fatalf("message %q was not delivered", msg)
		}
	}
}

func TestMaybeDeliverForwardsAgentID(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	delivered := make(chan IncomingMessage, 1)
	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open", nil)
	svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		delivered <- msg
		return HandlerResult{Reply: "ok"}, nil
	}))

	svc.maybeDeliver(protocol.MessageEnvelope{
		Type:    "chat.message",
		AgentID: "agent-42",
		From:    "node-beta",
		Content: "hello",
	})

	select {
	case msg := <-delivered:
		if msg.AgentID != "agent-42" {
			t.Fatalf("AgentID = %q, want agent-42", msg.AgentID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected message delivery")
	}
}

func TestMaybeDeliverRoutesTransferAvailableToTransferHandler(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	received := make(chan protocol.MessageEnvelope, 1)
	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open", nil)
	svc.SetTransferHandler(func(env protocol.MessageEnvelope) {
		received <- env
	})

	// Should NOT be delivered to message handler
	messageDelivered := false
	svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		messageDelivered = true
		return HandlerResult{}, nil
	}))

	svc.maybeDeliver(protocol.MessageEnvelope{
		Type:    "transfer.available",
		From:    "node-beta",
		Content: `{"transferId":"tf-1","bucket":"clawsynapse-transfer-node-alpha"}`,
	})

	select {
	case env := <-received:
		if env.Type != "transfer.available" {
			t.Fatalf("Type = %q, want transfer.available", env.Type)
		}
		if env.From != "node-beta" {
			t.Fatalf("From = %q, want node-beta", env.From)
		}
	case <-time.After(time.Second):
		t.Fatal("transfer handler not called within timeout")
	}

	if messageDelivered {
		t.Fatal("transfer.available should not be delivered to message handler")
	}
}

func TestIsDeliverableType(t *testing.T) {
	defaultPrefixes := []string{"chat", "task"}
	cases := []struct {
		typ  string
		want bool
	}{
		{"chat.message", true},
		{"chat.stream", true},
		{"task.assign", true},
		{"task.result", true},
		{"event.forward", false},
		{"discovery.announce", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isDeliverableType(tc.typ, defaultPrefixes); got != tc.want {
			t.Fatalf("isDeliverableType(%q) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

func TestIsDeliverableTypeCustomPrefixes(t *testing.T) {
	custom := []string{"control", "task"}
	cases := []struct {
		typ  string
		want bool
	}{
		{"control.shutdown", true},
		{"task.assign", true},
		{"chat.message", false},
	}
	for _, tc := range cases {
		if got := isDeliverableType(tc.typ, custom); got != tc.want {
			t.Fatalf("isDeliverableType(%q, custom) = %v, want %v", tc.typ, got, tc.want)
		}
	}
}

func TestNewSessionKeyUsesUUIDv4Format(t *testing.T) {
	sessionKey := newSessionKey()
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(sessionKey) {
		t.Fatalf("sessionKey = %q, want UUID v4", sessionKey)
	}
}

func TestPeerPublicKeyResolvesSelfIdentity(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}
	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "tofu", nil)

	pub, err := svc.peerPublicKey("node-alpha")
	if err != nil {
		t.Fatalf("self public key resolution failed: %v", err)
	}
	if string(pub) != string(id.PublicKey) {
		t.Fatal("self public key mismatch")
	}
}
