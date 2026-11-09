package messaging

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"clawsynapse/internal/protocol"
)

// Progress is one visible work sentence, not answer output or context history.
func validateProgressPayload(payload protocol.MessagePayload) error {
	if strings.TrimSpace(payload.Text) == "" || len(payload.Text) > 512 || !utf8.ValidString(payload.Text) || strings.ContainsFunc(payload.Text, unicode.IsControl) {
		return errors.New("progress text must be a nonempty single-line UTF-8 string of at most 512 bytes")
	}
	if payload.Instruction != "" || len(payload.Input) != 0 || payload.Channel != "" || payload.Code != "" || payload.Stage != "" || payload.Message != "" || payload.Indeterminate || payload.AdapterRunID != "" {
		return errors.New("progress payload only supports text")
	}
	return nil
}
