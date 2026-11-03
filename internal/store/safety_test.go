package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryLocksAreExclusiveAndIndependent(t *testing.T) {
	root := t.TempDir()
	first, err := Lock(filepath.Join(root, "a", "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if duplicate, err := Lock(filepath.Join(root, "a", "daemon.lock")); err == nil {
		duplicate.Close()
		t.Fatal("duplicate lock succeeded")
	}
	second, err := Lock(filepath.Join(root, "b", "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Lock(filepath.Join(root, "a", "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}
func TestStoreRejectsSymlinksAndCorruptState(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(link); err == nil {
		t.Fatal("accepted symlink directory")
	}
	if err := WriteJSONAtomic(filepath.Join(link, "state.json"), map[string]int{"a": 1}, 0o600); err == nil {
		t.Fatal("wrote through symlink")
	}
	for _, body := range []string{"not JSON", `{"schemaVersion":99}`, `null`} {
		data := t.TempDir()
		fs := NewFSStore(data)
		if err := os.WriteFile(fs.TrustPath(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fs.EnsureLayout(); err == nil {
			t.Fatalf("accepted corrupt trust state: %s", body)
		}
		raw, err := os.ReadFile(fs.TrustPath())
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != body {
			t.Fatal("corrupt state was reset")
		}
	}
}
func TestExplicitSessionDirectory(t *testing.T) {
	fs := NewFSStore(t.TempDir())
	fs.SessionDir = filepath.Join(fs.BaseDir, "sessions", "binding-one")
	path, err := fs.SessionPath("opencode", "session-key")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(fs.SessionDir, path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(rel)) != "opencode" {
		t.Fatalf("session escaped binding: %s", path)
	}
	if _, err := fs.SessionPath("../another-bot", "session-key"); err == nil {
		t.Fatal("accepted unsafe adapter path")
	}
}
