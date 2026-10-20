package discovery

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
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

	svc.handleAnnounce(announceSubject, b)

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

	svc.handleAnnounce(announceSubject, b)

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

	svc.handleAnnounce(announceSubject, b)

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

	svc.handleAnnounce(announceSubject, b)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("expected first auto auth to start")
	}

	msg.MessageID = "second-heartbeat"
	msg.Ts++
	signAnnounce(t, &msg, testIdentity(4))
	b = marshalDiscovery(t, msg)
	svc.handleAnnounce(announceSubject, b)
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

	svc.handleAnnounce(announceSubject, b)

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

	svc.handleAnnounce(announceSubject, b)

	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("peer should not be stored when nodeId mismatches did")
	}
}

func TestNewAnnounceUsesConfiguredAgentIdentity(t *testing.T) {
	id := testIdentity(7)
	did := identity.DeriveNodeDID(id.PublicKey)
	nodeID := identity.DeriveNodeID(did)
	svc := NewService(
		slog.Default(),
		nil,
		NewRegistry(),
		nil,
		nodeID,
		did,
		base64.RawURLEncoding.EncodeToString(id.PublicKey),
		5*time.Second,
		10*time.Second,
		"tofu",
		AnnounceConfig{
			Identity:     id,
			AgentProduct: "deepseek-harness",
			AgentVersion: "0.1.2-alpha.3",
			Capabilities: []string{"chat", "task", "transfer"},
		},
	)

	msg, err := svc.newAnnounce()
	if err != nil {
		t.Fatal(err)
	}
	receiver := NewService(slog.Default(), nil, NewRegistry(), nil, "receiver", "", "", time.Second, time.Minute, "tofu")
	receiver.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
	if _, ok := receiver.peers.Get(nodeID); !ok {
		t.Fatal("locally signed announce was rejected")
	}
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
}

func testAnnounce(t *testing.T, marker byte) protocol.DiscoveryAnnounce {
	t.Helper()

	id := testIdentity(marker)
	did := identity.DeriveNodeDID(id.PublicKey)
	nodeID := identity.DeriveNodeID(did)

	msg := protocol.DiscoveryAnnounce{
		ProtocolVersion: "v2",
		MessageID:       "m1",
		MessageType:     "discovery.announce",
		NodeID:          nodeID,
		DID:             did,
		Version:         "v0.1.1",
		AgentProduct:    "clawsynapse",
		Capabilities:    []string{"chat"},
		Inbox:           "clawsynapse.msg." + nodeID + ".inbox",
		PublicKey:       base64.RawURLEncoding.EncodeToString(id.PublicKey),
		Ts:              time.Now().UnixMilli(),
		TTLms:           30000,
	}
	signAnnounce(t, &msg, id)
	return msg
}

func testIdentity(marker byte) *identity.Identity {
	seed := make([]byte, ed25519.SeedSize)
	seed[0] = marker
	priv := ed25519.NewKeyFromSeed(seed)
	return &identity.Identity{PrivateKey: priv, PublicKey: priv.Public().(ed25519.PublicKey)}
}

func signAnnounce(t *testing.T, msg *protocol.DiscoveryAnnounce, id *identity.Identity) {
	t.Helper()
	signed, err := protocol.SignedBytes(announceSubject, *msg)
	if err != nil {
		t.Fatal(err)
	}
	msg.Signature = identity.Sign(id.PrivateKey, signed)
}

func signDepart(t *testing.T, msg *protocol.DiscoveryDepart, id *identity.Identity) {
	t.Helper()
	signed, err := protocol.SignedBytes(departSubject, *msg)
	if err != nil {
		t.Fatal(err)
	}
	msg.Signature = identity.Sign(id.PrivateKey, signed)
}

