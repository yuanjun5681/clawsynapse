package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/config"
)

type securityManager struct{}

func (securityManager) List() []BotInfo { return []BotInfo{{BotID: "n1-a", State: "stopped"}} }
func (securityManager) Info(id string) (BotInfo, error) {
	if id != "n1-a" {
		return BotInfo{}, Failure(404, "bot.not_found", "unknown Bot")
	}
	return BotInfo{BotID: id, State: "stopped"}, nil
}
func (securityManager) Config(string) (config.BotConfig, error) {
	return config.DefaultBotConfig(), nil
}
func (securityManager) Create(config.BotConfig) (BotInfo, error) {
	panic("unauthorized management call")
}
func (securityManager) Update(string, config.BotConfig) (BotInfo, error) {
	panic("unauthorized configuration call")
}
func (securityManager) Transition(string, string) (BotInfo, error) {
	panic("unauthorized lifecycle call")
}
func (securityManager) Acquire(string, bool) (http.Handler, func(), error) {
	return nil, nil, Failure(409, "bot.must_start", "Bot must be running")
}
func (securityManager) Authenticate(token string) (string, bool) {
	return "n1-a", token == strings.Repeat("b", 64)
}
func newProtectedServer(t *testing.T) *DaemonServer {
	t.Helper()
	cfg := config.DefaultDaemonConfig()
	cfg.DataDir = t.TempDir()
	cfg.AdminTokenPath = filepath.Join(cfg.DataDir, "admin.token")
	cfg.LocalAPIAddr = "127.0.0.1:0"
	s, err := NewDaemonServer(cfg, securityManager{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestAllAPIRoutesRequireBearer(t *testing.T) {
	s := newProtectedServer(t)
	for _, route := range []struct{ method, path string }{{"GET", "/v2/health"}, {"GET", "/v2/bots"}, {"POST", "/v2/bots"}, {"GET", "/v2/bots/n1-a"}, {"PUT", "/v2/bots/n1-a/config"}, {"POST", "/v2/bots/n1-a/start"}, {"GET", "/v2/bots/n1-a/messages"}, {"POST", "/v2/bots/n1-a/publish"}, {"GET", "/v2/bots/n1-a/transfer/id/file"}} {
		for _, auth := range []string{"", "Bearer wrong", "Basic wrong"} {
			r := httptest.NewRequest(route.method, route.path, nil)
			r.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatalf("%s %s: %d", route.method, route.path, w.Code)
			}
		}
	}
}
func TestScopedCredentialCannotCrossBotOrManagement(t *testing.T) {
	s := newProtectedServer(t)
	for _, route := range []struct{ method, path string }{{"GET", "/v2/bots"}, {"GET", "/v2/health"}, {"POST", "/v2/bots"}, {"GET", "/v2/bots/n1-b/messages"}, {"POST", "/v2/bots/n1-b/publish"}, {"GET", "/v2/bots/n1-a/config"}, {"POST", "/v2/bots/n1-a/start"}, {"POST", "/v2/bots/n1-a/transfer/import"}} {
		r := httptest.NewRequest(route.method, route.path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/v2/bots/n1-a", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"botId":"n1-a"`) {
		t.Fatalf("own identity denied: %s", w.Body.String())
	}
}
func TestTokenPersistsPrivatelyAndClientAuthenticates(t *testing.T) {
	s := newProtectedServer(t)
	path := s.cfg.AdminTokenPath
	token, err := readToken(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ensureToken(path)
	if err != nil || again != token {
		t.Fatal("token was replaced")
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	client := NewClient(server.URL, time.Second, WithTokenFile(path))
	result, err := client.Get(context.Background(), "/v2/health")
	if err != nil || !result.OK {
		t.Fatalf("authentication failed: %v", err)
	}
	missing := NewClient(server.URL, time.Second, WithTokenFile(filepath.Join(t.TempDir(), "missing")))
	if _, err = missing.Get(context.Background(), "/v2/health"); err == nil {
		t.Fatal("missing credential accepted")
	}
	remote := NewClient("http://192.0.2.1:18080", time.Second, WithTokenFile(path))
	if _, err = remote.Get(context.Background(), "/v2/health"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal("plaintext remote transport accepted")
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = ensureToken(path); err == nil {
		t.Fatal("insecure token accepted")
	}
}
func TestPublishRejectsUnsafeJSONBeforeExecution(t *testing.T) {
	srv := &Server{}
	for _, body := range []string{`{"targetBot":"peer","message":"hello","unexpected":true}`, `{"targetBot":"peer","message":"hello"} {}`, `{"targetBot":"one","targetBot":"two","message":"hello"}`, `null`, `[]`, `{"targetBot":"peer","message":"hello","payload":{"text":"other"}}`, `{"targetNode":"peer","message":"old-field"}`, `{"targetBot":"peer","from":"forged","message":"hello"}`, `{"message":"` + strings.Repeat("x", 256*1024) + `"}`} {
		w := httptest.NewRecorder()
		srv.handlePublish(w, httptest.NewRequest("POST", "/publish", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("unsafe JSON status %d: %.100s", w.Code, body)
		}
	}
}
func TestServerRejectsUnsafeRemoteListener(t *testing.T) {
	for _, cfg := range []config.DaemonConfig{{DataDir: t.TempDir()}, {DataDir: t.TempDir(), AllowRemoteAPI: true}, {DataDir: t.TempDir(), AllowRemoteAPI: true, APITLSCertFile: "missing", APITLSKeyFile: "missing"}} {
		cfg.LocalAPIAddr = "0.0.0.0:8080"
		if _, err := NewDaemonServer(cfg, securityManager{}); err == nil {
			t.Fatal("unsafe listener accepted")
		}
	}
}
