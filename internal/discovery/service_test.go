package discovery

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
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

	svc := NewService(slog.Default(), nil, r, nil, "node-alpha", "", 5*time.Second, 10*time.Second, "tofu")
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

func TestHandleAnnounceRestoresPersistedTrustStatusForNewPeer(t *testing.T) {
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: "node-beta", AtMs: 100, Reason: "approve for test"}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", 5*time.Second, 10*time.Second, "tofu")
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
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: "node-beta", AtMs: 100}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", 5*time.Second, 10*time.Second, "tofu")
	called := make(chan string, 1)
	svc.SetAutoAuthenticator(func(_ context.Context, nodeID string) error {
		called <- nodeID
		return nil
	})

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

	select {
	case nodeID := <-called:
		if nodeID != "node-beta" {
			t.Fatalf("expected auto auth for node-beta, got %s", nodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected auto auth to be triggered")
	}
}

func TestHandleAnnounceDeduplicatesAutoAuthentication(t *testing.T) {
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}
	if err := fs.SaveTrustState(store.TrustState{
		SchemaVersion: 1,
		Trusted:       []store.TrustPeerState{{NodeID: "node-beta", AtMs: 100}},
		Pending:       []store.TrustPendingState{},
		Rejected:      []store.TrustPeerState{},
		Revoked:       []store.TrustPeerState{},
	}); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, fs, "node-alpha", "", 5*time.Second, 10*time.Second, "tofu")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	svc.SetAutoAuthenticator(func(_ context.Context, nodeID string) error {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil
	})

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