func marshalDiscovery(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStartRequiresSigningIdentity(t *testing.T) {
	svc := NewService(slog.Default(), nil, NewRegistry(), nil, "local", "", "", time.Second, time.Minute, "open")
	if err := svc.Start(context.Background()); err == nil {
		t.Fatal("discovery started without a signing identity")
	}
}

func TestAnnounceRejectsTamperingInEveryTrustMode(t *testing.T) {
	mutations := map[string]func(*protocol.DiscoveryAnnounce){
		"version":    func(m *protocol.DiscoveryAnnounce) { m.Version = "altered" },
		"product":    func(m *protocol.DiscoveryAnnounce) { m.AgentProduct = "altered" },
		"capability": func(m *protocol.DiscoveryAnnounce) { m.Capabilities[0] = "altered" },
		"inbox":      func(m *protocol.DiscoveryAnnounce) { m.Inbox = "attacker.inbox" },
		"timestamp":  func(m *protocol.DiscoveryAnnounce) { m.Ts-- },
		"ttl":        func(m *protocol.DiscoveryAnnounce) { m.TTLms++ },
		"metadata":   func(m *protocol.DiscoveryAnnounce) { m.Metadata = map[string]any{"role": "admin"} },
		"messageId":  func(m *protocol.DiscoveryAnnounce) { m.MessageID = "altered" },
		"signature":  func(m *protocol.DiscoveryAnnounce) { m.Signature = "invalid" },
		"unsigned":   func(m *protocol.DiscoveryAnnounce) { m.Signature = "" },
	}
	for _, mode := range []string{"tofu", "explicit", "open"} {
		for name, mutate := range mutations {
			t.Run(mode+"/"+name, func(t *testing.T) {
				msg := testAnnounce(t, 20)
				mutate(&msg)
				r := NewRegistry()
				svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, mode)
				svc.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
				if _, ok := r.Get(msg.NodeID); ok {
					t.Fatal("tampered or unsigned announcement installed a peer")
				}
			})
		}
	}
}

func TestAnnounceRejectsInvalidSignedClaims(t *testing.T) {
	cases := map[string]func(*protocol.DiscoveryAnnounce){
		"future":         func(m *protocol.DiscoveryAnnounce) { m.Ts = time.Now().Add(time.Minute).UnixMilli() },
		"expired":        func(m *protocol.DiscoveryAnnounce) { m.Ts = time.Now().Add(-time.Minute).UnixMilli() },
		"oversizeTTL":    func(m *protocol.DiscoveryAnnounce) { m.TTLms = (3 * time.Minute).Milliseconds() },
		"zeroTTL":        func(m *protocol.DiscoveryAnnounce) { m.TTLms = 0 },
		"oldVersion":     func(m *protocol.DiscoveryAnnounce) { m.ProtocolVersion = "v1" },
		"missingVersion": func(m *protocol.DiscoveryAnnounce) { m.ProtocolVersion = "" },
		"wrongType":      func(m *protocol.DiscoveryAnnounce) { m.MessageType = "discovery.depart" },
		"missingID":      func(m *protocol.DiscoveryAnnounce) { m.MessageID = "" },
		"wrongDID":       func(m *protocol.DiscoveryAnnounce) { m.DID = "did:key:wrong" },
		"wrongNode":      func(m *protocol.DiscoveryAnnounce) { m.NodeID = "wrong-node" },
		"wrongInbox":     func(m *protocol.DiscoveryAnnounce) { m.Inbox = "attacker.inbox" },
		"metadataSize":   func(m *protocol.DiscoveryAnnounce) { m.Metadata = map[string]any{"value": strings.Repeat("x", 9000)} },
		"metadataCount": func(m *protocol.DiscoveryAnnounce) {
			m.Metadata = map[string]any{}
			for i := range 65 {
				m.Metadata[strings.Repeat("x", i+1)] = true
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			msg := testAnnounce(t, 21)
			mutate(&msg)
			signAnnounce(t, &msg, testIdentity(21))
			r := NewRegistry()
			svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "tofu")
			svc.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
			if _, ok := r.Get(msg.NodeID); ok {
				t.Fatal("invalid signed claim installed a peer")
			}
		})
	}
}

