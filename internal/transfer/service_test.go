package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/blob"
)

func testService(t *testing.T, root, botID string, blobs *blob.Store, quota int64) *Service {
	t.Helper()
	createTestBot(t, root, botID)
	s := NewService(nil, nil, nil, nil, botID, nil, "open", TransferConfig{BotID: botID, Blobs: blobs, TransferDir: filepath.Join(root, "bots", botID, "transfers"), Workspace: filepath.Join(root, "bots", botID, "workspace"), QuotaBytes: quota, MaxFileSize: 1000})
	if err := s.ConfigurePersistence(); err != nil {
		t.Fatal(err)
	}
	return s
}
func testBlobStore(t *testing.T, root string, limits blob.Limits) *blob.Store {
	t.Helper()
	s, err := blob.New(root, limits)
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(root, "bots.json")
	if _, err := os.Stat(catalog); os.IsNotExist(err) {
		if err := os.WriteFile(catalog, []byte(`{"schemaVersion":3,"bots":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	return s
}
func importTestFile(t *testing.T, s *Service, body string) TransferInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := s.ImportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
func readTransfer(t *testing.T, s *Service, id string) string {
	t.Helper()
	f, err := s.OpenFile(id)
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

func TestTransferReferenceSurvivesRestartAndScopesAllOperations(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	a := testService(t, root, "A", blobs, 100)
	b := testService(t, root, "B", blobs, 100)
	info := importTestFile(t, a, "local import")
	if info.Direction != "imported" || info.LocalPath != "" || info.Checksum == "" {
		t.Fatalf("invalid imported record: %+v", info)
	}
	if _, err := b.OpenFile(info.TransferID); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("cross-Bot read: %v", err)
	}
	if _, err := b.ForwardFile(info.TransferID, SendFileRequest{TargetBot: "external"}); !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("cross-Bot forward: %v", err)
	}
	if err := b.DeleteTransfer(info.TransferID); err == nil {
		t.Fatal("cross-Bot delete succeeded")
	}
	if got := readTransfer(t, a, info.TransferID); got != "local import" {
		t.Fatal(got)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := testBlobStore(t, root, blob.Limits{})
	restored := testService(t, root, "A", reopened, 100)
	if len(restored.ListTransfers()) != 1 || readTransfer(t, restored, info.TransferID) != "local import" {
		t.Fatal("transfer record or grant was not durable")
	}
	if err := restored.DeleteTransfer(info.TransferID); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.OpenFile(info.TransferID); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("deleted grant remained usable")
	}
}

func TestInboxOnlyReceivePersistsWithoutExecutionWorkspace(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{WorkspaceBytes: 1})
	createTestBot(t, root, "console")
	cfg := TransferConfig{BotID: "console", Blobs: blobs, TransferDir: filepath.Join(root, "bots", "console", "transfers"), Workspace: filepath.Join(root, "bots", "console", "workspace"), QuotaBytes: 100, MaxFileSize: 1000, InboxOnly: true}
	s := NewService(nil, nil, nil, nil, "console", nil, "open", cfg)
	if err := s.ConfigurePersistence(); err != nil {
		t.Fatal(err)
	}
	rec := &TransferRecord{TransferID: "console-file", Direction: "inbound", PeerBot: "sender", Status: "completed", NotificationState: "pending"}
	if err := s.commit(rec, strings.NewReader("payload"), blob.WriteOptions{ExpectedSize: 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.notifyPending(); err != nil {
		t.Fatal(err)
	}
	if got := readTransfer(t, s, rec.TransferID); got != "payload" {
		t.Fatalf("received file inaccessible: %q", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored := NewService(nil, nil, nil, nil, "console", nil, "open", cfg)
	defer restored.Close()
	if err := restored.ConfigurePersistence(); err != nil {
		t.Fatal(err)
	}
	if state := restored.transfers[rec.TransferID].NotificationState; state != "admitted" {
		t.Fatalf("passive notification not durably consumed: %q", state)
	}
	if err := restored.notifyPending(); err != nil {
		t.Fatal(err)
	}
	if got := readTransfer(t, restored, rec.TransferID); got != "payload" {
		t.Fatalf("received file lost after restart: %q", got)
	}
}
func TestReceiveAdmissionIsDurableAndWorkspaceIsIndependent(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	s := testService(t, root, "A", blobs, 100)
	rec := &TransferRecord{TransferID: "received", Direction: "inbound", PeerBot: "sender", FileName: "../../untrusted.txt", Status: "completed", NotificationState: "pending"}
	if err := s.commit(rec, strings.NewReader("content"), blob.WriteOptions{ExpectedSize: 7, QuotaBytes: 100}); err != nil {
		t.Fatal(err)
	}
	called := make(chan TransferRecord, 2)
	s.OnReceived(func(rec TransferRecord) { called <- rec })
	if err := s.notifyPending(); err != nil {
		t.Fatal(err)
	}
	var delivered TransferRecord
	select {
	case delivered = <-called:
	case <-time.After(time.Second):
		t.Fatal("receive callback missing")
	}
	if !strings.HasPrefix(delivered.LocalPath, filepath.Join(root, "bots", "A", "workspace")+string(filepath.Separator)) || strings.Contains(delivered.LocalPath, "blobs") {
		t.Fatal("Harness was given a shared/global path")
	}
	ref, err := blobs.Get("A", "received")
	if err != nil {
		t.Fatal(err)
	}
	var durable TransferRecord
	if err := json.Unmarshal(ref.Data, &durable); err != nil {
		t.Fatal(err)
	}
	if durable.NotificationState != "admitted" || durable.LocalPath != "" {
		t.Fatal("notification happened before durable admission")
	}
	if err := os.WriteFile(delivered.LocalPath, []byte("Harness edits"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readTransfer(t, s, "received"); got != "content" {
		t.Fatal("Harness modified immutable blob")
	}
	if err := s.notifyPending(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := testService(t, root, "A", testBlobStore(t, root, blob.Limits{}), 100)
	restarted.OnReceived(func(rec TransferRecord) { called <- rec })
	if err := restarted.notifyPending(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("duplicate notification after redelivery/restart")
	default:
	}
}
func TestPendingReceiveRecoversBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	s := testService(t, root, "A", blobs, 100)
	rec := &TransferRecord{TransferID: "crashed-before-notify", Direction: "inbound", Status: "completed", NotificationState: "pending"}
	if err := s.commit(rec, strings.NewReader("payload"), blob.WriteOptions{ExpectedSize: 7}); err != nil {
		t.Fatal(err)
	}
	restarted := testService(t, root, "A", testBlobStore(t, root, blob.Limits{}), 100)
	called := make(chan TransferRecord, 1)
	restarted.OnReceived(func(rec TransferRecord) { called <- rec })
	if err := restarted.notifyPending(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-called:
		if got.TransferID != rec.TransferID {
			t.Fatal(got)
		}
	case <-time.After(time.Second):
		t.Fatal("pending durable notification did not recover")
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestReceiveWorkspaceBudgetFailureLeavesRecoverableReference(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{WorkspaceBytes: 2})
	s := testService(t, root, "A", blobs, 100)
	rec := &TransferRecord{TransferID: "pending", Direction: "inbound", Status: "completed", NotificationState: "pending"}
	if err := s.commit(rec, strings.NewReader("payload"), blob.WriteOptions{ExpectedSize: 7}); err != nil {
		t.Fatal(err)
	}
	s.OnReceived(func(TransferRecord) { t.Error("callback admitted despite insufficient budget") })
	if err := s.notifyPending(); !errors.Is(err, blob.ErrCapacity) {
		t.Fatalf("workspace quota ignored: %v", err)
	}
	if got := readTransfer(t, s, "pending"); got != "payload" {
		t.Fatal("materialization failure lost reference")
	}
	ref, err := blobs.Get("A", "pending")
	if err != nil {
		t.Fatal(err)
	}
	var durable TransferRecord
	if err := json.Unmarshal(ref.Data, &durable); err != nil {
		t.Fatal(err)
	}
	if durable.NotificationState != "pending" {
		t.Fatal("unexecuted notification was admitted")
	}
}
func TestSendCannotReadOutsideBotWorkspace(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	a := testService(t, root, "A", blobs, 100)
	b := testService(t, root, "B", blobs, 100)
	info := importTestFile(t, b, "B private")
	copyPath, err := blobs.Workspace("B", info.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendFile(SendFileRequest{FilePath: copyPath, TargetBot: "peer"}); err == nil || !strings.Contains(err.Error(), "administrator import") {
		t.Fatalf("cross-Bot workspace accepted: %v", err)
	}
	link := filepath.Join(root, "bots", "A", "workspace", "linked")
	if err := os.Symlink(copyPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendFile(SendFileRequest{FilePath: link, TargetBot: "peer"}); err == nil {
		t.Fatal("workspace symlink accepted")
	}
}
func TestDeleteOneBotReferenceDoesNotDeleteAnother(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	a := testService(t, root, "A", blobs, 100)
	b := testService(t, root, "B", blobs, 100)
	first := importTestFile(t, a, "identical")
	second := importTestFile(t, b, "identical")
	if _, err := blobs.Workspace("A", first.TransferID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteTransfer(first.TransferID); err != nil {
		t.Fatal(err)
	}
	if err := blobs.GC(); err != nil {
		t.Fatal(err)
	}
	if got := readTransfer(t, b, second.TransferID); got != "identical" {
		t.Fatal("delete affected another Bot")
	}
}
func TestCorruptTransferMetadataFailsRecovery(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	s := testService(t, root, "A", blobs, 100)
	info := importTestFile(t, s, "keep")
	if err := blobs.UpdateData("A", info.TransferID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(nil, nil, nil, nil, "A", nil, "open", TransferConfig{BotID: "A", Blobs: blobs, TransferDir: filepath.Join(root, "bots", "A", "transfers"), Workspace: filepath.Join(root, "bots", "A", "workspace")})
	if err := restarted.ConfigurePersistence(); err == nil {
		t.Fatal("corrupt history silently became empty")
	}
}
func TestLegacyImportPreservesUnrecoverableOriginsAndNestedFiles(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, "unknown-origin.bin"), []byte("old bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(legacy, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "nested", "other"), []byte("other bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	createTestBot(t, root, "A")
	if err := ImportLegacy(legacy, "A", blobs, 100, 100); err != nil {
		t.Fatal(err)
	}
	s := testService(t, root, "A", blobs, 100)
	if len(s.ListTransfers()) != 2 {
		t.Fatal("legacy import omitted files")
	}
	for _, info := range s.ListTransfers() {
		if info.Metadata["originUnknown"] != true {
			t.Fatal("legacy source invented")
		}
		if got := readTransfer(t, s, info.TransferID); got != "old bytes" && got != "other bytes" {
			t.Fatal(got)
		}
	}
	if err := ImportLegacy(legacy, "A", blobs, 100, 100); err != nil {
		t.Fatal(err)
	}
	if refs, err := blobs.List("A"); err != nil || len(refs) != 2 {
		t.Fatal("legacy retry duplicated references")
	}
}
func TestTransferViewHasOnlyBotVocabularyAndNoEntityPath(t *testing.T) {
	rec := &TransferRecord{TransferID: "one", PeerBot: "peer", LocalPath: "/global/secret", BlobDigest: "secret", Metadata: map[string]any{"key": "value"}}
	view := toTransferInfo(rec)
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"peerBot":"peer"`)) || bytes.Contains(data, []byte("peerNode")) || bytes.Contains(data, []byte("/global")) || bytes.Contains(data, []byte("blobDigest")) {
		t.Fatalf("unsafe local view %s", data)
	}
	view.Metadata["key"] = "edited"
	if rec.Metadata["key"] != "value" {
		t.Fatal("view exposes mutable record metadata")
	}
}
func TestBucketNameAndTTLRemainWireCompatible(t *testing.T) {
	if bucketName("bot-alpha") != "clawsynapse-transfer-bot-alpha" {
		t.Fatal("wire bucket changed")
	}
	if parseTTL("") != 24*time.Hour || parseTTL("30m") != 30*time.Minute || parseTTL("invalid") != 24*time.Hour {
		t.Fatal("TTL defaults changed")
	}
}
func TestDisabledNetworkStillRecoversLocalReferences(t *testing.T) {
	root := t.TempDir()
	s := testService(t, root, "A", testBlobStore(t, root, blob.Limits{}), 100)
	info := importTestFile(t, s, "local")
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if readTransfer(t, s, info.TransferID) != "local" {
		t.Fatal("disabled network blocked durable reads")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedOutboundPublicationRecoversAsUncertain(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	s := testService(t, root, "A", blobs, 100)
	rec := &TransferRecord{TransferID: "outbound-crashed", Direction: "outbound", Status: "pending", FileName: "data.txt"}
	if err := s.commit(rec, strings.NewReader("payload"), blob.WriteOptions{ExpectedSize: 7}); err != nil {
		t.Fatal(err)
	}
	restarted := testService(t, root, "A", testBlobStore(t, root, blob.Limits{}), 100)
	info, ok := restarted.GetTransfer(rec.TransferID)
	if !ok || info.Status != "uncertain" {
		t.Fatal("interrupted publication did not retain uncertain outcome")
	}
	if readTransfer(t, restarted, rec.TransferID) != "payload" {
		t.Fatal("interrupted outbound snapshot lost")
	}
}

func TestLegacyDuplicateTransferPrefixDoesNotLoseAnEntity(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	legacy := t.TempDir()
	prefix := newTransferID()
	for name, body := range map[string]string{prefix + "-first": "first", prefix + "-second": "second"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	createTestBot(t, root, "A")
	if err := ImportLegacy(legacy, "A", blobs, 100, 100); err != nil {
		t.Fatal(err)
	}
	s := testService(t, root, "A", blobs, 100)
	if len(s.ListTransfers()) != 2 {
		t.Fatal("duplicate old transfer prefix lost an entity")
	}
}

func TestPendingReceiveStorageFailureDoesNotExposeEntityPath(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	s := testService(t, root, "A", blobs, 100)
	rec := &TransferRecord{TransferID: "pending", Direction: "inbound", Status: "completed", NotificationState: "pending"}
	if err := s.commit(rec, strings.NewReader("payload"), blob.WriteOptions{ExpectedSize: 7}); err != nil {
		t.Fatal(err)
	}
	ref, err := blobs.Get("A", rec.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "blobs", "sha256", ref.Digest[:2])
	if err := os.Remove(filepath.Join(prefix, ref.Digest)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(prefix); err != nil {
		t.Fatal(err)
	}
	s.OnReceived(func(TransferRecord) { t.Error("missing content reached Harness") })
	if err := s.Start(context.Background()); err == nil || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "blobs") {
		t.Fatalf("missing content did not fail safely: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizedReaderErrorsDoNotExposeEntityPath(t *testing.T) {
	root := t.TempDir()
	s := testService(t, root, "A", testBlobStore(t, root, blob.Limits{}), 100)
	info := importTestFile(t, s, "private")
	reader, err := s.OpenFile(info.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "blobs") {
		t.Fatalf("reader error exposed a shared path: %v", err)
	}
}
