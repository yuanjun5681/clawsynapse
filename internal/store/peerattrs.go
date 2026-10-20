package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"clawsynapse/pkg/types"
)

// PeerAttrsState persists locally maintained peer remarks (alias,
// description, labels) keyed by NodeID. Remote heartbeats never touch this
// file.
type PeerAttrsState struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Peers         map[string]types.PeerLocalAttrs `json:"peers,omitempty"`
}

func (s *FSStore) PeerAttrsPath() string {
	return filepath.Join(s.BaseDir, "peer-attrs.json")
}

func (s *FSStore) ensurePeerAttrsState() error {
	if _, err := os.Stat(s.PeerAttrsPath()); err == nil {
		return nil
	}
	st := PeerAttrsState{SchemaVersion: 1}
	return WriteJSONAtomic(s.PeerAttrsPath(), st, 0o600)
}

func (s *FSStore) LoadPeerAttrsState() (PeerAttrsState, error) {
	b, err := os.ReadFile(s.PeerAttrsPath())
	if err != nil {
		return PeerAttrsState{}, err
	}

	var st PeerAttrsState
	if err := json.Unmarshal(b, &st); err != nil {
		return PeerAttrsState{}, err
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	for nodeID, attrs := range st.Peers {
		if attrs.IsZero() {
			delete(st.Peers, nodeID)
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
