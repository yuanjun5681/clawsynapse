package discovery

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

func TestHandleAnnouncePreservesAuthAndTrustStatus(t *testing.T) {
	msg := testAnnounce(t, 1)
	r := NewRegistry()
	r.Upsert(types.Peer{
		NodeID:      msg.NodeID,
		AuthStatus:  types.AuthAuthenticated,
		TrustStatus: types.TrustTrusted,
		LastSeenMs:  1,
	})

	svc := NewService(slog.Default(), nil, r, nil, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	peer, ok := r.Get(msg.NodeID)
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

func TestHandleAnnounceRestoresPersistedTrustStatusForNewPeer(t *testing.T) {
	msg := testAnnounce(t, 2)
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: msg.NodeID, AtMs: 100, Reason: "approve for test"}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	peer, ok := r.Get(msg.NodeID)
	if !ok {
		t.Fatal("peer should exist")
	}
	if peer.AuthStatus != types.AuthSeen {
		t.Fatalf("expected auth seen, got %s", peer.AuthStatus)
	}
	if peer.TrustStatus != types.TrustTrusted {
		t.Fatalf("expected trust trusted, got %s", peer.TrustStatus)
	}
}

func TestHandleAnnounceAutoAuthenticatesTrustedPeer(t *testing.T) {
	msg := testAnnounce(t, 3)
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: msg.NodeID, AtMs: 100}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")
	called := make(chan string, 1)
	svc.SetAutoAuthenticator(func(_ context.Context, nodeID string) error {
		called <- nodeID
		return nil
	})

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	select {
	case nodeID := <-called:
		if nodeID != msg.NodeID {
			t.Fatalf("expected auto auth for %s, got %s", msg.NodeID, nodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected auto auth to be triggered")
	}
}

func TestHandleAnnounceDeduplicatesAutoAuthentication(t *testing.T) {
	msg := testAnnounce(t, 4)
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: msg.NodeID, AtMs: 100}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	svc.SetAutoAuthenticator(func(_ context.Context, nodeID string) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	})

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected first auto auth to start")
	}

	svc.handleAnnounce("", b)
	time.Sleep(50 * time.Millisecond)
	close(release)

	if got := calls.Load(); got != 1 {
		t.Fatalf("expected 1 auto auth call, got %d", got)
	}
}

func TestHandleAnnounceRejectsDIDMismatch(t *testing.T) {
	msg := testAnnounce(t, 5)
	msg.DID = "did:key:zinvalid"

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, nil, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("peer should not be stored when did mismatches public key")
	}
}

func TestHandleAnnounceRejectsNodeIDMismatch(t *testing.T) {
	msg := testAnnounce(t, 6)
	msg.NodeID = "n1-deadbeefdeadbeefdeadbeefdeadbeef"
	msg.Inbox = "clawsynapse.msg." + msg.NodeID + ".inbox"

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, nil, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")

	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal announce: %v", err)
	}

	svc.handleAnnounce("", b)

	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("peer should not be stored when nodeId mismatches did")
	}
}

func TestShouldLogLegacyAnnounceRateLimitsByPeer(t *testing.T) {
	svc := NewService(slog.Default(), nil, NewRegistry(), nil, "node-alpha", "", "", 5*time.Second, 10*time.Second, "tofu")
	now := time.Now()

	if !svc.shouldLogLegacyAnnounce("node-test-005", now) {
		t.Fatal("expected first legacy announce log to be emitted")
	}
	if svc.shouldLogLegacyAnnounce("node-test-005", now.Add(time.Minute)) {
		t.Fatal("expected repeated legacy announce log to be rate limited")
	}
	if !svc.shouldLogLegacyAnnounce("node-test-005", now.Add(legacyAnnounceLogWindow+time.Second)) {
		t.Fatal("expected legacy announce log after window expires")
	}
	if !svc.shouldLogLegacyAnnounce("node-test-006", now.Add(time.Minute)) {
		t.Fatal("expected rate limit to be tracked per peer")
	}
}

func TestNewAnnounceUsesConfiguredAgentIdentity(t *testing.T) {
	svc := NewService(
		slog.Default(),
		nil,
		NewRegistry(),
		nil,
		"node-alpha",
		"did:key:node-alpha",
		"public-key",
		5*time.Second,
		10*time.Second,
		"tofu",
		AnnounceConfig{
			AgentProduct: "deepseek-harness",
			AgentVersion: "0.1.2-alpha.3",
			Capabilities: []string{"chat", "task", "transfer"},
		},
	)

	msg := svc.newAnnounce()
	if msg.AgentProduct != "deepseek-harness" {
		t.Fatalf("agentProduct = %q, want deepseek-harness", msg.AgentProduct)
	}
	if msg.Version != "0.1.2-alpha.3" {
		t.Fatalf("version = %q, want 0.1.2-alpha.3", msg.Version)
	}
	if got := len(msg.Capabilities); got != 3 {
		t.Fatalf("capabilities length = %d, want 3", got)
	}
	if msg.Capabilities[2] != "transfer" {
		t.Fatalf("capabilities = %#v, want transfer capability", msg.Capabilities)
	}

	msg.Capabilities[0] = "mutated"
	second := svc.newAnnounce()
	if second.Capabilities[0] != "chat" {
		t.Fatalf("announce capabilities shared mutable storage: %#v", second.Capabilities)
	}
}

func testAnnounce(t *testing.T, marker byte) protocol.DiscoveryAnnounce {
	t.Helper()

	seed := make([]byte, ed25519.SeedSize)
	seed[0] = marker
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)

	did := identity.DeriveNodeDID(pub)
	nodeID := identity.DeriveNodeID(did)

	return protocol.DiscoveryAnnounce{
		MessageID:    "m1",
		MessageType:  "discovery.announce",
		NodeID:       nodeID,
		DID:          did,
		Version:      "v0.1.1",
		AgentProduct: "clawsynapse",
		Capabilities: []string{"chat"},
		Inbox:        "clawsynapse.msg." + nodeID + ".inbox",
		PublicKey:    base64.RawURLEncoding.EncodeToString(pub),
		Ts:           time.Now().UnixMilli(),
		TTLms:        30000,
	}
}
