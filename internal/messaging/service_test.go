package messaging

import (
	"context"
	"log/slog"
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

func TestBuildReplyCopiesRequestContext(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	svc := NewService(slog.Default(), peers, nil, "node-beta", id, "open")
	req := protocol.MessageEnvelope{
		ID:         "msg-1",
		From:       "node-alpha",
		To:         "node-beta",
		Content:    "hello",
		SessionKey: "session-1",
		RequestID:  "req-1",
		ReplyTo:    "clawsynapse.msg.node-alpha.reply",
	}

	reply, err := svc.buildReply(req)
	if err != nil {
		t.Fatalf("buildReply failed: %v", err)
	}
	if reply.Type != "chat.reply" {
		t.Fatalf("reply type = %q, want chat.reply", reply.Type)
	}
	if reply.To != "node-alpha" {
		t.Fatalf("reply to = %q, want node-alpha", reply.To)
	}
	if reply.RequestID != "req-1" {
		t.Fatalf("reply requestId = %q, want req-1", reply.RequestID)
	}
	if reply.CorrelationID != "msg-1" {
		t.Fatalf("reply correlationId = %q, want msg-1", reply.CorrelationID)
	}
	if reply.Content != "ack: hello" {
		t.Fatalf("reply content = %q, want ack", reply.Content)
	}
	if reply.Sig == "" {
		t.Fatal("expected reply signature")
	}
}

func TestBuildReplyUsesRequestHandler(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	svc := NewService(slog.Default(), peers, nil, "node-beta", id, "open")
	svc.SetRequestHandler(RequestHandlerFunc(func(req IncomingRequest) (HandlerResult, error) {
		if req.From != "node-alpha" {
			t.Fatalf("handler from = %q, want node-alpha", req.From)
		}
		if req.Message != "hello" {
			t.Fatalf("handler message = %q, want hello", req.Message)
		}
		if req.Metadata["source"] != "test" {
			t.Fatalf("handler metadata source = %v, want test", req.Metadata["source"])
		}
		return HandlerResult{Reply: "handled", RunID: "run-xyz"}, nil
	}))

	reply, err := svc.buildReply(protocol.MessageEnvelope{
		ID:         "msg-1",
		From:       "node-alpha",
		To:         "node-beta",
		Content:    "hello",
		SessionKey: "session-1",
		RequestID:  "req-1",
		ReplyTo:    "clawsynapse.msg.node-alpha.reply",
		Metadata: map[string]any{
			"source": "test",
		},
	})
	if err != nil {
		t.Fatalf("buildReply failed: %v", err)
	}
	if reply.Content != "handled" {
		t.Fatalf("reply content = %q, want handled", reply.Content)
	}
	if reply.Metadata["runId"] != "run-xyz" {
		t.Fatalf("reply runId = %v, want run-xyz", reply.Metadata["runId"])
	}
}

func TestAdapterRequestHandlerUsesAgentAdapter(t *testing.T) {
	handler := NewAdapterRequestHandler(stubAgentAdapter{
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

	result, err := handler.HandleRequest(IncomingRequest{
		From:    "node-alpha",
		Message: "hello",
		Metadata: map[string]any{
			"source": "test",
		},
	})
	if err != nil {
		t.Fatalf("HandleRequest failed: %v", err)
	}
	if result.Reply != "handled-by-adapter (runId=run-123)" {
		t.Fatalf("reply = %q, want handled-by-adapter with runId", result.Reply)
	}
	if result.RunID != "run-123" {
		t.Fatalf("runId = %q, want run-123", result.RunID)
	}
}

func TestAdapterRequestHandlerReturnsAcceptedWithRunID(t *testing.T) {
	handler := NewAdapterRequestHandler(stubAgentAdapter{
		deliver: func(_ context.Context, _ adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
			return &adapter.DeliverMessageResult{Success: true, Accepted: true, RunID: "run-456"}, nil
		},
	}, time.Second)

	result, err := handler.HandleRequest(IncomingRequest{From: "node-alpha", Message: "hello"})
	if err != nil {
		t.Fatalf("HandleRequest failed: %v", err)
	}
	if result.Reply != "accepted (runId=run-456)" {
		t.Fatalf("reply = %q, want accepted with runId", result.Reply)
	}
	if result.RunID != "run-456" {
		t.Fatalf("runId = %q, want run-456", result.RunID)
	}
}

func TestDispatchReplyCompletesPendingRequest(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open")
	resultCh := make(chan RequestResult, 1)
	svc.pending["req-1"] = &pendingRequest{requestID: "req-1", resultCh: resultCh, createdAt: time.Now()}

	svc.dispatchReply(protocol.MessageEnvelope{
		From:          "node-beta",
		RequestID:     "req-1",
		CorrelationID: "msg-1",
		Content:       "done",
		Metadata: map[string]any{
			"runId": "run-789",
		},
	})

	select {
	case result := <-resultCh:
		if result.RequestID != "req-1" {
			t.Fatalf("result requestId = %q, want req-1", result.RequestID)
		}
		if result.MessageID != "msg-1" {
			t.Fatalf("result messageId = %q, want msg-1", result.MessageID)
		}
		if result.From != "node-beta" {
			t.Fatalf("result from = %q, want node-beta", result.From)
		}
		if result.Reply != "done" {
			t.Fatalf("result reply = %q, want done", result.Reply)
		}
		if result.RunID != "run-789" {
			t.Fatalf("result runId = %q, want run-789", result.RunID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for reply result")
	}

	if _, ok := svc.pending["req-1"]; ok {
		t.Fatal("expected pending request to be cleared")
	}
}

func TestRequestRejectsMissingMessage(t *testing.T) {
	peers := discovery.NewRegistry()
	base := t.TempDir()
	id, err := identity.LoadOrCreate(base+"/identity.key", base+"/identity.pub")
	if err != nil {
		t.Fatalf("identity init failed: %v", err)
	}

	svc := NewService(slog.Default(), peers, nil, "node-alpha", id, "open")
	if _, err := svc.Request(RequestRequest{TargetNode: "node-beta"}); err == nil {
		t.Fatal("expected request to fail for missing message")
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
	svc.SetRequestHandler(RequestHandlerFunc(func(req IncomingRequest) (HandlerResult, error) {
		delivered <- req.Message
		return HandlerResult{Reply: "ok"}, nil
	}))

	// [reply] messages should NOT be delivered
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[reply] Task completed."})
	// [end] messages should NOT be delivered
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[end] Closing conversation."})
	// chat.reply should NOT be delivered
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.reply", Content: "ack"})

	// regular chat.message should be delivered
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.message", Content: "[request] Do something."})
	// chat.request should also be delivered
	svc.maybeDeliver(protocol.MessageEnvelope{Type: "chat.request", Content: "translate this"})

	received := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case msg := <-delivered:
			received[msg] = true
		case <-time.After(1 * time.Second):
			t.Fatalf("expected 2 deliveries, got %d", len(received))
		}
	}

	if !received["[request] Do something."] {
		t.Fatal("expected [request] message to be delivered")
	}
	if !received["translate this"] {
		t.Fatal("expected chat.request message to be delivered")
	}

	// ensure no extra deliveries
	select {
	case msg := <-delivered:
		t.Fatalf("unexpected extra delivery: %q", msg)
	case <-time.After(100 * time.Millisecond):
		// ok — no extra deliveries
	}
}
