package store

import (
	"clawsynapse/internal/protocol"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type FSStore struct {
	BaseDir    string
	SessionDir string
}

type TrustState struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Trusted       []TrustPeerState    `json:"trusted"`
	Pending       []TrustPendingState `json:"pending"`
	Rejected      []TrustPeerState    `json:"rejected"`
	Revoked       []TrustPeerState    `json:"revoked"`
}

type TrustPeerState struct {
	BotID  string `json:"nodeId"`
	AtMs   int64  `json:"atMs"`
	Reason string `json:"reason,omitempty"`
}

type TrustPendingState struct {
	RequestID    string `json:"requestId"`
	From         string `json:"from"`
	To           string `json:"to"`
	Direction    string `json:"direction"`
	Reason       string `json:"reason,omitempty"`
	ReceivedAtMs int64  `json:"receivedAtMs"`
}

type ReplayState struct {
	SchemaVersion int              `json:"schemaVersion"`
	Entries       map[string]int64 `json:"entries"`
}

func NewFSStore(baseDir string) *FSStore {
	return &FSStore{BaseDir: baseDir}
}

func (s *FSStore) EnsureLayout() error {
	if s.BaseDir == "" {
		return errors.New("base dir is empty")
	}

	if err := EnsurePrivateDir(s.BaseDir); err != nil {
		return err
	}
	if err := EnsurePrivateDir(filepath.Join(s.BaseDir, "peers")); err != nil {
		return err
	}
	if err := EnsurePrivateDir(filepath.Join(s.BaseDir, "sessions")); err != nil {
		return err
	}
	if err := EnsurePrivateDir(filepath.Join(s.BaseDir, "transfers")); err != nil {
		return err
	}

	if err := s.ensureTrustState(); err != nil {
		return err
	}
	if err := s.ensureReplayState(); err != nil {
		return err
	}
	if err := s.ensurePeerAttrsState(); err != nil {
		return err
	}

	return nil
}

func (s *FSStore) TrustPath() string {
	return filepath.Join(s.BaseDir, "trust.json")
}

func (s *FSStore) ReplayPath() string {
	return filepath.Join(s.BaseDir, "replay-cache.json")
}

func (s *FSStore) ensureTrustState() error {
	if _, err := os.Lstat(s.TrustPath()); err == nil {
		_, err = s.LoadTrustState()
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	st := TrustState{
		SchemaVersion: 1,
		Trusted:       []TrustPeerState{},
		Pending:       []TrustPendingState{},
		Rejected:      []TrustPeerState{},
		Revoked:       []TrustPeerState{},
	}
	return WriteJSONAtomic(s.TrustPath(), st, 0o600)
}

func (s *FSStore) ensureReplayState() error {
	if _, err := os.Lstat(s.ReplayPath()); err == nil {
		_, err = s.LoadReplayState()
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	st := ReplayState{
		SchemaVersion: 1,
		Entries:       map[string]int64{"bootAt": time.Now().UnixMilli()},
	}
	return WriteJSONAtomic(s.ReplayPath(), st, 0o600)
}

func (s *FSStore) LoadReplayState() (ReplayState, error) {
	b, err := ReadFile(s.ReplayPath())
	if err != nil {
		return ReplayState{}, err
	}

	var st ReplayState
	if err := protocol.DecodeStrictLimit(b, &st, 64<<20); err != nil {
		return ReplayState{}, err
	}
	if st.SchemaVersion != 1 || st.Entries == nil {
		return ReplayState{}, errors.New("invalid replay state schema or missing entries")
	}
	return st, nil
}

func (s *FSStore) SaveReplayState(st ReplayState) error {
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.Entries == nil {
		st.Entries = map[string]int64{}
	}
	return WriteJSONAtomic(s.ReplayPath(), st, 0o600)
}

func (s *FSStore) LoadTrustState() (TrustState, error) {
	b, err := ReadFile(s.TrustPath())
	if err != nil {
		return TrustState{}, err
	}

	var st TrustState
	if err := protocol.DecodeStrictLimit(b, &st, 64<<20); err != nil {
		return TrustState{}, err
	}
	if st.SchemaVersion != 1 {
		return TrustState{}, errors.New("invalid trust state schema")
	}
	if st.Trusted == nil {
		st.Trusted = []TrustPeerState{}
	}
	if st.Pending == nil {
		st.Pending = []TrustPendingState{}
	}
	if st.Rejected == nil {
		st.Rejected = []TrustPeerState{}
	}
	if st.Revoked == nil {
		st.Revoked = []TrustPeerState{}
	}
	return st, nil
}

func (s *FSStore) SaveTrustState(st TrustState) error {
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.Trusted == nil {
		st.Trusted = []TrustPeerState{}
	}
	if st.Pending == nil {
		st.Pending = []TrustPendingState{}
	}
	if st.Rejected == nil {
		st.Rejected = []TrustPeerState{}
	}
	if st.Revoked == nil {
		st.Revoked = []TrustPeerState{}
	}
	return WriteJSONAtomic(s.TrustPath(), st, 0o600)
}
