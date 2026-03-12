package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	base := t.TempDir()
	priv := filepath.Join(base, "identity.key")
	pub := filepath.Join(base, "identity.pub")

	id1, err := LoadOrCreate(priv, pub)
	if err != nil {
		t.Fatalf("first load/create failed: %v", err)
	}

	if _, err := os.Stat(priv); err != nil {
		t.Fatalf("private key not created: %v", err)
	}
	if _, err := os.Stat(pub); err != nil {
		t.Fatalf("public key not created: %v", err)
	}

	id2, err := LoadOrCreate(priv, pub)
	if err != nil {
		t.Fatalf("second load/create failed: %v", err)
	}

	if string(id1.PublicKey) != string(id2.PublicKey) {
		t.Fatal("expected same keypair after reload")
	}
}
