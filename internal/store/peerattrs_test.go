package store

import (
	"testing"

	"clawsynapse/pkg/types"
)

func TestPeerAttrsStateRoundTrip(t *testing.T) {
	fs := NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}

	st, err := fs.LoadPeerAttrsState()
	if err != nil {
		t.Fatalf("LoadPeerAttrsState: %v", err)
	}
	if st.SchemaVersion != 1 || len(st.Peers) != 0 {
		t.Fatalf("initial state = %#v", st)
	}

	st.Peers = map[string]types.PeerLocalAttrs{
		"bot-1": {
			Alias:       "现场网关",
			Description: "上海机房",
			Labels:      types.Labels{"floor": types.LabelString("3")},
		},
	}
	if err := fs.SavePeerAttrsState(st); err != nil {
		t.Fatalf("SavePeerAttrsState: %v", err)
	}

	loaded, err := fs.LoadPeerAttrsState()
	if err != nil {
		t.Fatalf("LoadPeerAttrsState: %v", err)
	}
	got := loaded.Peers["bot-1"]
	if got.Alias != "现场网关" || got.Description != "上海机房" {
		t.Fatalf("attrs = %#v", got)
	}
	if !got.Labels.MatchExact("floor", "3") {
		t.Fatalf("labels = %#v", got.Labels)
	}
}

func TestLoadPeerAttrsStateDropsZeroEntries(t *testing.T) {
	fs := NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	st := PeerAttrsState{Peers: map[string]types.PeerLocalAttrs{"bot-9": {}}}
	if err := fs.SavePeerAttrsState(st); err != nil {
		t.Fatalf("SavePeerAttrsState: %v", err)
	}
	loaded, err := fs.LoadPeerAttrsState()
	if err != nil {
		t.Fatalf("LoadPeerAttrsState: %v", err)
	}
	if len(loaded.Peers) != 0 {
		t.Fatalf("zero attrs should be dropped, got %#v", loaded.Peers)
	}
}
