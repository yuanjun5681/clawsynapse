package adapter

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOpenClawAdapterDeliverMessage(t *testing.T) {
	adapter, err := NewOpenClawAdapter(OpenClawConfig{
		NodeID:  "node-alpha",
		AgentID: "main",
	})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) < 4 || args[0] != "agent" {
			t.Fatalf("unexpected args: %v", args)
		}
		if args[2] != "main" {
			t.Fatalf("agent id = %q, want main", args[2])
		}
		wantMsg := "[clawsynapse from=node-beta to=node-alpha]\nhello"
		if args[4] != wantMsg {
			t.Fatalf("message = %q, want %q", args[4], wantMsg)
		}

		return []byte(`{
			"runId": "run-123",
			"status": "ok",
			"result": {
				"payloads": [{"text": "done"}],
				"meta": {"durationMs": 500}
			}
		}`), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := adapter.DeliverMessage(ctx, DeliverMessageRequest{
		SessionKey: "session-1",
		Message:    "hello",
		From:       "node-beta",
	})
	if err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if !result.Accepted {
		t.Fatal("expected accepted result")
	}
	if result.RunID != "run-123" {
		t.Fatalf("runId = %q, want run-123", result.RunID)
	}
	if result.Reply != "done" {
		t.Fatalf("reply = %q, want done", result.Reply)
	}
}

func TestOpenClawAdapterDeliverMessageError(t *testing.T) {
	adapter, err := NewOpenClawAdapter(OpenClawConfig{AgentID: "main"})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(`{"runId":"run-456","status":"error","error":"agent not found"}`), nil
	}

	result, err := adapter.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure")
	}
	if result.Error != "agent not found" {
		t.Fatalf("error = %q, want agent not found", result.Error)
	}
}

func TestOpenClawAdapterCommandFailure(t *testing.T) {
	adapter, err := NewOpenClawAdapter(OpenClawConfig{AgentID: "main"})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		return nil, errors.New("command not found")
	}

	_, err = adapter.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenClawAdapterValidatesConfig(t *testing.T) {
	_, err := NewOpenClawAdapter(OpenClawConfig{})
	if err == nil {
		t.Fatal("expected config validation error")
	}
	if err.Error() != "openclaw agent id is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenClawAdapterGetStatus(t *testing.T) {
	adapter, err := NewOpenClawAdapter(OpenClawConfig{AgentID: "main"})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected args: %v", args)
		}
		return []byte("OpenClaw 2026.3.12\n"), nil
	}

	status, err := adapter.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if !status.Healthy {
		t.Fatal("expected healthy")
	}
}

func TestFormatDeliverMessage(t *testing.T) {
	got := formatDeliverMessage("node-1", DeliverMessageRequest{
		From:    "node-2",
		Message: "hello world",
	})
	want := "[clawsynapse from=node-2 to=node-1]\nhello world"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatDeliverMessageNoFrom(t *testing.T) {
	got := formatDeliverMessage("node-1", DeliverMessageRequest{
		Message: "test",
	})
	want := "[clawsynapse to=node-1]\ntest"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseOpenClawResult(t *testing.T) {
	data := []byte(`{
		"runId": "run-789",
		"status": "ok",
		"result": {
			"payloads": [
				{"text": "first reply"},
				{"text": "second reply"}
			]
		}
	}`)

	result, err := parseOpenClawResult(data)
	if err != nil {
		t.Fatalf("parseOpenClawResult failed: %v", err)
	}
	if result.RunID != "run-789" {
		t.Fatalf("runId = %q, want run-789", result.RunID)
	}
	if result.Reply != "first reply" {
		t.Fatalf("reply = %q, want first reply", result.Reply)
	}
}
