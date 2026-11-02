// Package blob owns immutable daemon-wide content and durable Bot-scoped grants.
// A digest is never an authorization credential: all reads resolve a Bot and transfer.
package blob

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"clawsynapse/internal/store"
)

var (
	ErrNotFound = errors.New("file reference not found")
	ErrQuota    = errors.New("Bot file quota exceeded")
	ErrCapacity = errors.New("blob storage budget exceeded")
)

type Limits struct{ MaxBytes, TempBytes, WorkspaceBytes int64 }

type Reference struct {
	SchemaVersion int             `json:"schemaVersion"`
	BotID         string          `json:"botId"`
	TransferID    string          `json:"transferId"`
	Digest        string          `json:"digest"`
	Size          int64           `json:"size"`
	Data          json.RawMessage `json:"data"`
}

type WriteOptions struct {
	MaxBytes       int64
	QuotaBytes     int64
	ExpectedSize   int64  // -1 permits an unknown size.
	ExpectedDigest string // wire v2 SHA-256 digest, or hexadecimal SHA-256.
	Data           json.RawMessage
}

type Store struct {
	mu     sync.Mutex // Serializes publication, grants, workspace creation, and GC.
	root   string
	limits Limits
	pins   map[string]int
}

func New(root string, limits Limits) (*Store, error) {
	if root == "" {
		return nil, errors.New("empty blob root")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := privateDir(root); err != nil {
		return nil, err
	}
	s := &Store{root: root, limits: limits, pins: make(map[string]int)}
	for _, path := range []string{filepath.Join(root, "bots"), filepath.Join(root, "blobs"), filepath.Join(root, "blobs", "sha256"), filepath.Join(root, "blobs", "tmp")} {
		if err := privateDir(path); err != nil {
			return nil, err
		}
	}
	// The daemon holds its process lock before opening this store. Temporary files
	// have never been published and cannot authorize a read after a restart.
	entries, err := os.ReadDir(s.tempDir())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("unsafe blob temporary entry")
		}
		if err := os.Remove(filepath.Join(s.tempDir(), entry.Name())); err != nil {
			return nil, err
		}
	}
	if err := store.SyncDir(s.tempDir()); err != nil {
		return nil, err
	}
	return s, nil
}

