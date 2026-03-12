package messaging

import (
	"log/slog"
	"testing"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/pkg/types"
)

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
