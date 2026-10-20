package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const (
	WireVersion     = "v2"
	MaxMessageBytes = 256 << 10
)

// DecodeStrict accepts exactly one bounded JSON value, rejecting duplicate keys
// at every depth. Numbers retain their JSON spelling across a signed round trip.
func DecodeStrict(data []byte, out any) error {
	if len(data) > MaxMessageBytes || !utf8.Valid(data) {
		return errors.New("invalid or oversized JSON message")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := checkJSONValue(check, 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func checkJSONValue(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("invalid object key")
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = struct{}{}
			if err := checkJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := checkJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

// SignedBytes binds the exact subject and all fields except the top-level
// signature. v2 uses Go encoding/json key ordering and escaping, not RFC 8785.
// Numeric lexemes are preserved with UseNumber; callers must not coerce them.
func SignedBytes(subject string, value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := DecodeStrict(encoded, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("signed message must be an object")
	}
	delete(fields, "sig")
	delete(fields, "signature")
	return json.Marshal(struct {
		Domain  string         `json:"domain"`
		Subject string         `json:"subject"`
		Message map[string]any `json:"message"`
	}{Domain: "clawsynapse/wire/v2", Subject: subject, Message: fields})
}
