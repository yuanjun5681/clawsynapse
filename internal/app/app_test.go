package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/config"
	"clawsynapse/pkg/types"
)

func testDaemon(t *testing.T) *App {
	t.Helper()
	d := config.DefaultDaemonConfig()
	d.DataDir = t.TempDir()
	d.AdminTokenPath = filepath.Join(d.DataDir, "admin.token")
	d.LocalAPIAddr = "127.0.0.1:0"
	d.NATSServers = []string{"nats://127.0.0.1:1"}
	a, err := NewDaemon(d, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.manager.Shutdown(ctx)
		_ = a.lock.Close()
	})
	return a
}
func newTestBot(t *testing.T, a *App, name string) string {
	t.Helper()
	cfg := config.DefaultBotConfig()
	cfg.DisplayName = name
	b, err := a.manager.Create(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b.BotID
}
func request(t *testing.T, a *App, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.api.Handler().ServeHTTP(w, r)
	return w
}
func adminToken(t *testing.T, a *App) string {
	t.Helper()
	token, err := loadPrivateToken(a.manager.cfg.AdminTokenPath)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func botToken(t *testing.T, a *App, id string) string {
	t.Helper()
	token, err := loadPrivateToken(filepath.Join(a.manager.botDir(id), "credentials", "api.token"))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
}

func TestBotCreationIdentityAndCredentialIsolation(t *testing.T) {
	a := testDaemon(t)
	first := newTestBot(t, a, "duplicate name")
	second := newTestBot(t, a, "duplicate name")
	if first == second {
		t.Fatal("new Bot reused identity")
	}
	one, _ := a.manager.Info(first)
	two, _ := a.manager.Info(second)
	if one.DID == two.DID || one.DID == "" {
		t.Fatal("distinct keys did not produce distinct DID")
	}
	token := botToken(t, a, first)
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+first, token, nil), 200)
	for _, p := range []string{"/v2/bots", "/v2/health", "/v2/bots/" + second, "/v2/bots/" + second + "/messages", "/v2/bots/" + first + "/config"} {
		requireStatus(t, request(t, a, "GET", p, token, nil), 403)
	}
	requireStatus(t, request(t, a, "POST", "/v2/bots/"+second+"/publish", token, map[string]any{"targetBot": first, "message": "forged"}), 403)
	requireStatus(t, request(t, a, "POST", "/v2/bots/"+first+"/start", token, nil), 403)
	requireStatus(t, request(t, a, "POST", "/v2/bots/"+first+"/transfer/import", token, map[string]string{"filePath": "/etc/passwd"}), 403)
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+first+"/messages", token, nil), 200)
	requireStatus(t, request(t, a, "POST", "/v2/bots/"+first+"/publish", token, map[string]string{"targetBot": second, "message": "stopped"}), 409)
	admin := adminToken(t, a)
	requireStatus(t, request(t, a, "GET", "/v2/bots/n1-missing", admin, nil), 404)
	requireStatus(t, request(t, a, "GET", "/v1/peers", admin, nil), 404)
	requireStatus(t, request(t, a, "POST", "/v2/publish", admin, map[string]string{"targetBot": second, "message": "unselected"}), 404)
	if _, err := NewDaemon(a.manager.cfg, "test"); err == nil {
		t.Fatal("second daemon acquired same root")
	}
}

