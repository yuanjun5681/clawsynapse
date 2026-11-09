package main

import (
	"context"
	"testing"

	"clawsynapse/pkg/types"
)

func TestPublishProgressPreservesExactCorrelation(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Code: "msg.published"}}
	_, err := runPublish(context.Background(), client, []string{"--type", "chat.progress", "--target", "sender", "--agent", "worker", "--message", "Reading the files.", "--session-key", "topic", "--request-id", "original", "--run-id", "active", "--idempotency-key", "phase-one"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := client.payload.(map[string]any)
	for key, want := range map[string]string{"type": "chat.progress", "targetBot": "sender", "agentId": "worker", "sessionKey": "topic", "requestId": "original", "runId": "active", "idempotencyKey": "phase-one"} {
		if body[key] != want {
			t.Fatalf("%s = %#v, want %q", key, body[key], want)
		}
	}
	if _, supplied := body["seq"]; supplied {
		t.Fatal("CLI assigned a progress sequence")
	}
}

func TestPublishProgressRejectsIncompleteContextWithoutPosting(t *testing.T) {
	for _, args := range [][]string{
		{"--type", "chat.progress", "--target", "sender", "--message", "Reading."},
		{"--target", "sender", "--message", "answer", "--request-id", "original", "--run-id", "run"},
	} {
		client := &stubAPIClient{}
		if _, err := runPublish(context.Background(), client, args); err == nil || client.method != "" {
			t.Fatalf("invalid correlation posted: %v", err)
		}
	}
}
