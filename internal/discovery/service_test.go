package discovery

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

func TestHandleAnnouncePreservesAuthAndTrustStatus(t *testing.T) {
	r := NewRegistry()
	r.Upsert(types.Peer{
		NodeID:      "node-beta",
		AuthStatus:  types.AuthAuthenticated,
		TrustStatus: types.TrustTrusted,
		LastSeenMs:  1,
	})

	svc := NewService(slog.Default(), nil, r, "node-alpha", "", 5*time.Second, 10*time.Second, "tofu")
	msg := protocol.DiscoveryAnnounce{
		MessageID:    "m1",
		MessageType:  "discovery.announce",
		NodeID:       "node-beta",
		Version:      "v0.1.1",
		AgentProduct: "clawsynapse",
		Capabilities: []string{"chat"},
		Inbox:        "clawsynapse.msg.node-beta.inbox",
		PublicKey:    "pub",
		Ts:           time.Now().UnixMilli(),
		TTLms:        30000,
	}

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	peer, ok := r.Get("node-beta")
	if !ok {
		t.Fatal("peer should still exist")
	}
	if peer.AuthStatus != types.AuthAuthenticated {
		t.Fatalf("auth status overwritten: %s", peer.AuthStatus)
	}
	if peer.TrustStatus != types.TrustTrusted {
		t.Fatalf("trust status overwritten: %s", peer.TrustStatus)
	}
	if peer.Version != "v0.1.1" {
		t.Fatalf("expected version update, got %s", peer.Version)
	}
}
