package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadNeverReplacesMissingOrMalformedIdentity(t *testing.T) {
	for _, kind := range []string{"missing-private", "missing-public", "malformed-private", "mismatch"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			private := filepath.Join(root, "identity.key")
			public := filepath.Join(root, "identity.pub")
			if _, err := Create(private, public); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-private":
				if err := os.Remove(private); err != nil {
					t.Fatal(err)
				}
			case "missing-public":
				if err := os.Remove(public); err != nil {
					t.Fatal(err)
				}
			case "malformed-private":
				if err := os.WriteFile(private, []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "mismatch":
				other := t.TempDir()
				if _, err := Create(filepath.Join(other, "key"), filepath.Join(other, "pub")); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(other, "pub"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(public, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			beforePrivate, _ := os.ReadFile(private)
			beforePublic, _ := os.ReadFile(public)
			if _, err := Load(private, public); err == nil {
				t.Fatal("invalid identity loaded")
			}
			if _, err := Create(private, public); err == nil {
				t.Fatal("invalid existing identity replaced")
			}
			afterPrivate, _ := os.ReadFile(private)
			afterPublic, _ := os.ReadFile(public)
			if !bytes.Equal(beforePrivate, afterPrivate) || !bytes.Equal(beforePublic, afterPublic) {
				t.Fatal("identity was changed")
			}
		})
	}
}

func TestCreateRefusesExistingIdentity(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "identity.key")
	public := filepath.Join(root, "identity.pub")
	first, err := Create(private, public)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(private, public); err == nil {
		t.Fatal("duplicate create succeeded")
	}
	loaded, err := Load(private, public)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.PrivateKey, loaded.PrivateKey) {
		t.Fatal("identity changed")
	}
}