func TestDiscoveryRequiresCorrectSubjectAndStrictJSON(t *testing.T) {
	msg := testAnnounce(t, 22)
	data := marshalDiscovery(t, msg)
	cases := []struct {
		name    string
		subject string
		data    []byte
	}{
		{"wrongSubject", departSubject, data},
		{"unknownField", announceSubject, []byte(strings.TrimSuffix(string(data), "}") + `,"authStatus":"authenticated"}`)},
		{"duplicateField", announceSubject, []byte(strings.TrimSuffix(string(data), "}") + `,"nodeId":"` + msg.NodeID + `"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "tofu")
			svc.handleAnnounce(tc.subject, tc.data)
			if _, ok := r.Get(msg.NodeID); ok {
				t.Fatal("invalid wire message installed a peer")
			}
		})
	}
}

func TestDiscoveryRejectsReplayAcrossDepartureAndRediscovery(t *testing.T) {
	id := testIdentity(23)
	msg := testAnnounce(t, 23)
	now := time.Now().UnixMilli()
	msg.Ts = now - 3000
	msg.Version = "old"
	signAnnounce(t, &msg, id)
	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "tofu")
	deliver := func(m protocol.DiscoveryAnnounce) { svc.handleAnnounce(announceSubject, marshalDiscovery(t, m)) }
	deliver(msg)
	newer := msg
	newer.MessageID = "newer"
	newer.Version = "newer"
	newer.Ts = now - 2000
	signAnnounce(t, &newer, id)
	deliver(newer)
	peer, ok := r.Get(msg.NodeID)
	if !ok || peer.Version != "newer" {
		t.Fatal("fresh signed heartbeat did not update the peer")
	}
	deliver(msg)
	deliver(newer)
	after, ok := r.Get(msg.NodeID)
	if !ok || after.Version != "newer" || after.LastSeenMs != peer.LastSeenMs {
		t.Fatal("replay changed peer state or refreshed liveness")
	}
	reusedID := newer
	reusedID.Ts = now - 1500
	reusedID.Version = "reused-id"
	signAnnounce(t, &reusedID, id)
	deliver(reusedID)
	after, _ = r.Get(msg.NodeID)
	if after.Version != "newer" {
		t.Fatal("reused message ID was accepted")
	}
	depart := protocol.DiscoveryDepart{ProtocolVersion: "v2", MessageID: "depart", MessageType: "discovery.depart", NodeID: msg.NodeID, Reason: "shutdown", Ts: now - 1000}
	signDepart(t, &depart, id)
	svc.handleDepart(departSubject, marshalDiscovery(t, depart))
	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("valid signed departure did not remove the peer")
	}
	deliver(newer)
	olderWithNewID := newer
	olderWithNewID.MessageID = "unseen-old-heartbeat"
	signAnnounce(t, &olderWithNewID, id)
	deliver(olderWithNewID)
	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("old announce resurrected a departed peer")
	}
	fresh := newer
	fresh.MessageID = "rediscovery"
	fresh.Ts = now
	fresh.Version = "rediscovered"
	signAnnounce(t, &fresh, id)
	deliver(fresh)
	if _, ok := r.Get(msg.NodeID); !ok {
		t.Fatal("fresh announce failed to rediscover peer")
	}
	svc.handleDepart(departSubject, marshalDiscovery(t, depart))
	depart.MessageID = "unseen-old-depart"
	signDepart(t, &depart, id)
	svc.handleDepart(departSubject, marshalDiscovery(t, depart))
	if _, ok := r.Get(msg.NodeID); !ok {
		t.Fatal("old departure removed the rediscovered peer")
	}
}

func TestForgedDepartCannotEvictKnownPeer(t *testing.T) {
	cases := []string{"otherKey", "unsigned", "tamperedReason", "tamperedTime", "wrongType", "oldVersion", "future", "expired", "wrongSubject", "unknownNode"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			msg := testAnnounce(t, 24)
			msg.Ts -= 2000
			signAnnounce(t, &msg, testIdentity(24))
			r := NewRegistry()
			svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "open")
			svc.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
			depart := protocol.DiscoveryDepart{ProtocolVersion: "v2", MessageID: "depart", MessageType: "discovery.depart", NodeID: msg.NodeID, Reason: "shutdown", Ts: time.Now().UnixMilli()}
			subject := departSubject
			switch name {
			case "wrongType":
				depart.MessageType = "discovery.announce"
			case "oldVersion":
				depart.ProtocolVersion = "v1"
			case "future":
				depart.Ts = time.Now().Add(time.Minute).UnixMilli()
			case "expired":
				depart.Ts = time.Now().Add(-3 * time.Minute).UnixMilli()
			case "wrongSubject":
				subject = announceSubject
			case "unknownNode":
				depart.NodeID = "unknown-node"
			}
			signDepart(t, &depart, testIdentity(24))
			switch name {
			case "otherKey":
				signDepart(t, &depart, testIdentity(25))
			case "unsigned":
				depart.Signature = ""
			case "tamperedReason":
				depart.Reason = "forged"
			case "tamperedTime":
				depart.Ts++
			}
			svc.handleDepart(subject, marshalDiscovery(t, depart))
			if _, ok := r.Get(msg.NodeID); !ok {
				t.Fatal("invalid departure evicted the legitimate peer")
			}
		})
	}
}

func TestAnnounceUsesLocalLivenessAndIgnoresRemoteSecurityMetadata(t *testing.T) {
	msg := testAnnounce(t, 26)
	msg.Ts = time.Now().Add(-5 * time.Second).UnixMilli()
	msg.Metadata = map[string]any{"authStatus": types.AuthAuthenticated, "trustStatus": types.TrustTrusted, "ttlMs": 999999999, "publicKey": "forged", "labels": []string{"admin"}}
	signAnnounce(t, &msg, testIdentity(26))
	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "open")
	before := time.Now().UnixMilli()
	svc.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
	peer, ok := r.Get(msg.NodeID)
	if !ok {
		t.Fatal("valid signed announcement rejected")
	}
	if peer.LastSeenMs < before || peer.LastSeenMs > time.Now().UnixMilli() {
		t.Fatal("peer liveness did not use local receive time")
	}
	if peer.AuthStatus != types.AuthSeen || peer.TrustStatus != types.TrustNone {
		t.Fatal("remote metadata changed authentication or trust")
	}
	if peer.Metadata["publicKey"] != msg.PublicKey || metadataInt64(peer.Metadata, "ttlMs") != msg.TTLms {
		t.Fatal("remote metadata overrode verified discovery attributes")
	}
	if _, ok := peer.Metadata["labels"]; ok {
		t.Fatal("remote labels were imported")
	}
	svc.gc()
	if _, ok := r.Get(msg.NodeID); !ok {
		t.Fatal("fresh local reception expired prematurely")
	}
	peer.LastSeenMs = time.Now().UnixMilli() - msg.TTLms - 1
	r.Upsert(peer)
	svc.gc()
	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("peer remained discoverable after its bounded TTL expired")
	}
}

func TestConcurrentDiscoveryCannotResurrectDepartedPeer(t *testing.T) {
	id := testIdentity(27)
	msg := testAnnounce(t, 27)
	msg.Ts = time.Now().Add(-2 * time.Second).UnixMilli()
	signAnnounce(t, &msg, id)
	r := NewRegistry()
	svc := NewService(slog.Default(), nil, r, nil, "local", "", "", time.Second, time.Minute, "tofu")
	svc.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
	heartbeats := make([][]byte, 64)
	for i := range heartbeats {
		heartbeat := msg
		heartbeat.MessageID = strings.Repeat("h", i+1)
		heartbeat.Ts += int64(i + 1)
		signAnnounce(t, &heartbeat, id)
		heartbeats[i] = marshalDiscovery(t, heartbeat)
	}
	depart := protocol.DiscoveryDepart{ProtocolVersion: "v2", MessageID: "depart", MessageType: "discovery.depart", NodeID: msg.NodeID, Ts: time.Now().UnixMilli()}
	signDepart(t, &depart, id)
	departData := marshalDiscovery(t, depart)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, data := range heartbeats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			svc.handleAnnounce(announceSubject, data)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		svc.handleDepart(departSubject, departData)
	}()
	close(start)
	wg.Wait()
	if _, ok := r.Get(msg.NodeID); ok {
		t.Fatal("out-of-order concurrent heartbeat resurrected a departed peer")
	}
}

func TestLocalAnnounceDoesNotAdvertiseUnboundedTTL(t *testing.T) {
	id := testIdentity(28)
	did := identity.DeriveNodeDID(id.PublicKey)
	nodeID := identity.DeriveNodeID(did)
	svc := NewService(slog.Default(), nil, NewRegistry(), nil, nodeID, did,
		base64.RawURLEncoding.EncodeToString(id.PublicKey), time.Second, time.Hour, "tofu", AnnounceConfig{Identity: id})
	msg, err := svc.newAnnounce()
	if err != nil {
		t.Fatal(err)
	}
	if msg.TTLms <= 0 || msg.TTLms > (2*time.Minute).Milliseconds() {
		t.Fatal("local announcement advertises an unbounded TTL")
	}
	receiver := NewService(slog.Default(), nil, NewRegistry(), nil, "receiver", "", "", time.Second, time.Minute, "tofu")
	receiver.handleAnnounce(announceSubject, marshalDiscovery(t, msg))
	if _, ok := receiver.peers.Get(nodeID); !ok {
		t.Fatal("bounded, locally signed announcement was rejected")
	}
}
