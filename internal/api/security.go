package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"clawsynapse/internal/config"
	"clawsynapse/internal/protocol"
)

const maxRequestBytes = protocol.MaxMessageBytes

func readToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read API token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", errors.New("API token file must be a regular file with permissions 0600")
	}
	if info.Size() > 4096 {
		return "", errors.New("invalid API token file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read API token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("invalid API token file")
	}
	return token, nil
}

func decodeRequest(r *http.Request, out any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxRequestBytes {
		return errors.New("request exceeds 256 KiB")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("request must be a JSON object")
	}
	if _, botConfig := out.(*config.BotConfig); botConfig && r.Method == http.MethodPut {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			return err
		}
		var revision uint64
		if raw, present := fields["revision"]; !present || json.Unmarshal(raw, &revision) != nil || revision == 0 {
			return errors.New("revision is required for configuration replacement")
		}
	}
	return config.DecodeStrict(trimmed, out)
}
