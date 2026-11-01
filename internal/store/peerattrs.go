package store

import (
	"clawsynapse/internal/protocol"
	"errors"
	"os"
	"path/filepath"

	"clawsynapse/pkg/types"
)

// PeerAttrsState persists locally maintained peer remarks (alias,
// description, labels) keyed by BotID. Remote heartbeats never touch this
// file.
type PeerAttrsState struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Peers         map[string]types.PeerLocalAttrs `json:"peers,omitempty"`
}

func (s *FSStore) PeerAttrsPath() string {
	return filepath.Join(s.BaseDir, "peer-attrs.json")
}

func (s *FSStore) ensurePeerAttrsState() error {
	if _, err := os.Lstat(s.PeerAttrsPath()); err == nil {
		_, err = s.LoadPeerAttrsState()
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	st := PeerAttrsState{SchemaVersion: 1}
	return WriteJSONAtomic(s.PeerAttrsPath(), st, 0o600)
}

func (s *FSStore) LoadPeerAttrsState() (PeerAttrsState, error) {
	b, err := ReadFile(s.PeerAttrsPath())
	if err != nil {
		return PeerAttrsState{}, err
	}

	var st PeerAttrsState
	if err := protocol.DecodeStrictLimit(b, &st, 64<<20); err != nil {
		return PeerAttrsState{}, err
	}
	if st.SchemaVersion != 1 {
		return PeerAttrsState{}, errors.New("invalid peer attributes schema")
	}
	for botID, attrs := range st.Peers {
		if attrs.IsZero() {
			delete(st.Peers, botID)
		}
	}
	return st, nil
}

func (s *FSStore) SavePeerAttrsState(st PeerAttrsState) error {
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	return WriteJSONAtomic(s.PeerAttrsPath(), st, 0o600)
}
