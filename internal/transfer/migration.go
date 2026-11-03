package transfer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/store"
)

// ImportLegacy preserves every legacy entity even when its sender, bucket, or
// original transfer ID can no longer be reconstructed. The caller stages the
// daemon root and retains the original legacy directory until its own commit.
func ImportLegacy(legacyDir, botID string, blobs *blob.Store, maxFileSize, quotaBytes int64) error {
	info, err := os.Lstat(legacyDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe legacy transfer directory")
	}
	return filepath.WalkDir(legacyDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("legacy transfers contain a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("legacy transfers contain a nonregular file")
		}
		rel, err := filepath.Rel(legacyDir, path)
		if err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(filepath.ToSlash(rel)))
		id := "legacy-" + hex.EncodeToString(hash[:])
		name := entry.Name()
		// Native v2 transfer IDs have exactly 22 base64url characters. They
		// may themselves contain '-', so splitting on the first '-' loses IDs.
		if len(name) > 23 && name[22] == '-' && !strings.Contains(rel, string(filepath.Separator)) {
			candidate, decodeErr := base64.RawURLEncoding.DecodeString(name[:22])
			if decodeErr == nil && len(candidate) == 16 {
				existing, getErr := blobs.Get(botID, name[:22])
				if errors.Is(getErr, blob.ErrNotFound) {
					id = name[:22]
				} else if getErr != nil {
					return getErr
				} else {
					var previous TransferRecord
					if err := json.Unmarshal(existing.Data, &previous); err != nil {
						return err
					}
					if previous.Metadata["originalPath"] == filepath.ToSlash(rel) {
						id = name[:22]
					}
				}
				name = name[23:]
			}
		}
		f, err := store.OpenRegular(path)
		if err != nil {
			return err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			return err
		}
		if existing, getErr := blobs.Get(botID, id); getErr == nil {
			h := sha256.New()
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
			if hex.EncodeToString(h.Sum(nil)) != existing.Digest {
				return errors.New("legacy file changed since its previous import")
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
		} else if !errors.Is(getErr, blob.ErrNotFound) {
			return getErr
		}
		rec := TransferRecord{TransferID: id, Direction: "imported", FileName: safeName(name), FileSize: stat.Size(), Status: "completed", CreatedAt: stat.ModTime().UnixMilli(), CompletedAt: stat.ModTime().UnixMilli(), Metadata: map[string]any{"source": "legacy-import", "originalPath": filepath.ToSlash(rel), "originUnknown": true}}
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		_, err = blobs.Put(botID, id, f, blob.WriteOptions{MaxBytes: maxFileSize, QuotaBytes: quotaBytes, ExpectedSize: stat.Size(), Data: data})
		return err
	})
}
