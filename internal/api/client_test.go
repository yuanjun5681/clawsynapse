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

	"clawsynapse/pkg/types"
)

func clientToken(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClientRequiresExplicitBotAndBindsRequests(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing bearer")
		}
		_ = json.NewEncoder(w).Encode(types.APIResult{OK: true})
	}))
	defer server.Close()
	token := clientToken(t)
	client := NewClient(server.URL, time.Second, WithTokenFile(token))
	if _, err := client.Get(context.Background(), "/peers"); err == nil {
		t.Fatal("implicit Bot accepted")
	}
	if _, err := client.Get(context.Background(), "/v2/bots"); err != nil {
		t.Fatal(err)
	}
	scoped := NewClient(server.URL, time.Second, WithTokenFile(token), WithBot("bot-a"))
	if _, err := scoped.Get(context.Background(), "/peers?name=hello"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v2/bots/bot-b/peers", "/v2/bots", "/v2/health", "/v1/peers", "/../peers", "https://other/peers"} {
		if _, err := scoped.Get(context.Background(), path); err == nil {
			t.Fatalf("accepted out-of-scope path %q", path)
		}
	}
	if len(paths) != 2 || paths[0] != "/v2/bots" || paths[1] != "/v2/bots/bot-a/peers?name=hello" {
		t.Fatalf("unexpected requests: %v", paths)
	}
}

func TestClientNeverBorrowsCredentialsForAnotherEndpoint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAWSYNAPSE_DATA_DIR", root)
	t.Setenv("CLAWSYNAPSE_LOCAL_API_ADDR", "127.0.0.1:18080")
	client := NewClient("127.0.0.1:18081", time.Second)
	_, err := client.Get(context.Background(), "/v2/health")
	if err == nil || !strings.Contains(err.Error(), "token-file") {
		t.Fatalf("expected endpoint-bound credential error: %v", err)
	}
}

func TestClientExplicitCredentialsDoNotLoadLegacyConfiguration(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAWSYNAPSE_DATA_DIR", root)
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"schemaVersion":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(types.APIResult{OK: true}) }))
	defer server.Close()
	client := NewClient(server.URL, time.Second, WithTokenFile(clientToken(t)))
	if _, err := client.Get(context.Background(), "/v2/health"); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsRedirectsAndRemotePlaintext(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v2/health", http.StatusFound)
	}))
	defer server.Close()
	token := clientToken(t)
	client := NewClient(server.URL, time.Second, WithTokenFile(token))
	if _, err := client.Get(context.Background(), "/v2/health"); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("credential redirected")
	}
	client = NewClient("http://192.0.2.1:18080", time.Second, WithTokenFile(token))
	if _, err := client.Get(context.Background(), "/v2/health"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("plaintext remote accepted: %v", err)
	}
}