// ValidateLayout prevents a caller from redirecting a Bot's writable paths into
// another Bot or into the shared content tree.
func (s *Store) ValidateLayout(botID, transfers, workspace string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.refDir(botID, false)
	if err != nil {
		return err
	}
	actual, err := filepath.Abs(transfers)
	if err != nil {
		return err
	}
	if actual != dir {
		return errors.New("transfer directory is not Bot-scoped")
	}
	_, err = s.workspaceDir(botID, workspace)
	return err
}
func (s *Store) workspaceDir(botID, workspace string) (string, error) {
	root := filepath.Join(s.root, "bots", botID, "workspace")
	actual, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("workspace is not Bot-scoped")
	}
	current := root
	if err := privateDir(current); err != nil {
		return "", err
	}
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			if err := privateDir(current); err != nil {
				return "", err
			}
		}
	}
	return actual, nil
}
func (s *Store) tempDir() string { return filepath.Join(s.root, "blobs", "tmp") }
func (s *Store) objectPath(digest string) string {
	return filepath.Join(s.root, "blobs", "sha256", digest[:2], digest)
}
func (s *Store) objectDir(digest string) error {
	for _, path := range []string{filepath.Join(s.root, "blobs"), filepath.Join(s.root, "blobs", "sha256")} {
		if err := checkDir(path); err != nil {
			return err
		}
	}
	return privateDir(filepath.Dir(s.objectPath(digest)))
}
func validID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	b, err := hex.DecodeString(digest)
	return err == nil && hex.EncodeToString(b) == digest
}
func (s *Store) refDir(botID string, create bool) (string, error) {
	if !validID(botID) {
		return "", errors.New("invalid Bot ID")
	}
	botDir := filepath.Join(s.root, "bots", botID)
	for _, path := range []string{filepath.Join(s.root, "bots"), botDir, filepath.Join(botDir, "transfers")} {
		var err error
		if create {
			err = privateDir(path)
		} else {
			err = checkDir(path)
		}
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(botDir, "transfers"), nil
}
func privateDir(path string) error {
	if err := checkDir(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(path)
	if err := privateDir(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	if err := checkDir(path); err != nil {
		return err
	}
	if err := store.SyncDir(path); err != nil {
		return err
	}
	return store.SyncDir(parent)
}
func checkDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe storage directory")
	}
	return nil
}
func readReference(path, botID, id string) (Reference, error) {
	var ref Reference
	f, err := store.OpenRegular(path)
	if err != nil {
		return ref, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 16<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ref); err != nil {
		return ref, fmt.Errorf("corrupt reference %s: %w", id, err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return ref, errors.New("trailing reference data")
	}
	if ref.SchemaVersion != 1 || ref.BotID != botID || ref.TransferID != id || !validDigest(ref.Digest) || ref.Size < 0 || !json.Valid(ref.Data) {
		return ref, fmt.Errorf("invalid reference %s", id)
	}
	return ref, nil
}
func (s *Store) reference(botID, id string) (Reference, error) {
	if !validID(id) {
		return Reference{}, ErrNotFound
	}
	dir, err := s.refDir(botID, false)
	if os.IsNotExist(err) {
		return Reference{}, ErrNotFound
	}
	if err != nil {
		return Reference{}, err
	}
	ref, err := readReference(filepath.Join(dir, id+".json"), botID, id)
	if os.IsNotExist(err) {
		return Reference{}, ErrNotFound
	}
	return ref, err
}
func (s *Store) references(botID string) ([]Reference, error) {
	dir, err := s.refDir(botID, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	refs := make([]Reference, 0, len(entries))
	for _, entry := range entries {
		// Atomic JSON writer's crash leftovers are uncommitted, never references.
		if strings.HasPrefix(entry.Name(), ".tmp-") && entry.Type().IsRegular() {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("unrecognized transfer reference %s", entry.Name())
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID(id) {
			return nil, errors.New("invalid transfer reference name")
		}
		ref, err := readReference(filepath.Join(dir, entry.Name()), botID, id)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
func (s *Store) List(botID string) ([]Reference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.references(botID)
}

// ValidateBotReferences verifies every authorized object without copying its data.
// It is intended for offline migration validation, not a digest lookup interface.
func (s *Store) ValidateBotReferences(botID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs, err := s.references(botID)
	if err != nil {
		return err
	}
	seen := make(map[string]int64, len(refs))
	for _, ref := range refs {
		if size, ok := seen[ref.Digest]; ok {
			if size != ref.Size {
				return errors.New("inconsistent immutable blob reference size")
			}
			continue
		}
		if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
			return err
		}
		seen[ref.Digest] = ref.Size
	}
	return nil
}
func (s *Store) Get(botID, id string) (Reference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reference(botID, id)
}

type boundedWriter struct {
	out       io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, ErrCapacity
	}
	n, err := w.out.Write(p)
	w.remaining -= int64(n)
	return n, err
}

// Put publishes bytes before committing the grant. A failed commit may leave an
// unreachable immutable object, but can never expose an incomplete object.
func (s *Store) Put(botID, id string, r io.Reader, opts WriteOptions) (Reference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put(botID, id, r, opts)
}
func (s *Store) put(botID, id string, r io.Reader, opts WriteOptions) (Reference, error) {
	if !validID(id) || !json.Valid(opts.Data) {
		return Reference{}, errors.New("invalid transfer reference")
	}
	dir, err := s.refDir(botID, false)
	if err != nil {
		return Reference{}, err
	}
	if old, err := s.reference(botID, id); err == nil {
		if err := verifyObject(s.objectPath(old.Digest), old.Digest, old.Size); err != nil {
			return Reference{}, err
		}
		return old, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Reference{}, err
	}
	refs, err := s.references(botID)
	if err != nil {
		return Reference{}, err
	}
	f, err := os.CreateTemp(s.tempDir(), "download-")
	if err != nil {
		return Reference{}, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	hash := sha256.New()
	limit := opts.MaxBytes
	if s.limits.TempBytes > 0 && (limit <= 0 || s.limits.TempBytes < limit) {
		limit = s.limits.TempBytes
	}
	var source io.Reader = r
	if limit > 0 {
		source = io.LimitReader(r, limit+1)
	}
	var target io.Writer = io.MultiWriter(f, hash)
	if limit > 0 {
		target = &boundedWriter{out: target, remaining: limit}
	}
	n, err := io.Copy(target, source)
	if err != nil {
		return Reference{}, fmt.Errorf("download interrupted: %w", err)
	}
	if limit > 0 && n > limit {
		return Reference{}, ErrCapacity
	}
	if opts.ExpectedSize >= 0 && n != opts.ExpectedSize {
		return Reference{}, errors.New("transfer size mismatch")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if opts.ExpectedDigest != "" && !matchesDigest(digest, opts.ExpectedDigest) {
		return Reference{}, errors.New("transfer checksum mismatch")
	}
	seen := make(map[string]bool)
	var logical int64
	for _, ref := range refs {
		if !seen[ref.Digest] {
			logical += ref.Size
			seen[ref.Digest] = true
		}
	}
	if !seen[digest] && opts.QuotaBytes > 0 && n > opts.QuotaBytes-logical {
		return Reference{}, ErrQuota
	}
	path := s.objectPath(digest)
	if err := s.objectDir(digest); err != nil {
		return Reference{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := verifyObject(path, digest, n); err != nil {
			return Reference{}, err
		}
	} else if !os.IsNotExist(err) {
		return Reference{}, err
	} else {
		physical, err := treeBytes(filepath.Join(s.root, "blobs", "sha256"))
		if err != nil {
			return Reference{}, err
		}
		if s.limits.MaxBytes > 0 && n > s.limits.MaxBytes-physical {
			return Reference{}, ErrCapacity
		}
		if err := f.Chmod(0o400); err != nil {
			return Reference{}, err
		}
		if err := f.Sync(); err != nil {
			return Reference{}, err
		}
		if err := f.Close(); err != nil {
			return Reference{}, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return Reference{}, err
		}
		if err := store.SyncDir(filepath.Dir(path)); err != nil {
			return Reference{}, err
		}
		if err := store.SyncDir(filepath.Dir(filepath.Dir(path))); err != nil {
			return Reference{}, err
		}
	}
	ref := Reference{SchemaVersion: 1, BotID: botID, TransferID: id, Digest: digest, Size: n, Data: opts.Data}
	if err := store.WriteJSONAtomic(filepath.Join(dir, id+".json"), ref, 0o600); err != nil {
		return Reference{}, err
	}
	if err := store.SyncDir(dir); err != nil {
		return Reference{}, err
	}
	if err := store.SyncDir(filepath.Dir(dir)); err != nil {
		return Reference{}, err
	}
	return ref, nil
}
func matchesDigest(hexDigest, expected string) bool {
	if expected == hexDigest {
		return true
	}
	b, _ := hex.DecodeString(hexDigest)
	return expected == "SHA-256="+base64.URLEncoding.EncodeToString(b) || expected == "SHA-256="+base64.RawURLEncoding.EncodeToString(b) || expected == "SHA-256="+base64.StdEncoding.EncodeToString(b)
}
func verifyObject(path, digest string, size int64) error {
	parent := filepath.Dir(path)
	for range 3 {
		if err := checkDir(parent); err != nil {
			return err
		}
		parent = filepath.Dir(parent)
	}
	f, err := store.OpenRegular(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("missing or corrupt immutable blob")
	}
	return nil
}
func treeBytes(root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in storage tree")
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("nonregular storage entry")
		}
		n += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return n, err
}

// The publication mutex proves no copy generation is active. Recover only the
// reserved temporary names in managed transfers directories, including old bindings.
func workspaceBytes(root string) (int64, error) {
	var n int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in workspace")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("nonregular workspace entry")
		}
		if strings.HasPrefix(entry.Name(), ".copy-") && filepath.Base(filepath.Dir(path)) == "transfers" {
			if err := os.Remove(path); err != nil {
				return err
			}
			return store.SyncDir(filepath.Dir(path))
		}
		n += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return n, err
}

type pinnedReader struct {
	*os.File
	store  *Store
	digest string
	once   sync.Once
}

func (r *pinnedReader) Close() error {
	var err error
	r.once.Do(func() { err = r.File.Close(); r.store.mu.Lock(); r.store.pins[r.digest]--; r.store.mu.Unlock() })
	return err
}
func (s *Store) Open(botID, id string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, err := s.reference(botID, id)
	if err != nil {
		return nil, err
	}
	if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
		return nil, err
	}
	f, err := store.OpenRegular(s.objectPath(ref.Digest))
	if err != nil {
		return nil, err
	}
	s.pins[ref.Digest]++
	return &pinnedReader{File: f, store: s, digest: ref.Digest}, nil
}
func (s *Store) UpdateData(botID, id string, data json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !json.Valid(data) {
		return errors.New("invalid transfer metadata")
	}
	ref, err := s.reference(botID, id)
	if err != nil {
		return err
	}
	ref.Data = data
	dir, err := s.refDir(botID, false)
	if err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(filepath.Join(dir, id+".json"), ref, 0o600); err != nil {
		return err
	}
	return store.SyncDir(dir)
}
func (s *Store) Delete(botID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.reference(botID, id); err != nil {
		return err
	}
	dir, err := s.refDir(botID, false)
	if err != nil {
		return err
	}
	// Workspace files are deterministic independent copies, never the entity.
	workspace := filepath.Join(s.root, "bots", botID, "workspace")
	if err := checkDir(workspace); err == nil {
		if err := filepath.WalkDir(workspace, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("unsafe workspace entry")
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Name() == id && filepath.Base(filepath.Dir(path)) == "transfers" {
				if err := os.Remove(path); err != nil {
					return err
				}
				return store.SyncDir(filepath.Dir(path))
			}
			return nil
		}); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Join(dir, id+".json")); err != nil {
		return err
	}
	return store.SyncDir(dir)
}

// Workspace returns an independent writable copy. It never refreshes an existing
// copy, so Harness edits survive repeated notifications and restarts.
func (s *Store) Workspace(botID, id string, workspace ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, err := s.reference(botID, id)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.root, "bots", botID, "workspace")
	if len(workspace) > 1 {
		return "", errors.New("multiple workspace roots")
	}
	if len(workspace) == 1 {
		dir = workspace[0]
	}
	dir, err = s.workspaceDir(botID, dir)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "transfers")
	if err := privateDir(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("unsafe workspace copy")
		}
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	var total int64
	bots, err := os.ReadDir(filepath.Join(s.root, "bots"))
	if err != nil {
		return "", err
	}
	for _, bot := range bots {
		if !bot.IsDir() || bot.Type()&os.ModeSymlink != 0 {
			return "", errors.New("unsafe Bot directory")
		}
		n, err := workspaceBytes(filepath.Join(s.root, "bots", bot.Name(), "workspace"))
		if err != nil {
			return "", err
		}
		total += n
	}
	if s.limits.WorkspaceBytes > 0 && ref.Size > s.limits.WorkspaceBytes-total {
		return "", ErrCapacity
	}
	if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
		return "", err
	}
	in, err := store.OpenRegular(s.objectPath(ref.Digest))
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".copy-")
	if err != nil {
		return "", err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(out.Name(), path); err != nil {
		return "", err
	}
	if err := store.SyncDir(dir); err != nil {
		return "", err
	}
	return path, nil
}

