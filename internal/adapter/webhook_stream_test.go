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
	"time"
)

func TestWebhookStreamsCompleteLongUnicodeAnswerBeforeTerminal(t *testing.T) {
	chunk := strings.Repeat("答案🙂", 2000)
	firstOutput := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
			t.Error("webhook did not negotiate NDJSON")
		}
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		encoder := json.NewEncoder(w)
		_ = encoder.Encode(webhookFrame{Type: "output", Text: chunk})
		w.(http.Flusher).Flush()
		<-release
		for range 40 {
			_ = encoder.Encode(webhookFrame{Type: "output", Text: chunk})
		}
		_ = encoder.Encode(webhookFrame{Type: "completed"})
	}))
	defer srv.Close()
	a, _ := NewWebhookAdapter(testWebhookConfig(t, WebhookConfig{URL: srv.URL}))
	var answer strings.Builder
	done := make(chan error, 1)
	go func() {
		result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{MessageID: "stable-request", Emit: func(event OutputEvent) error {
			answer.WriteString(event.Text)
			select {
			case firstOutput <- struct{}{}:
			default:
			}
			return nil
		}})
		if err == nil && (!result.Completed || result.Reply != "") {
			err = errors.New("stream was not completed without duplicate reply")
		}
		done <- err
	}()
	select {
	case <-firstOutput:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first frame was buffered until terminal")
	}
	select {
	case err := <-done:
		close(release)
		t.Fatalf("stream ended before terminal: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if answer.String() != strings.Repeat(chunk, 41) {
		t.Fatalf("long answer truncated: got %d bytes", answer.Len())
	}
}

func TestWebhookPartialFailureNeverBecomesCompletionOrSafeRetry(t *testing.T) {
	for _, tc := range []struct {
		name, tail, code string
		indeterminate    bool
	}{
		{"explicit failure", `{"type":"failed","error":"model stopped"}` + "\n", "webhook_failed", false},
		{"unknown effects", `{"type":"failed","error":"connection lost","indeterminate":true}` + "\n", "webhook_failed", true},
		{"missing terminal", "", "response_incomplete", true},
		{"malformed", "not-json\n", "response_schema", true},
		{"unknown frame", `{"type":"accepted"}` + "\n", "response_schema", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = io.WriteString(w, "{\"type\":\"output\",\"text\":\"partial\"}\n"+tc.tail)
			}))
			defer srv.Close()
			a, _ := NewWebhookAdapter(testWebhookConfig(t, WebhookConfig{URL: srv.URL}))
			var text string
			_, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Emit: func(event OutputEvent) error { text += event.Text; return nil }})
			var failure *ExecutionError
			if !errors.As(err, &failure) || failure.Code != tc.code || failure.Indeterminate != tc.indeterminate || text != "partial" {
				t.Fatalf("unsafe partial response: %q / %#v / %v", text, failure, err)
			}
			var retry interface{ Retryable() bool }
			if errors.As(err, &retry) {
				t.Fatal("post-admission failure is retryable")
			}
		})
	}
}

func TestWebhookBoundsFramesAndPropagatesOutputBackpressure(t *testing.T) {
	oversized, _ := json.Marshal(webhookFrame{Type: "output", Text: strings.Repeat("x", 65537)})
	_, err := consumeWebhookStream(strings.NewReader(string(oversized)+"\n"), func(OutputEvent) error { t.Fatal("oversized output emitted"); return nil }, false)
	var failure *ExecutionError
	if !errors.As(err, &failure) || failure.Code != "response_limit" || !failure.Indeterminate {
		t.Fatalf("unbounded output frame: %v", err)
	}
	blocked := errors.New("durable output journal unavailable")
	_, err = consumeWebhookStream(strings.NewReader("{\"type\":\"output\",\"text\":\"answer\"}\n{\"type\":\"completed\"}\n"), func(OutputEvent) error { return blocked }, false)
	if !errors.Is(err, blocked) || !errors.As(err, &failure) || !failure.Indeterminate {
		t.Fatalf("output backpressure ignored: %v", err)
	}
}
