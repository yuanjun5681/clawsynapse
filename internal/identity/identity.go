package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"

	"clawsynapse/internal/store"
)

type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

func LoadOrCreate(privatePath, publicPath string) (*Identity, error) {
	if privatePath == "" || publicPath == "" {
		return nil, errors.New("identity paths are required")
	}

	priv, pub, err := tryLoad(privatePath, publicPath)
	if err == nil {
		return &Identity{PrivateKey: priv, PublicKey: pub}, nil
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	privEnc := base64.RawURLEncoding.EncodeToString(private)
	pubEnc := base64.RawURLEncoding.EncodeToString(public)

	if err := os.MkdirAll(dirOf(privatePath), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirOf(publicPath), 0o700); err != nil {
		return nil, err
	}

	if err := store.WriteJSONAtomic(privatePath, map[string]string{"kty": "ed25519", "privateKey": privEnc}, 0o600); err != nil {
		return nil, err
	}
	if err := store.WriteJSONAtomic(publicPath, map[string]string{"kty": "ed25519", "publicKey": pubEnc}, 0o644); err != nil {
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
