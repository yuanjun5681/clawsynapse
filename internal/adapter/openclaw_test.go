package adapter

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

type captureHandler struct {
	records *[]slog.Record
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	clone := slog.Record{
		Time:    r.Time,
		Level:   r.Level,
		Message: r.Message,
		PC:      r.PC,
	}
	r.Attrs(func(a slog.Attr) bool {
		clone.AddAttrs(a)
		return true
	})
	*h.records = append(*h.records, clone)
	return nil
}

func (h captureHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h captureHandler) WithGroup(_ string) slog.Handler {
	return h
}

func TestOpenClawAdapterDeliverMessage(t *testing.T) {
	adapter, err := NewOpenClawAdapter(OpenClawConfig{
		NodeID:  "node-alpha",
		AgentID: "main",
	})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, args ...string) ([]byte, error) {
		// args: agent --agent main --message <msg> --json --session-id <id>
		if len(args) < 8 || args[0] != "agent" {
			t.Fatalf("unexpected args: %v", args)
		}
		if args[2] != "main" {
			t.Fatalf("agent id = %q, want main", args[2])
		}
		wantMsg := "[clawsynapse from=node-beta to=node-alpha session=session-1]\nhello"
		if args[4] != wantMsg {
			t.Fatalf("message = %q, want %q", args[4], wantMsg)
		}
		if args[6] != "--session-id" || args[7] != "session-1" {
			t.Fatalf("session-id args = %v, want [--session-id session-1]", args[6:])
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

func TestFormatDeliverMessageWithSession(t *testing.T) {
	got := formatDeliverMessage("node-1", DeliverMessageRequest{
		From:       "node-2",
		Message:    "hello world",
		SessionKey: "task-abc",
	})
	want := "[clawsynapse from=node-2 to=node-1 session=task-abc]\nhello world"
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

func TestResolveSessionID(t *testing.T) {
	a, _ := NewOpenClawAdapter(OpenClawConfig{NodeID: "node-1", AgentID: "main"})

	// explicit sessionKey takes priority
	got := a.resolveSessionID(DeliverMessageRequest{SessionKey: "task-1", From: "node-2"})
	if got != "task-1" {
		t.Fatalf("got %q, want task-1", got)
	}

	// derive from from+nodeID when sessionKey is empty
	got = a.resolveSessionID(DeliverMessageRequest{From: "node-2"})
	if got != "cs-node-2-node-1" {
		t.Fatalf("got %q, want cs-node-2-node-1", got)
	}

	// anonymous when from is empty
	got = a.resolveSessionID(DeliverMessageRequest{})
	if got != "cs-_anon-node-1" {
		t.Fatalf("got %q, want cs-_anon-node-1", got)
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

func TestFormatOpenClawCommandForLogTruncatesMessage(t *testing.T) {
	message := strings.Repeat("x", 300)

	got := formatOpenClawCommandForLog([]string{
		"agent",
		"--agent", "main",
		"--message", message,
		"--json",
		"--session-id", "session-1",
	})

	if !strings.Contains(got, `openclaw "agent" "--agent" "main" "--message" "`) {
		t.Fatalf("command = %q, missing prefix", got)
	}
	if !strings.Contains(got, "truncated, 300 bytes total") {
		t.Fatalf("command = %q, missing truncation marker", got)
	}
	if !strings.Contains(got, `"--session-id" "session-1"`) {
		t.Fatalf("command = %q, missing session id", got)
	}
}

func TestOpenClawAdapterDeliverMessageLogsCommand(t *testing.T) {
	var records []slog.Record
	logger := slog.New(captureHandler{records: &records})

	adapter, err := NewOpenClawAdapter(OpenClawConfig{
		NodeID:  "node-alpha",
		AgentID: "main",
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("NewOpenClawAdapter failed: %v", err)
	}

	adapter.execCmd = func(_ context.Context, _ ...string) ([]byte, error) {
		return []byte(`{"runId":"run-1","status":"ok","result":{"payloads":[{"text":"done"}]}}`), nil
	}

	_, err = adapter.DeliverMessage(context.Background(), DeliverMessageRequest{
		SessionKey: "session-1",
		Message:    strings.Repeat("m", 320),
		From:       "node-beta",
	})
	if err != nil {
		t.Fatalf("DeliverMessage failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("log records = %d, want 1", len(records))
	}

	var command string
	records[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "command" {
			command = a.Value.String()
		}
		return true
	})
	if command == "" {
		t.Fatal("expected command attribute")
	}
	wantMarker := "truncated, " + strconv.Itoa(len(formatDeliverMessage("node-alpha", DeliverMessageRequest{
		SessionKey: "session-1",
		Message:    strings.Repeat("m", 320),
		From:       "node-beta",
	}))) + " bytes total"
	if !strings.Contains(command, wantMarker) {
		t.Fatalf("command = %q, missing truncation marker", command)
	}
	if !strings.Contains(command, `"--agent" "main"`) {
		t.Fatalf("command = %q, missing agent", command)
	}
}