func TestConfigCASBindingChangeAndArchiveKeepIdentity(t *testing.T) {
	a := testDaemon(t)
	id := newTestBot(t, a, "before")
	before, _ := a.manager.Info(id)
	cfg, _ := a.manager.Config(id)
	stale := cfg
	cfg.DisplayName = "after"
	renamed, err := a.manager.Update(id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.BotID != id || renamed.DID != before.DID || renamed.BindingRevision != before.BindingRevision {
		t.Fatal("name change mutated identity or binding")
	}
	if _, err = a.manager.Update(id, stale); err == nil {
		t.Fatal("stale configuration revision accepted")
	}
	cfg, _ = a.manager.Config(id)
	cfg.Harness.Adapter = "opencode"
	cfg.Harness.Model = "opencode/glm-5.3-flash-free"
	rebound, err := a.manager.Update(id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rebound.BindingRevision == before.BindingRevision || rebound.DID != before.DID {
		t.Fatal("binding change failed to isolate sessions or changed identity")
	}
	cfg, _ = a.manager.Config(id)
	cfg.Harness.Adapter = "default"
	cfg.Harness.Model = ""
	back, err := a.manager.Update(id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if back.BindingRevision == before.BindingRevision || back.BindingRevision == rebound.BindingRevision {
		t.Fatal("switching back revived old session scope")
	}
	archived, err := a.manager.Transition(id, "archive")
	if err != nil || archived.State != "archived" {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if _, err = a.manager.Transition(id, "start"); err == nil {
		t.Fatal("archived Bot started")
	}
	restored, err := a.manager.Transition(id, "restore")
	if err != nil || restored.State != "stopped" || restored.BotID != id || restored.DID != before.DID {
		t.Fatalf("restore changed identity/state: %+v %v", restored, err)
	}
	data, err := os.ReadFile(filepath.Join(a.manager.cfg.DataDir, "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "displayName") || strings.Contains(string(data), "harness") {
		t.Fatal("catalog duplicated configuration truth")
	}
}

func TestCorruptBotFailsWithoutReplacingIdentityOrOtherBotState(t *testing.T) {
	a := testDaemon(t)
	bad := newTestBot(t, a, "bad")
	good := newTestBot(t, a, "good")
	keyPath := filepath.Join(a.manager.botDir(bad), "credentials", "identity.key")
	original, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.manager.botDir(bad), "trust.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	accepted, err := a.manager.Transition(bad, "start")
	if err != nil || accepted.State != "starting" {
		t.Fatalf("start not accepted: %+v %v", accepted, err)
	}
	entry, _ := a.manager.entry(bad)
	entry.transition.Wait()
	failed, _ := a.manager.Info(bad)
	if failed.State != "failed" || !strings.Contains(failed.Error, "trust") {
		t.Fatalf("failure not explicit: %+v", failed)
	}
	current, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(original, current) {
		t.Fatal("failed start replaced identity")
	}
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+good+"/messages", botToken(t, a, good), nil), 200)
	requireStatus(t, request(t, a, "GET", "/v2/bots", adminToken(t, a), nil), 200)
	other, _ := a.manager.Info(good)
	if other.State != "stopped" {
		t.Fatal("another Bot state changed")
	}
}
func TestManagerReturnsIndependentConfiguration(t *testing.T) {
	a := testDaemon(t)
	cfg := config.DefaultBotConfig()
	cfg.DisplayName = "copy"
	cfg.Labels = types.Labels{"env": types.LabelString("original")}
	cfg.Harness.AllowedAgentIDs = []string{"allowed"}
	bot, err := a.manager.Create(cfg)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := a.manager.Config(bot.BotID)
	view.Labels["env"] = types.LabelString("mutated")
	view.Harness.AllowedAgentIDs[0] = "forged"
	stored, _ := a.manager.Config(bot.BotID)
	if !stored.Labels["env"].MatchExact("original") || stored.Harness.AllowedAgentIDs[0] != "allowed" {
		t.Fatal("configuration view mutated server authorization")
	}
}

func TestManagementCreateRequiresRoleAndApplicableFields(t *testing.T) {
	a := testDaemon(t)
	admin := adminToken(t, a)
	for _, body := range []map[string]any{
		{"displayName": "missing"},
		{"displayName": "empty", "role": ""},
		{"displayName": "null", "role": nil},
		{"displayName": "device", "role": "device"},
		{"displayName": "roles", "roles": []string{"console"}},
		{"displayName": "null harness", "role": "console", "harness": nil},
		{"displayName": "empty harness", "role": "console", "harness": map[string]any{}},
		{"displayName": "budget", "role": "console", "maxConcurrent": 0},
		{"displayName": "queue", "role": "console", "maxQueued": 0},
		{"displayName": "no harness", "role": "agent"},
		{"displayName": "no adapter", "role": "agent", "harness": map[string]any{}},
		{"displayName": "legacy", "role": "agent", "harness": map[string]any{"adapter": "console"}},
	} {
		requireStatus(t, request(t, a, "POST", "/v2/bots", admin, body), 400)
	}
	if len(a.manager.List()) != 0 {
		t.Fatal("invalid creation allocated a Bot identity")
	}
	for _, body := range []map[string]any{
		{"displayName": "human", "role": "console"},
		{"displayName": "executor", "role": "agent", "harness": map[string]any{"adapter": "default"}},
	} {
		response := request(t, a, "POST", "/v2/bots", admin, body)
		requireStatus(t, response, 201)
		var result types.APIResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		bot := result.Data["bot"].(map[string]any)
		if bot["role"] != body["role"] {
			t.Fatalf("management omitted Role: %#v", bot)
		}
		id := bot["botId"].(string)
		// Defaults for new Bots must not become an implicit update CAS token.
		requireStatus(t, request(t, a, "PUT", "/v2/bots/"+id+"/config", admin, body), 400)
		health := request(t, a, "GET", "/v2/bots/"+id+"/health", botToken(t, a, id), nil)
		requireStatus(t, health, 200)
		if err := json.Unmarshal(health.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data["self"].(map[string]any)["role"] != body["role"] {
			t.Fatal("scoped self metadata omitted Role")
		}
		if result.Data["adapter"].(map[string]any)["applicable"] != (body["role"] == "agent") {
			t.Fatal("Harness applicability did not follow Role")
		}
	}
}

func TestConfigurationRoleIsImmutableInBothDirections(t *testing.T) {
	for _, role := range []string{config.RoleConsole, config.RoleAgent} {
		t.Run(role, func(t *testing.T) {
			a := testDaemon(t)
			cfg := config.DefaultBotConfigForRole(role)
			cfg.DisplayName = role
			before, err := a.manager.Create(cfg)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := a.manager.Config(before.BotID)
			changed := config.DefaultBotConfigForRole(config.RoleConsole)
			if role == config.RoleConsole {
				changed = config.DefaultBotConfigForRole(config.RoleAgent)
			}
			changed.Revision = original.Revision
			changed.DisplayName = "attempted role switch"
			if _, err := a.manager.Update(before.BotID, changed); err == nil {
				t.Fatal("complete valid replacement changed Role")
			}
			after, _ := a.manager.Info(before.BotID)
			if after != before {
				t.Fatal("failed Role update changed identity, revision or Binding")
			}
			stored, _ := a.manager.Config(before.BotID)
			if stored.Role != original.Role || stored.Revision != original.Revision || stored.BindingRevision != original.BindingRevision {
				t.Fatal("failed Role update changed persistent configuration")
			}
		})
	}
}

func TestStartupRejectsLegacyCatalogAndMixedBotFormat(t *testing.T) {
	for _, legacyCatalog := range []bool{true, false} {
		a := testDaemon(t)
		id := newTestBot(t, a, "legacy")
		if err := a.manager.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if legacyCatalog {
			path := filepath.Join(a.manager.cfg.DataDir, "bots.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.Replace(raw, []byte(`"schemaVersion": 3`), []byte(`"schemaVersion": 2`), 1)
			raw = bytes.Replace(raw, []byte(`"schemaVersion":3`), []byte(`"schemaVersion":2`), 1)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			path := filepath.Join(a.manager.botDir(id), "config.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, "role")
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := newManager(a.manager.cfg, "test", nil); err == nil {
			t.Fatal("normal startup accepted a legacy or mixed layout")
		}
	}
}

func TestConsoleFileRecordsAndAuthorizedBytesRemainReadableAcrossRestart(t *testing.T) {
	a := testDaemon(t)
	console, token, err := a.manager.BindWorkspace("files-desktop", "Human")
	if err != nil {
		t.Fatal(err)
	}
	other := newTestBot(t, a, "other")
	source := filepath.Join(t.TempDir(), "received.txt")
	const content = "durable human-readable bytes"
	if err := os.WriteFile(source, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	// Seed an authorized durable file using the same admin import operation.
	// No Harness or network runtime is necessary to exercise stopped-state reads.
	entry, _ := a.manager.entry(console.BotID)
	entry.mu.Lock()
	handler, close, err := a.manager.offline(entry)
	if err != nil {
		entry.mu.Unlock()
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"filePath": source})
	imported := httptest.NewRecorder()
	handler.ServeHTTP(imported, httptest.NewRequest("POST", "/transfer/import", bytes.NewReader(body)))
	close()
	entry.mu.Unlock()
	requireStatus(t, imported, 201)
	var result types.APIResult
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	transferID := result.Data["transfer"].(map[string]any)["transferId"].(string)
	listing := request(t, a, "GET", "/v2/bots/"+console.BotID+"/transfers", token, nil)
	requireStatus(t, listing, 200)
	if !strings.Contains(listing.Body.String(), transferID) || strings.Contains(listing.Body.String(), source) {
		t.Fatal("Console file record missing or disclosed a local storage path")
	}
	filePath := "/v2/bots/" + console.BotID + "/transfer/" + transferID + "/file"
	file := request(t, a, "GET", filePath, token, nil)
	requireStatus(t, file, 200)
	if file.Body.String() != content {
		t.Fatal("Console cannot read authorized file bytes")
	}
	requireStatus(t, request(t, a, "GET", filePath, botToken(t, a, other), nil), 403)
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+other+"/transfer/"+transferID+"/file", botToken(t, a, other), nil), 404)
	if err := a.manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := newManager(a.manager.cfg, "test", a.blobs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Shutdown(context.Background()) })
	handler, release, err := restored.Acquire(console.BotID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	file = httptest.NewRecorder()
	handler.ServeHTTP(file, httptest.NewRequest("GET", "/transfer/"+transferID+"/file", nil))
	requireStatus(t, file, 200)
	if file.Body.String() != content {
		t.Fatal("Console file bytes lost after reopen")
	}
}
