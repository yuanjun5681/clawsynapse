package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/config"
)

func newProtectedServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer("127.0.0.1:0", nil, nil, nil, nil, nil, nil, nil, "default", SelfInfo{}, "test", config.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAllAPIRoutesRequireBearer(t *testing.T) {
	s := newProtectedServer(t)
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/peers"}, {"POST", "/v1/auth/challenge"}, {"POST", "/v1/trust/request"},
		{"POST", "/v1/trust/approve"}, {"POST", "/v1/trust/reject"}, {"POST", "/v1/trust/revoke"},
		{"GET", "/v1/trust/pending"}, {"POST", "/v1/publish"}, {"GET", "/v1/messages"},
		{"POST", "/v1/transfer/send"}, {"GET", "/v1/transfers"}, {"GET", "/v1/transfer/id"},
		{"DELETE", "/v1/transfer/id"}, {"GET", "/v1/health"}, {"GET", "/v1/config"}, {"PUT", "/v1/config"},
	} {
		for _, authorization := range []string{"", "Bearer wrong", "Basic wrong"} {
			r := httptest.NewRequest(route.method, route.path, nil)
			r.Header.Set("Authorization", authorization)
			w := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without valid bearer: %d", route.method, route.path, w.Code)
			}
		}
	}
}

func TestTokenPersistsPrivatelyAndClientAuthenticates(t *testing.T) {
	s := newProtectedServer(t)
	path := filepath.Join(s.cfg.DataDir, "api.token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token permissions: %v", info.Mode())
	}
	token, err := readToken(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ensureToken(path)
	if err != nil || again != token {
		t.Fatal("token was not persisted")
	}
	httpServer := httptest.NewServer(s.httpServer.Handler)
	defer httpServer.Close()
	client := NewClient(httpServer.URL, time.Second, WithTokenFile(path))
	result, err := client.Get(context.Background(), "/v1/config")
	if err != nil || !result.OK {
		t.Fatalf("authenticated client failed: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("token leaked through API config")
	}
	missing := NewClient(httpServer.URL, time.Second, WithTokenFile(filepath.Join(t.TempDir(), "missing")))
	if _, err := missing.Get(context.Background(), "/v1/config"); err == nil {
		t.Fatal("missing token silently allowed")
	}
	remote := NewClient("http://192.0.2.1:18080", time.Second, WithTokenFile(path))
	if _, err := remote.Get(context.Background(), "/v1/config"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal("client did not reject plaintext remote transport")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureToken(path); err == nil {
		t.Fatal("insecure token permissions accepted")
	}
}

func TestPublishRejectsUnsafeJSONBeforeExecution(t *testing.T) {
	s := newProtectedServer(t)
	token, err := readToken(s.cfg.APITokenPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"targetNode":"peer","message":"hello","unexpected":true}`,
		`{"targetNode":"peer","message":"hello"} {}`,
		`{"targetNode":"one","targetNode":"two","message":"hello"}`,
		`null`, `[]`,
		`{"targetNode":"peer","message":"hello","payload":{"text":"other"}}`,
		`{"message":"` + strings.Repeat("x", 256*1024) + `"}`,
	} {
		r := httptest.NewRequest("POST", "/v1/publish", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unsafe JSON status: %d", w.Code)
		}
	}
}

func TestServerRejectsUnsafeRemoteListener(t *testing.T) {
	for _, cfg := range []config.Config{
		{DataDir: t.TempDir()},
		{DataDir: t.TempDir(), AllowRemoteAPI: true},
		{DataDir: t.TempDir(), AllowRemoteAPI: true, APITLSCertFile: "missing", APITLSKeyFile: "missing"},
	} {
		if _, err := NewServer("0.0.0.0:8080", nil, nil, nil, nil, nil, nil, nil, "default", SelfInfo{}, "test", cfg); err == nil {
			t.Fatal("unsafe remote listener accepted")
		}
	}
}
