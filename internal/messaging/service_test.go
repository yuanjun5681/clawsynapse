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

	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "tofu")
	if _, err := svc.Publish(PublishRequest{TargetNode: "node-beta", Message: "hello"}); err == nil {
		t.Fatal("expected publish to fail for untrusted peer")
	}
}

func TestAdapterMessageHandlerUsesAgentAdapter(t *testing.T) {
	handler := NewAdapterMessageHandler(stubAgentAdapter{
		deliver: func(_ context.Context, req adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
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
		From:    "node-alpha",
		Message: "hello",
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

func TestMaybeDeliverSkipsReplyAndEnd(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	delivered := make(chan string, 10)
	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open")
	svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
		delivered <- msg.Message
		return HandlerResult{Reply: "ok"}, nil
	}))

	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[reply] Task completed."})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[end] Closing conversation."})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "event.forward", Content: "ignored"})
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[request] Do something."})

	select {
	case msg := <-delivered:
		if msg != "[request] Do something." {
			t.Fatalf("delivered message = %q, want request body", msg)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected message delivery")
	}

	select {
	case msg := <-delivered:
		t.Fatalf("unexpected extra delivery: %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestNewSessionKeyUsesUUIDv4Format(t *testing.T) {
	sessionKey := newSessionKey()
	pattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !pattern.MatchString(sessionKey) {
		t.Fatalf("sessionKey = %q, want UUID v4", sessionKey)
	}
}
