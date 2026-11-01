package discovery

import (
	"testing"
	"time"

	"clawsynapse/pkg/types"
)

func testPeer(botID string) types.Peer {
	return types.Peer{
		BotID:        botID,
		AgentProduct: "clawsynapse",
		Version:      "v0.1.0",
		DisplayName:  "remote-name",
		Labels:       types.Labels{}.WithCaps([]string{"chat"}),
		Inbox:        "clawsynapse.msg." + botID + ".inbox",
		LastSeenMs:   time.Now().UnixMilli(),
	}
}

func TestRegistryGetAndListMergeLocalAttrs(t *testing.T) {
	r := NewRegistry()
	r.Upsert(testPeer("bot-a"))
	r.Upsert(testPeer("bot-b"))
	r.SetLocalAttrs("bot-a", types.PeerLocalAttrs{
		Alias:       "别名",
		Description: "备注",
		Labels:      types.Labels{"site": types.LabelString("hq")},
	})

	peer, ok := r.Get("bot-a")
	if !ok {
		t.Fatal("bot-a missing")
	}
	if peer.LocalAlias != "别名" || peer.LocalDescription != "备注" {
		t.Fatalf("local attrs not merged: %+v", peer)
	}
	if scalar, ok := peer.LocalLabels["site"].Scalar(); !ok || scalar != "hq" {
		t.Fatalf("local labels not merged: %#v", peer.LocalLabels)
	}
	// Remote fields stay intact alongside local remarks.
	if peer.DisplayName != "remote-name" || !peer.Labels.MatchMember(types.LabelKeyCaps, "chat") {
		t.Fatalf("remote fields clobbered: %+v", peer)
	}

	peer, ok = r.Get("bot-b")
	if !ok {
		t.Fatal("bot-b missing")
	}
	if peer.LocalAlias != "" || peer.LocalDescription != "" || len(peer.LocalLabels) != 0 {
		t.Fatalf("unexpected local attrs on bot-b: %+v", peer)
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	for _, p := range list {
		if p.BotID == "bot-a" && p.LocalAlias != "别名" {
			t.Fatalf("List did not merge local attrs: %+v", p)
		}
		if p.BotID == "bot-b" && p.LocalAlias != "" {
			t.Fatalf("List leaked local attrs: %+v", p)
		}
	}
}

func TestRegistryRefreshDiscoveryPreservesLocalAttrs(t *testing.T) {
	r := NewRegistry()
	r.SetLocalAttrs("bot-a", types.PeerLocalAttrs{Alias: "别名"})
	r.Upsert(testPeer("bot-a"))

	// Remote rediscovery/heartbeat carries no local fields and must not
	// clear them. refreshDiscovery returns the previous view.
	_, existed := r.refreshDiscovery(testPeer("bot-a"))
	if !existed {
		t.Fatal("refreshDiscovery lost existing peer")
	}
	peer, ok := r.Get("bot-a")
	if !ok || peer.LocalAlias != "别名" {
		t.Fatalf("refreshDiscovery cleared local attrs: %+v ok=%v", peer, ok)
	}

	r.Upsert(testPeer("bot-a"))
	peer, ok = r.Get("bot-a")
	if !ok || peer.LocalAlias != "别名" {
		t.Fatalf("Upsert cleared local attrs: %+v ok=%v", peer, ok)
	}
}

func TestRegistrySetLocalAttrsZeroDeletes(t *testing.T) {
	r := NewRegistry()
	r.SetLocalAttrs("bot-a", types.PeerLocalAttrs{Alias: "别名"})
	if got := len(r.LocalAttrsSnapshot()); got != 1 {
		t.Fatalf("snapshot len = %d, want 1", got)
	}
	r.SetLocalAttrs("bot-a", types.PeerLocalAttrs{})
	if got := len(r.LocalAttrsSnapshot()); got != 0 {
		t.Fatalf("zero attrs did not delete entry, snapshot len = %d", got)
	}
}

func TestRegistryLocalAttrsSnapshotRoundTrip(t *testing.T) {
	src := NewRegistry()
	src.SetLocalAttrs("bot-a", types.PeerLocalAttrs{
		Alias:  "别名",
		Labels: types.Labels{}.WithCaps([]string{"ops"}),
	})
	src.SetLocalAttrs("bot-b", types.PeerLocalAttrs{Description: "备注"})

	snapshot := src.LocalAttrsSnapshot()
	dst := NewRegistry()
	dst.LoadLocalAttrs(snapshot)

	got := dst.LocalAttrsSnapshot()
	if len(got) != 2 {
		t.Fatalf("round-trip snapshot len = %d, want 2", len(got))
	}
	if got["bot-a"].Alias != "别名" || !got["bot-a"].Labels.MatchMember(types.LabelKeyCaps, "ops") {
		t.Fatalf("bot-a attrs lost: %+v", got["bot-a"])
	}
	if got["bot-b"].Description != "备注" {
		t.Fatalf("bot-b attrs lost: %+v", got["bot-b"])
	}

	// Mutating the snapshot must not affect the registry.
	snapshot["bot-a"] = types.PeerLocalAttrs{Alias: "tampered"}
	if src.LocalAttrsSnapshot()["bot-a"].Alias != "别名" {
		t.Fatal("snapshot aliased registry state")
	}
}
