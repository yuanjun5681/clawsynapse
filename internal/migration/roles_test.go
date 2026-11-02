package migration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/store"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := store.WriteJSONAtomic(path, value, 0600); err != nil {
		t.Fatal(err)
	}
}

func downgradeBot(t *testing.T, path, adapter string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, path), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "role")
	fields["harness"], _ = json.Marshal(config.HarnessBinding{Adapter: adapter, Timeout: "120s"})
	fields["maxConcurrent"] = json.RawMessage("2")
	fields["maxQueued"] = json.RawMessage("0")
	writeJSON(t, path, fields)
}

// Start with a real legacy migration so identity, session and file layouts match
// real installations; then explicitly reconstruct their version 2 config form.
func version2Fixture(t *testing.T) (string, string, string) {
	t.Helper()
	legacy, _ := legacyFixture(t)
	root := filepath.Join(t.TempDir(), "daemon")
	agentID, err := Migrate(legacy, root)
	if err != nil {
		t.Fatal(err)
	}
	downgradeBot(t, filepath.Join(root, "bots", agentID, "config.json"), "opencode")
	if _, err := config.LoadDaemon([]string{"--data-dir", root}); err != nil {
		t.Fatal(err)
	}
	var daemonFields map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, filepath.Join(root, "config.json")), &daemonFields); err != nil {
		t.Fatal(err)
	}
	daemonFields["schemaVersion"] = json.RawMessage("2")
	daemonFields["adminTokenPath"], _ = json.Marshal(filepath.Join(root, "admin.token"))
	daemonFields["allowRemoteAPI"] = json.RawMessage("false")
	writeJSON(t, filepath.Join(root, "config.json"), daemonFields)
	credentials := filepath.Join(t.TempDir(), "credentials")
	if err := store.EnsurePrivateDir(credentials); err != nil {
		t.Fatal(err)
	}
	id, err := identity.Create(filepath.Join(credentials, "identity.key"), filepath.Join(credentials, "identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	consoleID := identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey))
	botRoot := filepath.Join(root, "bots", consoleID)
	if err := copyTree(credentials, filepath.Join(botRoot, "credentials"), nil); err != nil {
		t.Fatal(err)
	}
	if err := newToken(filepath.Join(botRoot, "credentials", "api.token")); err != nil {
		t.Fatal(err)
	}
	if err := store.NewFSStore(botRoot).EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	b := config.DefaultBotConfig()
	b.BindingRevision = "original-console-binding"
	writeJSON(t, filepath.Join(botRoot, "config.json"), b)
	downgradeBot(t, filepath.Join(botRoot, "config.json"), "console")
	if err := store.EnsurePrivateDir(filepath.Join(botRoot, "workspace", b.BindingRevision)); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(botRoot, "workspace-binding.json"), map[string]string{"workspaceId": "desktop"})
	writeJSON(t, filepath.Join(root, "bots.json"), catalog{SchemaVersion: 2, Bots: []botEntry{
		{BotID: agentID, DesiredState: "stopped", Archived: true},
		{BotID: consoleID, DesiredState: "stopped", WorkspaceID: "desktop"},
	}})
	putRoleFile(t, root, agentID)
	putRoleFile(t, root, consoleID)
	return root, agentID, consoleID
}

