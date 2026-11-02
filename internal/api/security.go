package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

func ensureToken(path string) (string, error) {
	if _, err := os.Lstat(path); err == nil {
		return readToken(path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect API token file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create API token directory: %w", err)
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("generate API token failed")
	}
	token := hex.EncodeToString(raw[:])
	// Publish a fully written private file atomically; concurrent daemon starts never observe a partial token.
	f, err := os.CreateTemp(filepath.Dir(path), ".api-token-*")
	if err != nil {
		return "", fmt.Errorf("create API token file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return "", fmt.Errorf("write API token file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("sync API token file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close API token file: %w", err)
	}
	if err := os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("persist API token file: %w", err)
	}
	// Make the published credential durable before accepting management traffic.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return "", err
	}
	return readToken(path)
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
