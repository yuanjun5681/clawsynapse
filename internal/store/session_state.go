package store

import (
	"clawsynapse/internal/protocol"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type SessionState struct {
	SchemaVersion int    `json:"schemaVersion"`
	Adapter       string `json:"adapter"`
	SessionKey    string `json:"sessionKey"`
	SessionID     string `json:"sessionId"`
	CWD           string `json:"cwd,omitempty"`
	CreatedAtMs   int64  `json:"createdAtMs"`
	UpdatedAtMs   int64  `json:"updatedAtMs"`
}

func (s *FSStore) SessionPath(adapterName, sessionKey string) (string, error) {
	adapterName = strings.ToLower(strings.TrimSpace(adapterName))
	sessionKey = strings.TrimSpace(sessionKey)
	if adapterName != filepath.Base(adapterName) || adapterName == "." || adapterName == ".." || strings.ContainsAny(adapterName, `/\\`) {
		return "", errors.New("invalid adapter name")
	}
	if adapterName == "" {
		return "", errors.New("adapter name is required")
	}
	if sessionKey == "" {
		return "", errors.New("session key is required")
	}

	hash := sha256.Sum256([]byte(sessionKey))
	hexHash := hex.EncodeToString(hash[:])
	dir := s.SessionDir
	if dir == "" {
		dir = filepath.Join(s.BaseDir, "sessions")
	}
	return filepath.Join(dir, adapterName, hexHash[:2], hexHash+".json"), nil
}

func (s *FSStore) LoadSessionState(adapterName, sessionKey string) (SessionState, bool, error) {
	path, err := s.SessionPath(adapterName, sessionKey)
	if err != nil {
		return SessionState{}, false, err
	}

	b, err := ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionState{}, false, nil
		}
		return SessionState{}, false, err
	}

	var st SessionState
	if err := protocol.DecodeStrict(b, &st); err != nil {
		return SessionState{}, false, err
	}
	if st.SchemaVersion != 1 || st.Adapter != strings.ToLower(strings.TrimSpace(adapterName)) || st.SessionKey != strings.TrimSpace(sessionKey) || strings.TrimSpace(st.SessionID) == "" {
		return SessionState{}, false, errors.New("invalid or mismatched session state")
	}
	return st, true, nil
}

func (s *FSStore) SaveSessionState(st SessionState) error {
	st.Adapter = strings.ToLower(strings.TrimSpace(st.Adapter))
	st.SessionKey = strings.TrimSpace(st.SessionKey)
	st.SessionID = strings.TrimSpace(st.SessionID)
	if st.Adapter == "" {
		return errors.New("adapter is required")
	}
	if st.SessionKey == "" {
		return errors.New("session key is required")
	}
	if st.SessionID == "" {
		return errors.New("session id is required")
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}

	path, err := s.SessionPath(st.Adapter, st.SessionKey)
	if err != nil {
		return err
	}
	return WriteJSONAtomic(path, st, 0o600)
}

func (s *FSStore) DeleteSessionState(adapterName, sessionKey string) error {
	path, err := s.SessionPath(adapterName, sessionKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