func TestUpgradePreservesAllBotsBindingsSessionsAndPaths(t *testing.T) {
	root, agentID, consoleID := version2Fixture(t)
	agentRoot := filepath.Join(root, "bots", agentID)
	consoleRoot := filepath.Join(root, "bots", consoleID)
	key := mustRead(t, filepath.Join(agentRoot, "credentials", "identity.key"))
	credentials := mustRead(t, filepath.Join(consoleRoot, "credentials", "api.token"))
	journal := mustRead(t, filepath.Join(agentRoot, "state", "messages.jsonl"))
	var before map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, filepath.Join(agentRoot, "config.json")), &before); err != nil {
		t.Fatal(err)
	}
	if err := Upgrade(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckStartup(root); err != nil {
		t.Fatal(err)
	}
	var agent, console config.BotConfig
	if err := config.DecodeStrict(mustRead(t, filepath.Join(agentRoot, "config.json")), &agent); err != nil {
		t.Fatal(err)
	}
	if err := config.DecodeStrict(mustRead(t, filepath.Join(consoleRoot, "config.json")), &console); err != nil {
		t.Fatal(err)
	}
	var binding string
	if err := json.Unmarshal(before["bindingRevision"], &binding); err != nil {
		t.Fatal(err)
	}
	if agent.Role != config.RoleAgent || agent.BindingRevision != binding || agent.MaxQueued != 0 {
		t.Fatal("Agent execution context changed")
	}
	if console.Role != config.RoleConsole || console.Harness != nil || console.MaxConcurrent != 0 || console.BindingRevision != "original-console-binding" {
		t.Fatal("Console role or historical path metadata lost")
	}
	if !bytes.Equal(key, mustRead(t, filepath.Join(agentRoot, "credentials", "identity.key"))) || !bytes.Equal(credentials, mustRead(t, filepath.Join(consoleRoot, "credentials", "api.token"))) || !bytes.Equal(journal, mustRead(t, filepath.Join(agentRoot, "state", "messages.jsonl"))) {
		t.Fatal("identity, credential or journal changed")
	}
	fs := store.NewFSStore(agentRoot)
	fs.SessionDir = filepath.Join(agentRoot, "sessions", agent.BindingRevision)
	state, found, err := fs.LoadSessionState("opencode", migrateSessionKey(agentID, agent.BindingRevision, "opencode", "legacy-key"))
	if err != nil || !found || state.SessionID != "engine-session" || state.CWD != filepath.Join(agentRoot, "peers") {
		t.Fatalf("native Session lost: %#v %v", state, err)
	}
	cat, err := loadCatalog(root)
	if err != nil || len(cat.Bots) != 2 || !cat.Bots[0].Archived || cat.Bots[1].WorkspaceID != "desktop" {
		t.Fatalf("catalog metadata changed: %#v %v", cat, err)
	}
	after := mustRead(t, filepath.Join(agentRoot, "config.json"))
	if err := Upgrade(root); err != nil {
		t.Fatal(err)
	}
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, mustRead(t, filepath.Join(agentRoot, "config.json"))) {
		t.Fatal("idempotent migration rewrote Binding")
	}
	assertRoleFile(t, root, agentID)
	assertRoleFile(t, root, consoleID)
}

