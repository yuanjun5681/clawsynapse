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
