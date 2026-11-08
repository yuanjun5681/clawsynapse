package natsbus

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestMessagingDurableMatchesInboxDerivation(t *testing.T) {
	const botID = "bot-alpha"
	digest := sha256.Sum256([]byte(botID))
	want := "node_" + hex.EncodeToString(digest[:16])
	if got := messagingDurable(botID); got != want {
		t.Fatalf("messagingDurable(%q) = %q, want %q", botID, got, want)
	}
}

func TestDeleteMessagingConsumerRequiresJetStream(t *testing.T) {
	client := &Client{}
	if err := client.DeleteMessagingConsumer("bot-alpha"); err == nil {
		t.Fatal("expected an explicit error when JetStream is unavailable")
	}
}
