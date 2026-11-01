package blob

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func registerRecoveryBot(t *testing.T, s *Store, bot string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"schemaVersion": 2, "bots": []any{map[string]any{"botId": bot, "desiredState": "stopped", "archived": true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.root, "bots.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestMissingReferenceDirectoryIsNotRecoveredAsEmpty(t *testing.T) {
	s := newTestStore(t, Limits{})
	ref := putTest(t, s, "A", "file", "preserve content", 100)
	registerRecoveryBot(t, s, "A")
	dir := filepath.Join(s.root, "bots", "A", "transfers")
	if err := os.Rename(dir, filepath.Join(s.root, "saved-references")); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateLayout("A", dir, filepath.Join(s.root, "bots", "A", "workspace")); err == nil {
		t.Fatal("recovery silently recreated missing references")
	}
	if _, err := s.List("A"); err == nil {
		t.Fatal("missing references became an empty history")
	}
	if err := s.ExportBot("A", filepath.Join(t.TempDir(), "bundle")); err == nil {
		t.Fatal("export silently lost references")
	}
	if _, err := s.Put("A", "new", bytes.NewBufferString("new"), WriteOptions{ExpectedSize: 3, Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("publication silently recreated missing references")
	}
	if err := s.GC(); err == nil {
		t.Fatal("GC accepted missing references")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("recovery mutated missing reference layout: %v", err)
	}
	if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
		t.Fatalf("GC lost live content: %v", err)
	}
}
func TestGCRequiresRegisteredBotAndReferenceCatalog(t *testing.T) {
	for _, missing := range []string{"Bot directory", "catalog"} {
		t.Run(missing, func(t *testing.T) {
			s := newTestStore(t, Limits{})
			ref := putTest(t, s, "A", "file", "preserve content", 100)
			registerRecoveryBot(t, s, "A")
			if missing == "Bot directory" {
				if err := os.Rename(filepath.Join(s.root, "bots", "A"), filepath.Join(s.root, "saved-bot")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(filepath.Join(s.root, "bots.json")); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.GC(); err == nil {
				t.Fatal("GC accepted incomplete registered state")
			}
			if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
				t.Fatalf("GC lost protected content: %v", err)
			}
		})
	}
}
