package auth

import (
	"testing"
	"time"

	"clawsynapse/internal/store"
)

func TestReplayGuardDetectsDuplicate(t *testing.T) {
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}

	rg, err := NewReplayGuard(fs, 100, 10*time.Minute)
	if err != nil {
		t.Fatalf("new replay guard: %v", err)
	}

	ts := time.Now().UnixMilli()
	if err := rg.CheckAndRemember("k1", ts); err != nil {
		t.Fatalf("first remember should succeed: %v", err)
	}
	if err := rg.CheckAndRemember("k1", ts); err == nil {
		t.Fatal("second remember should fail")
	}
}

func TestReplayGuardPersistsAcrossReload(t *testing.T) {
	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}

	rg, err := NewReplayGuard(fs, 100, 10*time.Minute)
	if err != nil {
		t.Fatalf("new replay guard: %v", err)
	}

	ts := time.Now().UnixMilli()
	if err := rg.CheckAndRemember("persisted", ts); err != nil {
		t.Fatalf("remember should succeed: %v", err)
	}

	rg2, err := NewReplayGuard(fs, 100, 10*time.Minute)
	if err != nil {
		t.Fatalf("new replay guard reload: %v", err)
	}
	if err := rg2.CheckAndRemember("persisted", ts); err == nil {
		t.Fatal("reloaded cache should detect duplicate")
	}
}
