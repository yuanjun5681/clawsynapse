package blob

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestStore(t *testing.T, limits Limits) *Store {
	t.Helper()
	s, err := New(t.TempDir(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "bots.json"), []byte(`{"schemaVersion":3,"bots":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return s
}
func putTest(t *testing.T, s *Store, bot, id, body string, quota int64) Reference {
	t.Helper()
	prepareTestBot(t, s, bot)
	ref, err := s.Put(bot, id, bytes.NewBufferString(body), WriteOptions{ExpectedSize: int64(len(body)), QuotaBytes: quota, Data: json.RawMessage(`{"test":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func readTest(t *testing.T, s *Store, bot, id string) string {
	t.Helper()
	f, err := s.Open(bot, id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func objectCount(t *testing.T, s *Store) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(s.root, "blobs", "sha256"), func(_ string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSharedContentKeepsSeparateAuthorizationAndStoppedBotReferences(t *testing.T) {
	s := newTestStore(t, Limits{})
	a := putTest(t, s, "bot-A", "transfer-A", "same bytes", 100)
	b := putTest(t, s, "bot-B", "transfer-B", "same bytes", 100)
	if a.Digest != b.Digest || objectCount(t, s) != 1 {
		t.Fatal("identical content was not deduplicated")
	}
	if _, err := s.Open("bot-C", a.TransferID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized read: %v", err)
	}
	if _, err := s.Get("bot-C", a.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("digest became a credential: %v", err)
	}
	if err := s.Delete("bot-A", "transfer-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("bot-A", "transfer-A"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted grant remains readable")
	}
	// B has no running Service. Its disk-backed grant still keeps the entity alive.
	if err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if got := readTest(t, s, "bot-B", "transfer-B"); got != "same bytes" {
		t.Fatal(got)
	}
	reader, err := s.Open("bot-B", "transfer-B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("bot-B", "transfer-B"); err != nil {
		t.Fatal(err)
	}
	if err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if objectCount(t, s) != 1 {
		t.Fatal("GC removed pinned read")
	}
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "same bytes" {
		t.Fatalf("read/GC race: %q %v", body, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if objectCount(t, s) != 0 {
		t.Fatal("unreferenced entity was not reclaimed")
	}
}
func TestConcurrentIdenticalPublicationAndWorkspaceIsolation(t *testing.T) {
	s := newTestStore(t, Limits{WorkspaceBytes: 100})
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, bot := range []string{"A", "B"} {
		prepareTestBot(t, s, bot)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Put(bot, "file", bytes.NewBufferString("immutable"), WriteOptions{ExpectedSize: 9, Data: json.RawMessage(`{}`)})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if objectCount(t, s) != 1 {
		t.Fatal("concurrent duplicate objects")
	}
	a, err := s.Workspace("A", "file")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Workspace("B", "file")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("A edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(b)
	if err != nil || string(body) != "immutable" {
		t.Fatalf("workspace alias: %s %v", body, err)
	}
	if got := readTest(t, s, "A", "file"); got != "immutable" {
		t.Fatal("workspace modified shared content")
	}
	a2, err := s.Workspace("A", "file")
	if err != nil || a2 != a {
		t.Fatal("workspace was unexpectedly replaced")
	}
	body, err = os.ReadFile(a)
	if err != nil || string(body) != "A edits" {
		t.Fatal("repeat materialization lost edits")
	}
	ref, err := s.Get("A", "file")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.objectPath(ref.Digest))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatal("immutable object is writable")
	}
}

type interruptedReader struct{ done bool }

func (r *interruptedReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, "incomplete"), errors.New("network disconnected")
}
func TestInterruptedOrInvalidDownloadsNeverCommit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader io.Reader
		opts   WriteOptions
	}{
		{"interrupt", &interruptedReader{}, WriteOptions{ExpectedSize: 10}},
		{"size", bytes.NewBufferString("short"), WriteOptions{ExpectedSize: 20}},
		{"checksum", bytes.NewBufferString("wrong"), WriteOptions{ExpectedSize: 5, ExpectedDigest: stringsOfZeroDigest()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, Limits{})
			prepareTestBot(t, s, "A")
			tc.opts.Data = json.RawMessage(`{}`)
			if _, err := s.Put("A", "file", tc.reader, tc.opts); err == nil {
				t.Fatal("bad download accepted")
			}
			refs, err := s.List("A")
			if err != nil || len(refs) != 0 {
				t.Fatalf("visible failed grant: %v %v", refs, err)
			}
			if objectCount(t, s) != 0 {
				t.Fatal("failed download published")
			}
			entries, err := os.ReadDir(s.tempDir())
			if err != nil || len(entries) != 0 {
				t.Fatal("failed download leaked temporary file")
			}
		})
	}
}
func stringsOfZeroDigest() string { return hex.EncodeToString(make([]byte, 32)) }
func TestDurableRecoveryAndCorruptReferenceFailClosedGC(t *testing.T) {
	s := newTestStore(t, Limits{})
	live := putTest(t, s, "archived-B", "file", "retained", 0)
	orphan := putTest(t, s, "A", "orphan", "orphan", 0)
	// Simulate a crash after entity publication but before a reference commit.
	if err := os.Remove(filepath.Join(s.root, "bots", "A", "transfers", "orphan.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.tempDir(), "download-crashed"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(s.root, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(reopened.tempDir())
	if err != nil || len(entries) != 0 {
		t.Fatal("startup did not discard unpublished temporary data")
	}
	if err := os.WriteFile(filepath.Join(s.root, "bots", "archived-B", "transfers", "file.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reopened.GC(); err == nil {
		t.Fatal("corrupt grant did not block GC")
	}
	if _, err := os.Stat(s.objectPath(orphan.Digest)); err != nil {
		t.Fatal("GC swept before completing safe mark")
	}
	if _, err := os.Stat(s.objectPath(live.Digest)); err != nil {
		t.Fatal("GC removed archived Bot content")
	}
}
func TestLogicalQuotaAndPhysicalTempWorkspaceBudgets(t *testing.T) {
	t.Run("quota per distinct Bot content", func(t *testing.T) {
		s := newTestStore(t, Limits{})
		putTest(t, s, "A", "one", "1234", 4)
		putTest(t, s, "A", "duplicate", "1234", 4)
		prepareTestBot(t, s, "B")
		if _, err := s.Put("B", "same", bytes.NewBufferString("1234"), WriteOptions{ExpectedSize: 4, QuotaBytes: 3, Data: json.RawMessage(`{}`)}); !errors.Is(err, ErrQuota) {
			t.Fatalf("dedupe bypassed quota: %v", err)
		}
		if _, err := s.Put("A", "other", bytes.NewBufferString("x"), WriteOptions{ExpectedSize: 1, QuotaBytes: 4, Data: json.RawMessage(`{}`)}); !errors.Is(err, ErrQuota) {
			t.Fatalf("quota ignored: %v", err)
		}
	})
	t.Run("physical", func(t *testing.T) {
		s := newTestStore(t, Limits{MaxBytes: 4})
		putTest(t, s, "A", "one", "1234", 0)
		putTest(t, s, "B", "same", "1234", 0)
		if _, err := s.Put("A", "two", bytes.NewBufferString("x"), WriteOptions{ExpectedSize: 1, Data: json.RawMessage(`{}`)}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("physical budget ignored: %v", err)
		}
	})
	t.Run("temporary", func(t *testing.T) {
		s := newTestStore(t, Limits{TempBytes: 3})
		prepareTestBot(t, s, "A")
		if _, err := s.Put("A", "one", bytes.NewBufferString("1234"), WriteOptions{ExpectedSize: 4, Data: json.RawMessage(`{}`)}); !errors.Is(err, ErrCapacity) {
			t.Fatalf("temporary budget ignored: %v", err)
		}
		if objectCount(t, s) != 0 {
			t.Fatal("oversize temporary object published")
		}
	})
	t.Run("workspace including stopped Bots", func(t *testing.T) {
		s := newTestStore(t, Limits{WorkspaceBytes: 4})
		putTest(t, s, "A", "one", "1234", 0)
		putTest(t, s, "B", "two", "1234", 0)
		if _, err := s.Workspace("A", "one"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Workspace("B", "two"); !errors.Is(err, ErrCapacity) {
			t.Fatalf("workspace budget ignored: %v", err)
		}
		if err := s.Delete("A", "one"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Workspace("B", "two"); err != nil {
			t.Fatal(err)
		}
	})
}
func TestBotExportImportIncludesEveryReferencedEntity(t *testing.T) {
	s := newTestStore(t, Limits{})
	putTest(t, s, "A", "one", "first", 100)
	putTest(t, s, "A", "two", "second", 100)
	putTest(t, s, "B", "private", "not exported", 100)
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := s.ExportBot("A", bundle); err != nil {
		t.Fatal(err)
	}
	restored := newTestStore(t, Limits{})
	if err := restored.ImportBot("A", bundle, 100); err != nil {
		t.Fatal(err)
	}
	if got := readTest(t, restored, "A", "one"); got != "first" {
		t.Fatal(got)
	}
	if got := readTest(t, restored, "A", "two"); got != "second" {
		t.Fatal(got)
	}
	if _, err := restored.Open("B", "private"); !errors.Is(err, ErrNotFound) {
		t.Fatal("export leaked other Bot grant")
	}
	if objectCount(t, restored) != 2 {
		t.Fatal("export omitted or leaked entities")
	}
	ref, err := restored.Get("A", "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bundle, "blobs", "sha256", ref.Digest[:2], ref.Digest)); err != nil {
		t.Fatal(err)
	}
	broken := newTestStore(t, Limits{})
	if err := broken.ImportBot("A", bundle, 100); err == nil {
		t.Fatal("incomplete bundle restored")
	}
	refs, err := broken.List("A")
	if err != nil || len(refs) != 0 {
		t.Fatal("incomplete bundle committed grants")
	}
}
func TestSymlinksCannotRedirectReferencesOrContent(t *testing.T) {
	s := newTestStore(t, Limits{})
	ref := putTest(t, s, "A", "one", "safe", 0)
	path := filepath.Join(s.root, "bots", "A", "transfers", "one.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "ref.json")
	data, _ := json.Marshal(ref)
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("A", "one"); err == nil {
		t.Fatal("symlink grant accepted")
	}
	if err := s.GC(); err == nil {
		t.Fatal("unsafe symlink did not block GC")
	}
}
func TestCorruptExistingObjectIsNotReused(t *testing.T) {
	s := newTestStore(t, Limits{})
	ref := putTest(t, s, "A", "one", "safe", 0)
	if err := os.Chmod(s.objectPath(ref.Digest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.objectPath(ref.Digest), []byte("evil"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("A", "one"); err == nil {
		t.Fatal("corrupt content read")
	}
	prepareTestBot(t, s, "B")
	if _, err := s.Put("B", "two", bytes.NewBufferString("safe"), WriteOptions{ExpectedSize: 4, Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("corrupt dedupe reused")
	}
	h := sha256.Sum256([]byte("safe"))
	if ref.Digest != hex.EncodeToString(h[:]) {
		t.Fatal("wrong local SHA256")
	}
}

func TestImportRestoresEntitiesWhenBotReferencesWereAlreadyCopied(t *testing.T) {
	source := newTestStore(t, Limits{})
	ref := putTest(t, source, "A", "one", "must restore", 100)
	putTest(t, source, "A", "two", "new reference", 100)
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := source.ExportBot("A", bundle); err != nil {
		t.Fatal(err)
	}
	target := newTestStore(t, Limits{})
	dir, err := target.refDir("A", true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "one.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := target.ImportBot("A", bundle, 100); err != nil {
		t.Fatal(err)
	}
	if got := readTest(t, target, "A", "two"); got != "new reference" {
		t.Fatal(got)
	}
	preserved, err := os.ReadFile(filepath.Join(dir, "one.json"))
	if err != nil || !bytes.Equal(preserved, data) {
		t.Fatal("recovery rewrote an existing valid reference")
	}
	if err := target.ImportBot("A", bundle, 100); err != nil {
		t.Fatalf("retrying a completed import failed: %v", err)
	}
	if objectCount(t, target) != 2 {
		t.Fatal("recovery omitted or duplicated an entity")
	}
	if _, err := target.Open("B", "one"); !errors.Is(err, ErrNotFound) {
		t.Fatal("restoring A granted another Bot access")
	}
	if got := readTest(t, target, "A", "one"); got != "must restore" {
		t.Fatal(got)
	}
}

func TestImportRejectsConflictingReferencesWithoutReplacingBytes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		sourceData json.RawMessage
		data       json.RawMessage
	}{
		{"content", "other bytes!", json.RawMessage(`{"test":true}`), json.RawMessage(`{"test":true}`)},
		{"metadata", "must restore", json.RawMessage(`{"test":true}`), json.RawMessage(`{"test":false}`)},
		{"string whitespace", "must restore", json.RawMessage(`{"test":"ab"}`), json.RawMessage(`{"test":"a b"}`)},
		{"exact number", "must restore", json.RawMessage(`{"test":9007199254740992}`), json.RawMessage(`{"test":9007199254740993}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newTestStore(t, Limits{})
			prepareTestBot(t, source, "A")
			if _, err := source.Put("A", "one", bytes.NewBufferString("must restore"), WriteOptions{ExpectedSize: 12, Data: tc.sourceData}); err != nil {
				t.Fatal(err)
			}
			bundle := filepath.Join(t.TempDir(), "bundle")
			if err := source.ExportBot("A", bundle); err != nil {
				t.Fatal(err)
			}
			target := newTestStore(t, Limits{})
			prepareTestBot(t, target, "A")
			_, err := target.Put("A", "one", bytes.NewBufferString(tc.body), WriteOptions{ExpectedSize: int64(len(tc.body)), Data: tc.data})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(target.root, "bots", "A", "transfers", "one.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := target.ImportBot("A", bundle, 100); err == nil {
				t.Fatal("conflicting reference accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("conflicting import replaced the existing reference")
			}
			if got := readTest(t, target, "A", "one"); got != tc.body {
				t.Fatal("conflicting import replaced the existing content")
			}
			if objectCount(t, target) != 1 {
				t.Fatal("conflicting import published an entity")
			}
		})
	}
}

func TestImportRecoveryWithCopiedReferencesHonorsBudgets(t *testing.T) {
	source := newTestStore(t, Limits{})
	ref := putTest(t, source, "A", "one", "1234", 100)
	bundle := filepath.Join(t.TempDir(), "bundle")
	if err := source.ExportBot("A", bundle); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		limits Limits
		quota  int64
		want   error
	}{
		{"physical", Limits{MaxBytes: 3}, 100, ErrCapacity},
		{"temporary", Limits{TempBytes: 3}, 100, ErrCapacity},
		{"logical", Limits{}, 3, ErrQuota},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := newTestStore(t, tc.limits)
			dir, err := target.refDir("A", true)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "one.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := target.ImportBot("A", bundle, tc.quota); !errors.Is(err, tc.want) {
				t.Fatalf("recovery bypassed budget: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, data) {
				t.Fatal("failed recovery changed an existing reference")
			}
			if objectCount(t, target) != 0 {
				t.Fatal("failed recovery published an entity")
			}
			entries, err := os.ReadDir(target.tempDir())
			if err != nil || len(entries) != 0 {
				t.Fatal("failed recovery leaked temporary bytes")
			}
		})
	}
}

func TestBindingWorkspaceValidationCopiesAndDeletion(t *testing.T) {
	s := newTestStore(t, Limits{})
	putTest(t, s, "A", "one", "original", 100)
	transfers := filepath.Join(s.root, "bots", "A", "transfers")
	oldBinding := filepath.Join(s.root, "bots", "A", "workspace", "old-binding")
	newBinding := filepath.Join(s.root, "bots", "A", "workspace", "new-binding")
	for _, workspace := range []string{oldBinding, newBinding} {
		if err := s.ValidateLayout("A", transfers, workspace); err != nil {
			t.Fatal(err)
		}
	}
	oldPath, err := s.Workspace("A", "one", oldBinding)
	if err != nil {
		t.Fatal(err)
	}
	newPath, err := s.Workspace("A", "one", newBinding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("old engine edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(newPath)
	if err != nil || string(body) != "original" {
		t.Fatal("binding workspaces aliased")
	}
	if err := s.ValidateLayout("A", transfers, filepath.Join(s.root, "bots", "B", "workspace")); err == nil {
		t.Fatal("other Bot workspace accepted")
	}
	symlink := filepath.Join(s.root, "bots", "A", "workspace", "redirected")
	if err := os.Symlink(t.TempDir(), symlink); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateLayout("A", transfers, filepath.Join(symlink, "binding")); err == nil {
		t.Fatal("workspace ancestor symlink accepted")
	}
	if err := os.Remove(symlink); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("A", "one"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oldPath, newPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("managed binding copy survived reference deletion")
		}
	}
}

func TestGCRecoversAbandonedWorkspaceTemporaries(t *testing.T) {
	s := newTestStore(t, Limits{WorkspaceBytes: 4})
	putTest(t, s, "A", "one", "1234", 100)
	dir := filepath.Join(s.root, "bots", "A", "workspace", "old-binding", "transfers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, ".copy-crashed")
	if err := os.WriteFile(orphan, []byte("partial stale generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.GC(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("abandoned copy consumed quota after recovery")
	}
	if _, err := s.Workspace("A", "one"); err != nil {
		t.Fatal(err)
	}
}
