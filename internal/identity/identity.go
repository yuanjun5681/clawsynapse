package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"clawsynapse/internal/store"
)

type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

// Load never creates or replaces identity material.
func Load(privatePath, publicPath string) (*Identity, error) {
	if privatePath == "" || publicPath == "" || privatePath == publicPath {
		return nil, errors.New("distinct identity paths are required")
	}
	priv, pub, err := tryLoad(privatePath, publicPath)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return &Identity{PrivateKey: priv, PublicKey: pub}, nil
}

// Create exclusively publishes a new pair. Existing or partial pairs are errors.
func Create(privatePath, publicPath string) (*Identity, error) {
	if privatePath == "" || publicPath == "" || privatePath == publicPath {
		return nil, errors.New("distinct identity paths are required")
	}
	for _, p := range []string{privatePath, publicPath} {
		if err := store.CheckPath(p); err != nil {
			return nil, err
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return nil, fmt.Errorf("identity path already exists or is inaccessible: %s", p)
		}
		if err := store.EnsurePrivateDir(filepath.Dir(p)); err != nil {
			return nil, err
		}
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	write := func(path string, value any) error {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(path)
			return err
		}
		return store.SyncDir(filepath.Dir(path))
	}
	if err := write(privatePath, privateFile{KTY: "ed25519", PrivateKey: base64.RawURLEncoding.EncodeToString(private)}); err != nil {
		return nil, err
	}
	if err := write(publicPath, publicFile{KTY: "ed25519", PublicKey: base64.RawURLEncoding.EncodeToString(public)}); err != nil {
		_ = os.Remove(privatePath)
		_ = store.SyncDir(filepath.Dir(privatePath))
		return nil, err
	}
	return &Identity{PrivateKey: private, PublicKey: public}, nil
}

func Fingerprint(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "sha256:" + hex.EncodeToString(h[:8])
}

func Sign(priv ed25519.PrivateKey, data []byte) string {
	sig := ed25519.Sign(priv, data)
	return base64.RawURLEncoding.EncodeToString(sig)
}

func Verify(pub ed25519.PublicKey, data []byte, signature string) bool {
	b, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, data, b)
}

func DecodePublicKey(encoded string) (ed25519.PublicKey, error) {
	pubBytes, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid key size")
	}
	return ed25519.PublicKey(pubBytes), nil
}
