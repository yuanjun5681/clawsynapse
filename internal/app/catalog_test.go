package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMissingCatalogWithExistingBotsDoesNotInitializeEmptyDaemon(t *testing.T) {
	a := testDaemon(t)
	id := newTestBot(t, a, "existing")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.lock.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.manager.cfg.DataDir, "bots.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewDaemon(a.manager.cfg, "test")
	if err == nil {
		_ = restored.lock.Close()
		t.Fatal("existing Bot state was silently registered as an empty installation")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing catalog was silently overwritten: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.manager.botDir(id), "credentials", "identity.key")); err != nil {
		t.Fatalf("original identity was not retained: %v", err)
	}
}
