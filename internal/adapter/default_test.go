package adapter

import (
	"context"
	"strings"
	"testing"
)

func TestDefaultAdapterReplyUsesBotIdentity(t *testing.T) {
	a := NewDefaultAdapter("n1-example")
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "hello", From: "n1-peer"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Reply, "bot n1-example handled message from n1-peer ") {
		t.Fatalf("reply uses stale or incorrect identity terminology: %q", result.Reply)
	}
}
