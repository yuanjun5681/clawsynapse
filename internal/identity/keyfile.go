package identity

import (
	"bytes"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

type privateFile struct {
	KTY        string `json:"kty"`
	PrivateKey string `json:"privateKey"`
}

type publicFile struct {
	KTY       string `json:"kty"`
	PublicKey string `json:"publicKey"`
}

func tryLoad(privatePath, publicPath string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privRaw, err := store.ReadFile(privatePath)
	if err != nil {
		return nil, nil, err
	}
	pubRaw, err := store.ReadFile(publicPath)
	if err != nil {
		return nil, nil, err
	}

	var pf privateFile
	if err := protocol.DecodeStrict(privRaw, &pf); err != nil {
		return nil, nil, err
	}
	var pubf publicFile
	if err := protocol.DecodeStrict(pubRaw, &pubf); err != nil {
		return nil, nil, err
	}

	if pf.KTY != "ed25519" || pubf.KTY != "ed25519" {
		return nil, nil, errors.New("unsupported key type")
	}

	privBytes, err := base64.RawURLEncoding.DecodeString(pf.PrivateKey)
	if err != nil {
		return nil, nil, err
	}
	pubBytes, err := base64.RawURLEncoding.DecodeString(pubf.PublicKey)
	if err != nil {
		return nil, nil, err
	}

	if len(privBytes) != ed25519.PrivateKeySize || len(pubBytes) != ed25519.PublicKeySize {
		return nil, nil, errors.New("invalid key size")
	}

	private := ed25519.NewKeyFromSeed(privBytes[:ed25519.SeedSize])
	if !bytes.Equal(private, privBytes) || !bytes.Equal(private.Public().(ed25519.PublicKey), pubBytes) {
		return nil, nil, errors.New("identity public and private keys do not match")
	}
	return private, ed25519.PublicKey(pubBytes), nil
}