func TestUpgradeBlocksDamagedStoppedAndArchivedBots(t *testing.T) {
	for _, damage := range []string{"identity", "trust", "workspace", "reference", "adapter", "blob"} {
		t.Run(damage, func(t *testing.T) {
			root, agentID, consoleID := version2Fixture(t)
			botRoot := filepath.Join(root, "bots", agentID)
			switch damage {
			case "identity":
				if err := os.Remove(filepath.Join(botRoot, "credentials", "identity.key")); err != nil {
					t.Fatal(err)
				}
			case "trust":
				if err := os.Remove(filepath.Join(botRoot, "trust.json")); err != nil {
					t.Fatal(err)
				}
			case "workspace":
				writeJSON(t, filepath.Join(root, "bots", consoleID, "workspace-binding.json"), map[string]string{"workspaceId": "wrong"})
			case "reference":
				writeJSON(t, filepath.Join(botRoot, "sessions", "missing.json"), map[string]string{"cwd": filepath.Join(root, "missing-workspace")})
			case "adapter":
				downgradeBot(t, filepath.Join(botRoot, "config.json"), "unknown")
			case "blob":
				var ref blob.Reference
				if err := json.Unmarshal(mustRead(t, filepath.Join(botRoot, "transfers", "role-file.json")), &ref); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, "blobs", "sha256", ref.Digest[:2], ref.Digest)
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			original := mustRead(t, filepath.Join(root, "config.json"))
			if err := Upgrade(root); err == nil {
				t.Fatal("damaged layout published")
			}
			if !bytes.Equal(original, mustRead(t, filepath.Join(root, "config.json"))) {
				t.Fatal("source changed on validation failure")
			}
			if err := CheckStartup(root); err == nil {
				t.Fatal("unfinished transition did not block startup")
			}
			if err := Recover(root); err != nil {
				t.Fatal(err)
			}
			if err := Recover(root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRoleRecoveryAfterEachPublicationBoundary(t *testing.T) {
	for _, phase := range []string{"prepared", "backed-up", "published"} {
		t.Run(phase, func(t *testing.T) {
			root, agentID, _ := version2Fixture(t)
			if err := Upgrade(root); err != nil {
				t.Fatal(err)
			}
			var m roleTransition
			if err := decodeManifest(mustRead(t, roleMarker(root)), &m); err != nil {
				t.Fatal(err)
			}
			if phase != "published" {
				if err := os.Rename(root, m.Stage); err != nil {
					t.Fatal(err)
				}
				if phase == "prepared" {
					if err := os.Rename(m.Backup, root); err != nil {
						t.Fatal(err)
					}
				}
			}
			m.Status = phase
			writeJSON(t, roleMarker(root), m)
			if err := CheckStartup(root); err == nil {
				t.Fatal("interrupted publication allowed startup")
			}
			if err := Recover(root); err != nil {
				t.Fatal(err)
			}
			if err := Recover(root); err != nil {
				t.Fatal(err)
			}
			if err := CheckStartup(root); err != nil {
				t.Fatal(err)
			}
			var b config.BotConfig
			if err := config.DecodeStrict(mustRead(t, filepath.Join(root, "bots", agentID, "config.json")), &b); err != nil {
				t.Fatal(err)
			}
			if b.Role != config.RoleAgent {
				t.Fatal("recovered mixed layout")
			}
		})
	}
}

func TestExplicitVersion2ArchiveConversionPreservesOriginal(t *testing.T) {
	legacy, _ := legacyFixture(t)
	root := filepath.Join(t.TempDir(), "daemon")
	botID, err := Migrate(legacy, root)
	if err != nil {
		t.Fatal(err)
	}
	putRoleFile(t, root, botID)
	source := filepath.Join(t.TempDir(), "old-export")
	if err := Export(root, botID, source); err != nil {
		t.Fatal(err)
	}
	downgradeBot(t, filepath.Join(source, "bots", botID, "config.json"), "opencode")
	// Reconstruct the old export bug: duplicate content grants retained the
	// source path while the Bot-local grant already used the archive path.
	contentPath := filepath.Join(source, "content", "transfers", "role-file.json")
	var oldRef blob.Reference
	if err := json.Unmarshal(mustRead(t, contentPath), &oldRef); err != nil {
		t.Fatal(err)
	}
	oldRef.Data, _ = json.Marshal(map[string]string{"localPath": filepath.Join(root, "bots", botID, "workspace", "historical.txt"), "authorization": "retained"})
	writeJSON(t, contentPath, oldRef)
	var manifest archiveManifest
	if err := decodeManifest(mustRead(t, filepath.Join(source, "export.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion = 2
	manifest.Files, err = archiveInventory(source)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(source, "export.json"), manifest)
	original := mustRead(t, filepath.Join(source, "export.json"))
	oldConfig := mustRead(t, filepath.Join(source, "bots", botID, "config.json"))
	if _, err := Import(source, filepath.Join(t.TempDir(), "reject")); err == nil {
		t.Fatal("ordinary import accepted old archive")
	}
	converted := filepath.Join(t.TempDir(), "converted")
	if err := ConvertArchive(source, converted); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, mustRead(t, filepath.Join(source, "export.json"))) || !bytes.Equal(oldConfig, mustRead(t, filepath.Join(source, "bots", botID, "config.json"))) {
		t.Fatal("conversion modified original")
	}
	importRoot := filepath.Join(t.TempDir(), "imported")
	imported, err := Import(converted, importRoot)
	if err != nil || imported != botID {
		t.Fatalf("converted bundle failed import: %s %v", imported, err)
	}
	assertRoleFile(t, importRoot, botID)
	if err := ConvertArchive(converted, filepath.Join(t.TempDir(), "reject-current")); err == nil {
		t.Fatal("converter accepted non-v2 archive")
	}
}

func putRoleFile(t *testing.T, root, botID string) {
	t.Helper()
	path := filepath.Join(root, "bots", botID, "workspace", "historical.txt")
	if err := store.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("immutable bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := blob.New(root, blob.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"localPath": path, "authorization": "retained"})
	if _, err := s.Put(botID, "role-file", bytes.NewBufferString("immutable bytes"), blob.WriteOptions{ExpectedSize: 15, Data: data}); err != nil {
		t.Fatal(err)
	}
}

func assertRoleFile(t *testing.T, root, botID string) {
	t.Helper()
	s, err := blob.New(root, blob.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Open(botID, "role-file")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "immutable bytes" {
		t.Fatalf("file bytes lost: %s %v", data, err)
	}
	var ref blob.Reference
	if err := json.Unmarshal(mustRead(t, filepath.Join(root, "bots", botID, "transfers", "role-file.json")), &ref); err != nil {
		t.Fatal(err)
	}
	var record map[string]string
	if err := json.Unmarshal(ref.Data, &record); err != nil {
		t.Fatal(err)
	}
	if record["localPath"] != filepath.Join(root, "bots", botID, "workspace", "historical.txt") || record["authorization"] != "retained" {
		t.Fatal("file authorization or local path changed incorrectly")
	}
}

func TestOldConsoleValidationPrecedesRemovingExecutionFields(t *testing.T) {
	b := config.DefaultBotConfig()
	b.BindingRevision = "original-binding"
	path := filepath.Join(t.TempDir(), "config.json")
	writeJSON(t, path, b)
	downgradeBot(t, path, "console")
	original := mustRead(t, path)
	for _, field := range []string{"model", "instance", "webhookUrl", "credentialsFile", "allowedAgentIds"} {
		t.Run(field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(original, &fields); err != nil {
				t.Fatal(err)
			}
			var harness map[string]json.RawMessage
			if err := json.Unmarshal(fields["harness"], &harness); err != nil {
				t.Fatal(err)
			}
			value := json.RawMessage(`"invalid"`)
			if field == "allowedAgentIds" {
				value = json.RawMessage(`["invalid"]`)
			}
			harness[field] = value
			fields["harness"], _ = json.Marshal(harness)
			raw, _ := json.Marshal(fields)
			if _, err := convertOldBot(raw); err == nil {
				t.Fatal("invalid old Console fields silently discarded")
			}
		})
	}
}

func TestLegacyConsoleMigrationPublishesVersion3WithoutHarness(t *testing.T) {
	legacy, _ := legacyFixture(t)
	legacy.AgentAdapter = "console"
	root := filepath.Join(t.TempDir(), "daemon")
	botID, err := Migrate(legacy, root)
	if err != nil {
		t.Fatal(err)
	}
	var b config.BotConfig
	raw := mustRead(t, filepath.Join(root, "bots", botID, "config.json"))
	if err := config.DecodeStrict(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.Role != config.RoleConsole || b.Harness != nil || bytes.Contains(raw, []byte(`"maxConcurrent"`)) || bytes.Contains(raw, []byte(`"maxQueued"`)) {
		t.Fatal("legacy Console kept an execution Binding")
	}
	if _, _, err := readLayout(root, 3); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyRecoveryFinishesDurableCommitSidecar(t *testing.T) {
	legacy, _ := legacyFixture(t)
	root := filepath.Join(t.TempDir(), "daemon")
	if _, err := Migrate(legacy, root); err != nil {
		t.Fatal(err)
	}
	var m marker
	if err := decodeManifest(mustRead(t, root+".migration.json"), &m); err != nil {
		t.Fatal(err)
	}
	m.Status = "prepared"
	writeJSON(t, root+".migration.json", m)
	if err := CheckStartup(root); err == nil {
		t.Fatal("incomplete sidecar allowed startup")
	}
	if err := Recover(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckStartup(root); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeHonorsArchivedBotRuntimeLocks(t *testing.T) {
	root, agentID, _ := version2Fixture(t)
	lock, err := store.Lock(filepath.Join(root, "bots", agentID, "runtime.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := Upgrade(root); err == nil {
		t.Fatal("migration ignored an archived Bot runtime lock")
	}
	if _, err := os.Stat(roleMarker(root)); !os.IsNotExist(err) {
		t.Fatal("locked migration began staging")
	}
}

func TestUpgradeAcceptsPartialDaemonConfigWithNormalDefaults(t *testing.T) {
	root, agentID, consoleID := version2Fixture(t)
	writeJSON(t, filepath.Join(root, "config.json"), map[string]any{
		"schemaVersion":  2,
		"dataDir":        root,
		"localApiAddr":   "127.0.0.1:18081",
		"adminTokenPath": filepath.Join(root, "admin.token"),
		"natsServers":    []string{"nats://127.0.0.1:4222"},
		"maxConcurrent":  2,
		"logLevel":       "debug",
		"logFormat":      "json",
	})
	if err := Upgrade(root); err != nil {
		t.Fatalf("valid partial daemon configuration did not upgrade: %v", err)
	}
	d, err := config.LoadDaemon([]string{"--data-dir", root})
	if err != nil {
		t.Fatal(err)
	}
	if d.SchemaVersion != 3 || d.BlobMaxBytes != 10<<30 || d.TempMaxBytes != 1<<30 || d.WorkspaceMaxBytes != 10<<30 || d.LogRotateMaxSizeMB != 10 {
		t.Fatalf("omitted daemon options did not retain normal defaults: %#v", d)
	}
	if d.MaxConcurrent != 2 || d.LogLevel != "debug" || d.LocalAPIAddr != "127.0.0.1:18081" {
		t.Fatalf("explicit daemon options changed: %#v", d)
	}
	assertRoleFile(t, root, agentID)
	assertRoleFile(t, root, consoleID)
}

func TestUpgradeAcceptsCurrentDaemonWithoutAdministratorCredential(t *testing.T) {
	for _, keepToken := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-token", true: "retained-token"}[keepToken], func(t *testing.T) {
			legacy, _ := legacyFixture(t)
			root := filepath.Join(t.TempDir(), "daemon")
			botID, err := Migrate(legacy, root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "config.json")
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(mustRead(t, path), &fields); err != nil {
				t.Fatal(err)
			}
			tokenPath := filepath.Join(root, "admin.token")
			fields["adminTokenPath"], _ = json.Marshal(tokenPath)
			fields["allowRemoteAPI"] = json.RawMessage("true")
			fields["natsServers"] = json.RawMessage(`["tls://nats.example:4222"]`)
			fields["natsTLSServerName"] = json.RawMessage(`"nats.example"`)
			writeJSON(t, path, fields)
			original := mustRead(t, path)
			keyPath := filepath.Join(root, "bots", botID, "credentials", "identity.key")
			originalKey := mustRead(t, keyPath)
			oldSecret := []byte("retained obsolete secret")
			if keepToken {
				if err := os.WriteFile(tokenPath, oldSecret, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err := Upgrade(root); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, mustRead(t, path)) || !bytes.Equal(originalKey, mustRead(t, keyPath)) {
				t.Fatal("current schema validation changed configuration or identity")
			}
			d, _, err := readLayout(root, 3)
			if err != nil {
				t.Fatal(err)
			}
			if len(d.NATSServers) != 1 || d.NATSServers[0] != "tls://nats.example:4222" || d.NATSTLSServerName != "nats.example" {
				t.Fatal("old configuration lost NATS settings")
			}
			if keepToken {
				if !bytes.Equal(oldSecret, mustRead(t, tokenPath)) {
					t.Fatal("validation changed obsolete secret")
				}
			} else if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
				t.Fatalf("validation generated administrator credential: %v", err)
			}
		})
	}
}

func TestRoleUpgradeRejectsRemoteListenerWithoutChangingSource(t *testing.T) {
	root, _, _ := version2Fixture(t)
	path := filepath.Join(root, "config.json")
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, path), &fields); err != nil {
		t.Fatal(err)
	}
	fields["localApiAddr"] = json.RawMessage(`"192.0.2.1:18080"`)
	fields["allowRemoteAPI"] = json.RawMessage("true")
	fields["apiTLSCertFile"] = json.RawMessage(`"/private/api.pem"`)
	fields["apiTLSKeyFile"] = json.RawMessage(`"/private/api.key"`)
	writeJSON(t, path, fields)
	original := mustRead(t, path)
	if err := Upgrade(root); err == nil {
		t.Fatal("role migration accepted remote management")
	}
	if !bytes.Equal(original, mustRead(t, path)) {
		t.Fatal("rejected remote listener was silently replaced")
	}
}
