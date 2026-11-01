package blob

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/store"
)

// ExportBot writes a self-contained bundle without exposing a digest lookup
// interface. The destination must not exist; publication is one directory rename.
func (s *Store) ExportBot(botID, destination string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.references(botID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("export destination exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := privateDir(parent); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".blob-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := privateDir(filepath.Join(tmp, "transfers")); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, ref := range refs {
		if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
			return err
		}
		if !seen[ref.Digest] {
			path := filepath.Join(tmp, "blobs", "sha256", ref.Digest[:2], ref.Digest)
			if err := privateDir(filepath.Dir(path)); err != nil {
				return err
			}
			if err := copyFile(s.objectPath(ref.Digest), path, 0o400); err != nil {
				return err
			}
			seen[ref.Digest] = true
		}
		if err := store.WriteJSONAtomic(filepath.Join(tmp, "transfers", ref.TransferID+".json"), ref, 0o600); err != nil {
			return err
		}
	}
	if err := syncTree(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, destination); err != nil {
		return err
	}
	return store.SyncDir(parent)
}

// ImportBot verifies all exported grants and bytes before creating any grants.
// Callers restoring a Bot use their staged daemon root and publish it only after
// this returns successfully; existing grants are never overwritten.
func (s *Store) ImportBot(botID, source string, quotaBytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(botID) {
		return errors.New("invalid Bot ID")
	}
	if err := checkDir(source); err != nil {
		return err
	}
	dir := filepath.Join(source, "transfers")
	if err := checkDir(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	refs := make([]Reference, 0, len(entries))
	seen := make(map[string]bool)
	var logical int64
	// Import is an explicit creation transaction. Existing Bot layouts must
	// already contain their authoritative reference directory.
	if _, err := os.Lstat(filepath.Join(s.root, "bots", botID)); os.IsNotExist(err) {
		if _, err := s.refDir(botID, true); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	current, err := s.references(botID)
	if err != nil {
		return err
	}
	for _, ref := range current {
		if !seen[ref.Digest] {
			logical += ref.Size
			seen[ref.Digest] = true
		}
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			return errors.New("invalid exported reference")
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID(id) {
			return errors.New("invalid exported transfer ID")
		}
		ref, err := readReference(filepath.Join(dir, entry.Name()), botID, id)
		if err != nil {
			return err
		}
		path := filepath.Join(source, "blobs", "sha256", ref.Digest[:2], ref.Digest)
		for _, p := range []string{filepath.Join(source, "blobs"), filepath.Join(source, "blobs", "sha256"), filepath.Dir(path)} {
			if err := checkDir(p); err != nil {
				return err
			}
		}
		if err := verifyObject(path, ref.Digest, ref.Size); err != nil {
			return err
		}
		if old, err := s.reference(botID, id); err == nil {
			if old.Digest != ref.Digest || old.Size != ref.Size || !sameReferenceData(old.Data, ref.Data) {
				return fmt.Errorf("transfer %s already exists with different contents", id)
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if !seen[ref.Digest] {
			logical += ref.Size
			seen[ref.Digest] = true
		}
		refs = append(refs, ref)
	}
	if quotaBytes > 0 && logical > quotaBytes {
		return ErrQuota
	}
	for _, ref := range refs {
		_, err := s.reference(botID, ref.TransferID)
		if err == nil {
			if err := s.restoreObject(ref, source); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		f, err := store.OpenRegular(filepath.Join(source, "blobs", "sha256", ref.Digest[:2], ref.Digest))
		if err != nil {
			return err
		}
		_, putErr := s.put(botID, ref.TransferID, f, WriteOptions{ExpectedSize: ref.Size, ExpectedDigest: ref.Digest, QuotaBytes: quotaBytes, Data: ref.Data})
		closeErr := f.Close()
		if putErr != nil {
			return putErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// Atomic reference writes indent RawMessage values. Compare their JSON tokens,
// not that presentation whitespace, without decoding numbers through float64 or
// overlooking changes to the persisted authorization/provenance data.
func sameReferenceData(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	var left, right bytes.Buffer
	if json.Compact(&left, a) != nil || json.Compact(&right, b) != nil {
		return false
	}
	return bytes.Equal(left.Bytes(), right.Bytes())
}
func (s *Store) restoreObject(ref Reference, source string) error {
	path := s.objectPath(ref.Digest)
	if err := verifyObject(path, ref.Digest, ref.Size); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if s.limits.TempBytes > 0 && ref.Size > s.limits.TempBytes {
		return ErrCapacity
	}
	physical, err := treeBytes(filepath.Join(s.root, "blobs", "sha256"))
	if err != nil {
		return err
	}
	if s.limits.MaxBytes > 0 && ref.Size > s.limits.MaxBytes-physical {
		return ErrCapacity
	}
	if err := s.objectDir(ref.Digest); err != nil {
		return err
	}
	in, err := store.OpenRegular(filepath.Join(source, "blobs", "sha256", ref.Digest[:2], ref.Digest))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(s.tempDir(), "restore-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	writer := &boundedWriter{out: out, remaining: ref.Size}
	if _, err := io.Copy(writer, in); err != nil {
		return err
	}
	if err := out.Chmod(0o400); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := verifyObject(out.Name(), ref.Digest, ref.Size); err != nil {
		return err
	}
	if err := os.Rename(out.Name(), path); err != nil {
		return err
	}
	if err := store.SyncDir(filepath.Dir(path)); err != nil {
		return err
	}
	return store.SyncDir(filepath.Dir(filepath.Dir(path)))
}
func copyFile(source, destination string, mode os.FileMode) error {
	in, err := store.OpenRegular(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return store.SyncDir(path)
		}
		return nil
	})
}
