package adapter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/store"
)

func newPiFamilyAdapter(product, botID, binding, workspace, model string, logger *slog.Logger, sessions *store.FSStore) (*cliAdapter, error) {
	cli, err := newCLIAdapter(product, botID, binding, workspace, model, logger, sessions, nil)
	if err != nil {
		return nil, err
	}
	if err := cli.ensurePiSessionDir(); err != nil {
		return nil, fmt.Errorf("prepare native session directory: %w", err)
	}
	return cli, nil
}

func (a *cliAdapter) isPiFamily() bool {
	return a.product == "pi" || a.product == "omp"
}

func (a *cliAdapter) piSessionDir() string {
	return filepath.Join(a.workspace, ".clawsynapse", a.product+"-sessions")
}

func (a *cliAdapter) ensurePiSessionDir() error {
	if err := store.EnsurePrivateDir(a.workspace); err != nil {
		return err
	}
	if err := store.EnsurePrivateDir(filepath.Join(a.workspace, ".clawsynapse")); err != nil {
		return err
	}
	return store.EnsurePrivateDir(a.piSessionDir())
}

func safePiSessionID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if id[i] != '-' {
				return false
			}
			continue
		}
		ch := id[i]
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
			return false
		}
	}
	return true
}

func (a *cliAdapter) preparePiSession(id string) (string, error) {
	if err := a.ensurePiSessionDir(); err != nil {
		return "", &ExecutionError{Code: "session_unavailable", Stage: "session_load", Err: errors.New("private native session directory is unavailable")}
	}
	if id == "" {
		return "", nil
	}
	path, err := a.findPiSession(id)
	if err != nil {
		return "", &ExecutionError{Code: "session_unavailable", Stage: "session_load", Err: errors.New("persisted native session is missing, ambiguous, or corrupt; create a new session explicitly")}
	}
	return path, nil
}

// The store retains only a UUID, never a path supplied by the native process.
// Resolve that UUID exclusively within this binding's private session directory.
func (a *cliAdapter) findPiSession(id string) (string, error) {
	if !safePiSessionID(id) {
		return "", errors.New("invalid native session UUID")
	}
	dir := a.piSessionDir()
	if err := store.CheckPath(dir); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	suffix := "_" + id + ".jsonl"
	path := ""
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), suffix) || len(entry.Name()) == len(suffix) {
			continue
		}
		if path != "" {
			return "", errors.New("ambiguous native session UUID")
		}
		path = filepath.Join(dir, entry.Name())
	}
	if path == "" {
		return "", os.ErrNotExist
	}
	if err := validatePiSessionFile(path, id, a.product == "omp"); err != nil {
		return "", err
	}
	return path, nil
}

func validatePiSessionFile(path, id string, allowTitle bool) error {
	if err := store.CheckPath(path); err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return errors.New("native session is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return errors.New("native session file changed while opening")
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	header := false
	title := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Version int    `json:"v"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type == "" {
			return errors.New("corrupt native session entry")
		}
		if !header {
			// OMP stores one title metadata record before the session identity.
			if allowTitle && !title && entry.Type == "title" && entry.Version == 1 {
				title = true
				continue
			}
			if entry.Type != "session" || entry.ID != id {
				return errors.New("native session header does not match UUID")
			}
			header = true
		} else if entry.Type == "session" {
			return errors.New("duplicate native session header")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !header {
		return errors.New("native session header is missing")
	}
	after, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !after.Mode().IsRegular() || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) {
		return errors.New("native session file changed during validation")
	}
	return store.CheckPath(path)
}
