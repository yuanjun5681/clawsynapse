package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateAndLoadPreserveIdentity(t *testing.T) {
	base := t.TempDir()
	priv := filepath.Join(base, "identity.key")
	pub := filepath.Join(base, "identity.pub")

	id1, err := Create(priv, pub)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if _, err := os.Stat(priv); err != nil {
		t.Fatalf("private key not created: %v", err)
	}
	if _, err := os.Stat(pub); err != nil {
		t.Fatalf("public key not created: %v", err)
	}

	id2, err := Load(priv, pub)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if string(id1.PublicKey) != string(id2.PublicKey) {
		t.Fatal("expected same keypair after reload")
	}
}
