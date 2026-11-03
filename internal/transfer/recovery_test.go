package transfer

import (
	"os"
	"path/filepath"
	"testing"

	"clawsynapse/internal/blob"
)

func createTestBot(t *testing.T, root, bot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "bots", bot, "transfers"), 0700); err != nil {
		t.Fatal(err)
	}
}
func TestLostTransferDirectoryFailsRecoveryWithoutRecreatingHistory(t *testing.T) {
	root := t.TempDir()
	blobs := testBlobStore(t, root, blob.Limits{})
	original := testService(t, root, "A", blobs, 100)
	importTestFile(t, original, "keep file")
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "bots", "A", "transfers")
	if err := os.Rename(dir, filepath.Join(root, "saved-references")); err != nil {
		t.Fatal(err)
	}
	recovered := NewService(nil, nil, nil, nil, "A", nil, "open", TransferConfig{BotID: "A", Blobs: blobs, TransferDir: dir, Workspace: filepath.Join(root, "bots", "A", "workspace"), QuotaBytes: 100, MaxFileSize: 1000})
	defer recovered.Close()
	if err := recovered.ConfigurePersistence(); err == nil {
		t.Fatal("damaged reference history was accepted as empty")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("recovery recreated missing references: %v", err)
	}
	if err := blobs.GC(); err == nil {
		t.Fatal("collection continued with damaged Bot reference history")
	}
}
