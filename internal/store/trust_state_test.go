package store

import "testing"

func TestTrustStateRoundTrip(t *testing.T) {
	fs := NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout failed: %v", err)
	}

	in := TrustState{
		SchemaVersion: 1,
		Trusted:       []TrustPeerState{{NodeID: "node-beta", AtMs: 100}},
		Pending:       []TrustPendingState{{RequestID: "req-1", From: "node-gamma", To: "node-alpha", Direction: "inbound", ReceivedAtMs: 200}},
		Rejected:      []TrustPeerState{},
		Revoked:       []TrustPeerState{},
	}

	if err := fs.SaveTrustState(in); err != nil {
		t.Fatalf("save trust state failed: %v", err)
	}

	out, err := fs.LoadTrustState()
	if err != nil {
		t.Fatalf("load trust state failed: %v", err)
	}

	if len(out.Trusted) != 1 || out.Trusted[0].NodeID != "node-beta" {
		t.Fatalf("unexpected trusted state: %+v", out.Trusted)
	}
	if len(out.Pending) != 1 || out.Pending[0].RequestID != "req-1" {
		t.Fatalf("unexpected pending state: %+v", out.Pending)
	}
}
