package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

func CanonicalJSON(input map[string]any) ([]byte, error) {
	if input == nil {
		return []byte("{}"), nil
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	ordered := make(map[string]any, len(input))
	for _, k := range keys {
		ordered[k] = input[k]
	}
	return json.Marshal(ordered)
}

func SignatureInput(messageType, subject, from, to string, ts int64, payload map[string]any) (string, error) {
	cp, err := CanonicalJSON(payload)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(cp)
	return strings.Join([]string{
		messageType,
		subject,
		from,
		to,
		strconv.FormatInt(ts, 10),
		hex.EncodeToString(h[:]),
	}, "\n"), nil
}
