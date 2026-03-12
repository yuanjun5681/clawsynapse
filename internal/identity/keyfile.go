package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	privRaw, err := os.ReadFile(privatePath)
	if err != nil {
		return nil, nil, err
	}
	pubRaw, err := os.ReadFile(publicPath)
	if err != nil {
		return nil, nil, err
	}

	var pf privateFile
	if err := json.Unmarshal(privRaw, &pf); err != nil {
		return nil, nil, err
	}
	var pubf publicFile
	if err := json.Unmarshal(pubRaw, &pubf); err != nil {
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

	return ed25519.PrivateKey(privBytes), ed25519.PublicKey(pubBytes), nil
}

func dirOf(path string) string {
	return filepath.Dir(path)
}
