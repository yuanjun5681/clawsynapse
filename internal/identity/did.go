package identity

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// DeriveBotDID returns a did:key identifier from an Ed25519 public key.
// Format: did:key:z<base58btc(0xed01 + pubkey)>
// See https://w3c-ccg.github.io/did-method-key/
func DeriveBotDID(pub ed25519.PublicKey) string {
	// multicodec ed25519-pub = 0xed, varint encoded = [0xed, 0x01]
	prefixed := make([]byte, 2+len(pub))
	prefixed[0] = 0xed
	prefixed[1] = 0x01
	copy(prefixed[2:], pub)
	// multibase base58btc prefix is 'z'
	return "did:key:z" + encodeBase58(prefixed)
}

// DeriveBotID returns a deterministic, subject-safe Bot ID from a DID.
// Format: n1-<lowercase hex of the first 16 bytes of sha256(did)>
func DeriveBotID(did string) string {
	h := sha256.Sum256([]byte(did))
	return "n1-" + hex.EncodeToString(h[:16])
}

// FingerprintFromDID returns a sha256 fingerprint from a DID string,
// compatible with the existing Fingerprint format.
func FingerprintFromDID(did string) string {
	h := sha256.Sum256([]byte(did))
	return "sha256:" + hex.EncodeToString(h[:8])
}

func encodeBase58(input []byte) string {
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	var result []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		result = append(result, base58Alphabet[mod.Int64()])
	}

	// leading zeros
	for _, b := range input {
		if b != 0 {
			break
		}
		result = append(result, base58Alphabet[0])
	}

	// reverse
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}

	return string(result)
}
