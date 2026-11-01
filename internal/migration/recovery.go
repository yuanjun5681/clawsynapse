package migration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

// Recover rolls back only an interrupted, uncommitted layout publication.
// A completed migration is never rolled back: it may already contain new work.
// Failed layouts and originals remain on disk for operator inspection.
func Recover(target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := store.CheckPath(target); err != nil {
		return err
	}
	data, err := store.ReadFile(target + ".migration.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m marker
	if err := decodeManifest(data, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 2 || m.Target != target {
		return errors.New("invalid migration recovery marker")
	}
	if m.Status == "complete" || m.Status == "rolled-back" {
		return nil
	}
	if m.Status != "prepared" {
		return errors.New("unsupported migration recovery status")
	}
	lock, err := store.Lock(target + ".migration.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	// The root marker is authoritative if the final sidecar sync was interrupted.
	if data, err := store.ReadFile(filepath.Join(target, "migration.json")); err == nil {
		var current marker
		if err := decodeManifest(data, &current); err != nil {
			return err
		}
		if current.Status == "complete" && current.BotID == m.BotID {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if m.OriginalBackup == "" {
		if _, err := os.Lstat(target); err == nil {
			currentLock, err := store.Lock(filepath.Join(target, "daemon.lock"))
			if err != nil {
				return err
			}
			defer currentLock.Close()
			suffix, err := randomID()
			if err != nil {
				return err
			}
			if err := os.Rename(target, target+".uncommitted-"+suffix); err != nil {
				return err
			}
			if err := store.SyncDir(filepath.Dir(target)); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		m.Status = "rolled-back"
		return store.WriteJSONAtomic(target+".migration.json", m, 0o600)
	}
	parent := filepath.Dir(target)
	if filepath.Dir(m.OriginalBackup) != parent || !within(parent, m.OriginalBackup) || m.OriginalBackup == target {
		return errors.New("unsafe migration backup path")
	}
	if err := store.CheckPath(m.OriginalBackup); err != nil {
		return err
	}
	if _, err := os.Lstat(m.OriginalBackup); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	originalLock, err := store.Lock(filepath.Join(m.OriginalBackup, "daemon.lock"))
	if err != nil {
		return err
	}
	defer originalLock.Close()
	if _, err := os.Lstat(target); err == nil {
		currentLock, err := store.Lock(filepath.Join(target, "daemon.lock"))
		if err != nil {
			return err
		}
		defer currentLock.Close()
		suffix, err := randomID()
		if err != nil {
			return err
		}
		if err := os.Rename(target, target+".uncommitted-"+suffix); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(m.OriginalBackup, target); err != nil {
		return fmt.Errorf("restore original migration root: %w", err)
	}
	if err := store.SyncDir(parent); err != nil {
		return err
	}
	m.Status = "rolled-back"
	return store.WriteJSONAtomic(target+".migration.json", m, 0o600)
}

func decodeManifest(data []byte, value any) error {
	return protocol.DecodeStrictLimit(data, value, 64<<20)
}
