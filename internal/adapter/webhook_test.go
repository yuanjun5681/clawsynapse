package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clawsynapse/internal/protocol"
)

func TestWebhookAdapterRequiresURL(t *testing.T) {
	_, err := NewWebhookAdapter(WebhookConfig{NodeID: "n1"})
	if err == nil {
		t.Fatal("expected error for empty url")
	}
}

func TestWebhookDeliverMessageSuccess(t *testing.T) {
	var received webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("expected application/json, got %s", ct)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer webhook-secret" {
			t.Fatalf("expected bearer token, got %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "node-1", URL: srv.URL, AuthToken: " webhook-secret "})
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{
		MessageID:  "msg-123",
		Type:       "chat",
		AgentID:    "agent-42",
		From:       "peer-a",
		SessionKey: "sess-1",
		Message:    "hello world",
		Metadata:   map[string]any{"key": "val"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if !result.Accepted {
		t.Fatal("expected accepted")
	}
	if result.Reply != `{"ok":true}` {
		t.Fatalf("unexpected reply: %s", result.Reply)
	}

	if received.NodeID != "node-1" {
		t.Fatalf("expected nodeId node-1, got %s", received.NodeID)
	}
	if received.MessageID != "msg-123" {
		t.Fatalf("expected messageId msg-123, got %s", received.MessageID)
	}
	if received.From != "peer-a" {
		t.Fatalf("expected from peer-a, got %s", received.From)
	}
	if received.Message != "hello world" {
		t.Fatalf("expected message 'hello world', got %s", received.Message)
	}
	if received.Type != "chat" {
		t.Fatalf("expected type chat, got %s", received.Type)
	}
	if received.AgentID != "agent-42" {
		t.Fatalf("expected agentId agent-42, got %s", received.AgentID)
	}
	if received.SessionKey != "sess-1" {
		t.Fatalf("expected sessionKey sess-1, got %s", received.SessionKey)
	}
}

func TestWebhookDeliverMessageRequestDecodesEscapedNewlines(t *testing.T) {
	var received webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Fatalf("expected no authorization header, got %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "node-1", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, err = a.DeliverMessage(context.Background(), DeliverMessageRequest{
		From:    "peer-a",
		Message: "line1\nline2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if received.Message != "line1\nline2" {
		t.Fatalf("expected decoded newline message, got %q", received.Message)
	}
}

func TestWebhookDeliverMessageJSONReplyDecodesEscapedNewlines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"reply":"line1\nline2"}`))
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "node-1", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{
		Message: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Reply != "line1\nline2" {
		t.Fatalf("reply = %q, want decoded multiline reply", result.Reply)
	}
}

func TestWebhookDeliverMessageServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "n1", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{
		Message: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for 500 response")
	}
}

func TestWebhookGetStatusHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer status-secret" {
			t.Fatalf("expected bearer token, got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "n1", URL: srv.URL, AuthToken: "status-secret"})
	if err != nil {
		t.Fatal(err)
	}

	status, err := a.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Healthy {
		t.Fatal("expected healthy")
	}
}

func TestWebhookGetStatusUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "n1", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	status, err := a.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Healthy {
		t.Fatal("expected unhealthy for 503")
	}
}

func TestWebhookGetStatusUnhealthyWhenUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "n1", URL: srv.URL, AuthToken: "wrong-secret"})
	if err != nil {
		t.Fatal(err)
	}

	status, err := a.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Healthy {
		t.Fatal("expected unhealthy for 401")
	}
}

func TestWebhookFeedbackPreservesStructuredPayloadAndCorrelation(t *testing.T) {
	received := make(chan webhookPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	a, _ := NewWebhookAdapter(WebhookConfig{URL: srv.URL})
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{
		Type: "run.failed", MessageID: "event", RequestID: "request", RunID: "run", Seq: 3,
		Payload: protocol.MessagePayload{Text: "partial", Code: "timeout", Stage: "execution", Indeterminate: true},
	})
	if err != nil || !result.Success || !result.Accepted || result.Completed {
		t.Fatalf("async webhook acknowledgment: %+v %v", result, err)
	}
	payload := <-received
	if payload.Message != "" || payload.RequestID != "request" || payload.RunID != "run" || payload.Seq != 3 || payload.Payload.Text != "partial" || payload.Payload.Code != "timeout" || !payload.Payload.Indeterminate {
		t.Fatalf("feedback lost its payload or correlation: %+v", payload)
	}
}

func TestWebhookDistinguishesCompletedAndAcceptedResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		completed  bool
	}{
		{"accepted", `{"accepted":true,"runId":"remote-run"}`, false},
		{"explicit async despite text", `{"accepted":true,"completed":false,"reply":"queued"}`, false},
		{"sync reply", `{"reply":"answer"}`, true},
		{"explicit empty completion", `{"completed":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
			defer srv.Close()
			a, _ := NewWebhookAdapter(WebhookConfig{URL: srv.URL})
			var emitted string
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Emit: func(event OutputEvent) error { emitted += event.Text; return nil }})
			if err != nil || result.Completed != tc.completed {
				t.Fatalf("wrong completion semantics: %+v %v", result, err)
			}
			if !tc.completed && emitted != "" {
				t.Fatal("async acceptance was published as a completed answer")
			}
			if tc.name == "sync reply" && (emitted != "answer" || result.Reply != "") {
				t.Fatalf("answer was lost or duplicated: %+v / %q", result, emitted)
			}
		})
	}
}

func TestWebhookRejectsOversizedReplyBeforeEmitting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("answer", 1000))
	}))
	defer srv.Close()
	a, err := NewWebhookAdapter(WebhookConfig{NodeID: "local", URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	emitted := false
	_, err = a.DeliverMessage(context.Background(), DeliverMessageRequest{Emit: func(OutputEvent) error { emitted = true; return nil }})
	var failure *ExecutionError
	if !errors.As(err, &failure) || failure.Code != "response_limit" || !failure.Indeterminate || emitted {
		t.Fatalf("oversized reply was not rejected safely: %v, emitted=%v", err, emitted)
	}
}