// GC scans all disk-backed Bots, not just running objects. Any malformed grant
// aborts the entire mark phase before any object can be removed.
func (s *Store) GC() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range []string{filepath.Join(s.root, "bots"), filepath.Join(s.root, "blobs"), filepath.Join(s.root, "blobs", "sha256")} {
		if err := checkDir(path); err != nil {
			return err
		}
	}
	if err := s.validateRegisteredReferences(); err != nil {
		return err
	}
	live := make(map[string]bool)
	bots, err := os.ReadDir(filepath.Join(s.root, "bots"))
	if err != nil {
		return err
	}
	for _, bot := range bots {
		if !bot.IsDir() || bot.Type()&os.ModeSymlink != 0 || !validID(bot.Name()) {
			return errors.New("unsafe Bot directory during GC")
		}
		refs, err := s.references(bot.Name())
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if err := verifyObject(s.objectPath(ref.Digest), ref.Digest, ref.Size); err != nil {
				return err
			}
			live[ref.Digest] = true
		}
	}
	for _, bot := range bots {
		if _, err := workspaceBytes(filepath.Join(s.root, "bots", bot.Name(), "workspace")); err != nil {
			return err
		}
	}
	root := filepath.Join(s.root, "blobs", "sha256")
	return filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe blob entry during GC")
		}
		if e.IsDir() {
			return nil
		}
		digest := e.Name()
		if !validDigest(digest) || filepath.Base(filepath.Dir(path)) != digest[:2] {
			return errors.New("unexpected blob entry during GC")
		}
		if !live[digest] && s.pins[digest] == 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
			return store.SyncDir(filepath.Dir(path))
		}
		return nil
	})
}
