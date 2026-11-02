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
	return BotInfo{BotID: "n1-new"}, nil
}
func (securityManager) Update(id string, _ config.BotConfig) (BotInfo, error) {
	return BotInfo{BotID: id}, nil
}
func (securityManager) Transition(id string, _ string) (BotInfo, error) {
	return BotInfo{BotID: id}, nil
}
func (securityManager) Acquire(string, bool) (http.Handler, func(), error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		success(w, http.StatusOK, "business.ok", map[string]any{"path": r.URL.Path})
	}), func() {}, nil
}
func (securityManager) Authenticate(token string) (string, bool) {
	return "n1-a", token == strings.Repeat("b", 64)
}
func (securityManager) BindWorkspace(string, string) (BotInfo, string, error) {
	return BotInfo{BotID: "n1-a"}, strings.Repeat("b", 64), nil
}
func newProtectedServer(t *testing.T) *DaemonServer {
	t.Helper()
	cfg := config.DefaultDaemonConfig()
	cfg.DataDir = t.TempDir()
	cfg.LocalAPIAddr = "127.0.0.1:0"
	s, err := NewDaemonServer(cfg, securityManager{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func localTestRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	return r
}

func TestLocalAnonymousManagement(t *testing.T) {
	s := newProtectedServer(t)
	for _, route := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/v2/health", "", 200},
		{"GET", "/v2/bots", "", 200},
		{"POST", "/v2/bots", `{"role":"console","displayName":"Local Bot"}`, 201},
		{"GET", "/v2/bots/n1-a/config", "", 200},
		{"PUT", "/v2/bots/n1-a/config", `{"role":"console","displayName":"Renamed Bot","revision":1}`, 200},
		{"POST", "/v2/bots/n1-a/start", "", 202},
		{"POST", "/v2/bots/n1-a/stop", "", 202},
		{"POST", "/v2/bots/n1-a/archive", "", 200},
		{"POST", "/v2/bots/n1-a/restore", "", 200},
		{"POST", "/v2/workspaces/workspace-a/binding", `{"displayName":"Workspace"}`, 200},
	} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localTestRequest(route.method, route.path, route.body))
		if w.Code != route.status {
			t.Fatalf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, "admin.token")); !os.IsNotExist(err) {
		t.Fatalf("admin credential unexpectedly exists: %v", err)
	}
}

func TestExplicitInvalidCredentialsFail(t *testing.T) {
	s := newProtectedServer(t)
	for _, auth := range []string{"Bearer wrong", "Basic wrong", ""} {
		r := localTestRequest("GET", "/v2/health", "")
		r.Header["Authorization"] = []string{auth}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("invalid credential accepted: %d", w.Code)
		}
	}
}
func TestScopedCredentialCannotCrossBotOrManagement(t *testing.T) {
	s := newProtectedServer(t)
	for _, route := range []struct{ method, path string }{{"GET", "/v2/bots"}, {"GET", "/v2/health"}, {"POST", "/v2/workspaces/workspace-a/binding"}, {"POST", "/v2/bots"}, {"GET", "/v2/bots/n1-b/messages"}, {"POST", "/v2/bots/n1-b/publish"}, {"GET", "/v2/bots/n1-a/config"}, {"POST", "/v2/bots/n1-a/start"}, {"POST", "/v2/bots/n1-a/transfer/import"}} {
		r := localTestRequest(route.method, route.path, "")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	r := localTestRequest("GET", "/v2/bots/n1-a", "")
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"botId":"n1-a"`) {
		t.Fatalf("own identity denied: %s", w.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/v2/bots/n1-a/messages"},
		{"POST", "/v2/bots/n1-a/publish"},
	} {
		r := localTestRequest(route.method, route.path, `{}`)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("own business request denied: %s %s: %d", route.method, route.path, w.Code)
		}
	}
}
func TestClientAnonymousAndOptionalScopedCredentials(t *testing.T) {
	s := newProtectedServer(t)
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	result, err := client.Get(context.Background(), "/v2/health")
	if err != nil || !result.OK {
		t.Fatalf("anonymous management failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bot.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	scoped := NewClient(server.URL, time.Second, WithTokenFile(path))
	if _, err := scoped.Get(context.Background(), "/v2/bots/n1-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Get(context.Background(), "/v2/health"); err == nil {
		t.Fatal("scoped credential allowed daemon management")
	}
	missing := NewClient(server.URL, time.Second, WithTokenFile(filepath.Join(t.TempDir(), "missing")))
	if _, err := missing.Get(context.Background(), "/v2/health"); err == nil {
		t.Fatal("missing scoped credential accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readToken(path); err == nil {
		t.Fatal("insecure scoped credential accepted")
	}
}

func TestLocalBoundaryRejectsRemoteAndCrossSiteRequests(t *testing.T) {
	s := newProtectedServer(t)
	for _, test := range []struct{ peer, host, origin, site string }{
		{"192.0.2.1:1234", "127.0.0.1", "", ""},
		{"127.0.0.1:1234", "evil.example", "", ""},
		{"127.0.0.1:1234", "127.0.0.1", "https://evil.example", ""},
		{"127.0.0.1:1234", "localhost", "null", ""},
		{"127.0.0.1:1234", "localhost", "", "cross-site"},
	} {
		r := localTestRequest("POST", "/v2/bots", `{"role":"console","displayName":"Local Bot"}`)
		r.RemoteAddr, r.Host = test.peer, test.host
		r.Header.Set("Origin", test.origin)
		if test.origin == "" {
			r.Header.Del("Origin")
		}
		r.Header.Set("Sec-Fetch-Site", test.site)
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("X-Forwarded-Host", "localhost")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("boundary bypass %+v: %d", test, w.Code)
		}
	}
	for _, host := range []string{"localhost:18080", "127.0.0.1:18080", "[::1]:18080"} {
		r := localTestRequest("POST", "/v2/bots", `{"role":"console","displayName":"Local Bot"}`)
		r.Host = host
		r.RemoteAddr = "[::1]:12345"
		r.Header.Set("Origin", "http://"+host)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("local origin denied: %s: %d", host, w.Code)
		}
	}
}

func TestDaemonLimitsAnonymousRequestBodies(t *testing.T) {
	s := newProtectedServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localTestRequest("POST", "/v2/bots", `{"displayName":"`+strings.Repeat("x", maxRequestBytes)+`"}`))
	if w.Code != 400 {
		t.Fatalf("oversize body accepted: %d", w.Code)
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
	for _, cfg := range []config.DaemonConfig{{DataDir: t.TempDir()}, {DataDir: t.TempDir(), APITLSCertFile: "missing", APITLSKeyFile: "missing"}} {
		cfg.LocalAPIAddr = "0.0.0.0:8080"
		if _, err := NewDaemonServer(cfg, securityManager{}); err == nil {
			t.Fatal("unsafe listener accepted")
		}
	}
}
