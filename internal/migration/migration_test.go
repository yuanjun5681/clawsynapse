package migration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/store"
)

func legacyFixture(t *testing.T) (config.Config, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "legacy")
	if err := store.EnsurePrivateDir(root); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	private := filepath.Join(external, "custom.key")
	public := filepath.Join(external, "custom.pub")
	if _, err := identity.Create(private, public); err != nil {
		t.Fatal(err)
	}
	transfers := filepath.Join(external, "received")
	if err := store.EnsurePrivateDir(transfers); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transfers, "unassociated-file.bin"), []byte("same immutable content"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(root, "config.yaml")
	body := "dataDir: " + root + "\nidentityKeyPath: " + private + "\nidentityPubPath: " + public + "\ntransferDir: " + transfers + "\nagentAdapter: opencode\ndisplayName: imported-bot\n"
	if err := os.WriteFile(legacyPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadLegacy(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	fs := store.NewFSStore(root)
	if err := fs.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := fs.SaveSessionState(store.SessionState{SchemaVersion: 1, Adapter: "opencode", SessionKey: "legacy-key", SessionID: "engine-session", CWD: filepath.Join(root, "peers")}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsurePrivateDir(filepath.Join(root, "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "messages.jsonl"), []byte("{\"published\":\"historical-message\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "unknown.dat"), []byte("unknown bytes retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.APITokenPath, []byte("legacy-admin-must-not-upgrade"), 0o600); err != nil {
		t.Fatal(err)
	}
	return c, root
}

func TestMigrationPreservesIdentityCustomPathsAndState(t *testing.T) {
	c, source := legacyFixture(t)
	target := filepath.Join(t.TempDir(), "daemon")
	oldID, err := identity.Load(c.IdentityKeyPath, c.IdentityPubPath)
	if err != nil {
		t.Fatal(err)
	}
	botID, err := Migrate(c, target)
	if err != nil {
		t.Fatal(err)
	}
	botRoot := filepath.Join(target, "bots", botID)
	loaded, err := identity.Load(filepath.Join(botRoot, "credentials", "identity.key"), filepath.Join(botRoot, "credentials", "identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldID.PrivateKey, loaded.PrivateKey) {
		t.Fatal("migration changed identity")
	}
	for _, path := range []string{filepath.Join(source, "unknown.dat"), filepath.Join(target, "legacy-backup", "data", "unknown.dat"), filepath.Join(botRoot, "unknown.dat"), filepath.Join(botRoot, "workspace", "legacy-transfers", "unassociated-file.bin"), filepath.Join(botRoot, "state", "messages.jsonl")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(botRoot, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b config.BotConfig
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	fs := store.NewFSStore(botRoot)
	fs.SessionDir = filepath.Join(botRoot, "sessions", b.BindingRevision)
	state, found, err := fs.LoadSessionState("opencode", migrateSessionKey(botID, b.BindingRevision, "opencode", "legacy-key"))
	if err != nil || !found {
		t.Fatalf("session missing: found=%v err=%v", found, err)
	}
	if state.SessionID != "engine-session" || state.CWD != filepath.Join(botRoot, "peers") {
		t.Fatalf("session not preserved: %#v", state)
	}
	token, err := os.ReadFile(filepath.Join(target, "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(token) == "legacy-admin-must-not-upgrade" {
		t.Fatal("old token gained administrator authority")
	}
	if _, err := config.LoadDaemon([]string{"--data-dir", target}); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(c, target); err == nil {
		t.Fatal("accepted nonempty target")
	}
}

func TestInPlaceMigrationRetainsOriginalBackup(t *testing.T) {
	c, root := legacyFixture(t)
	botID, err := Migrate(c, root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "migration.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m marker
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != "complete" || m.BotID != botID || m.OriginalBackup == "" {
		t.Fatalf("invalid commit marker: %#v", m)
	}
	if _, err := os.Stat(filepath.Join(m.OriginalBackup, "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadDaemon([]string{"--data-dir", root}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogReadAndRewritePreservesWorkspaceClaim(t *testing.T) {
	root := t.TempDir()
	data := []byte(`{"schemaVersion":3,"bots":[{"botId":"n1-0123456789abcdef0123456789abcdef","desiredState":"stopped","archived":false,"workspaceId":"desktop"}]}`)
	if err := os.WriteFile(filepath.Join(root, "bots.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := loadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Bots) != 1 || c.Bots[0].WorkspaceID != "desktop" {
		t.Fatal("migration catalog reader lost an existing workspace claim")
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"workspaceId":"desktop"`)) {
		t.Fatal("catalog rewrite would unbind the existing Console")
	}
}

func TestCompleteBotExportImportAndDuplicateConflict(t *testing.T) {
	c, _ := legacyFixture(t)
	source := filepath.Join(t.TempDir(), "source")
	botID, err := Migrate(c, source)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bot-export")
	if err := Export(source, botID, bundle); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	imported, err := Import(bundle, destination)
	if err != nil {
		t.Fatal(err)
	}
	if imported != botID {
		t.Fatal("import changed identity")
	}
	if _, err := Import(bundle, destination); err == nil {
		t.Fatal("duplicate identity import succeeded")
	}
	if _, err := os.Stat(filepath.Join(destination, "bots", botID, "workspace", "legacy-transfers", "unassociated-file.bin")); err != nil {
		t.Fatal(err)
	}
	// An integrity failure is rejected before any Bot is registered.
	if err := os.WriteFile(filepath.Join(bundle, "bots", botID, "unknown.dat"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(bundle, filepath.Join(t.TempDir(), "reject")); err == nil {
		t.Fatal("tampered archive imported")
	}
}
