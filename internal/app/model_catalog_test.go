package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/config"
)

func TestHarnessModelsEndpointContextAndAuthorization(t *testing.T) {
	a := testDaemon(t)
	var queryDir string
	a.manager.modelQuery = func(ctx context.Context, product, dir string) (adapter.ModelCatalog, error) {
		if ctx.Err() != nil || product != "opencode" {
			t.Fatalf("wrong query context: %s, %v", product, ctx.Err())
		}
		queryDir = dir
		if filepath.Dir(dir) != filepath.Join(a.manager.cfg.DataDir, "bots") {
			t.Fatalf("query left private Bot root: %q", dir)
		}
		stat, err := os.Stat(dir)
		if err != nil || stat.Mode().Perm() != 0700 {
			t.Fatalf("query workspace is not private: %v, %v", stat, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("query workspace is not empty: %v, %v", entries, err)
		}
		return adapter.ModelCatalog{Adapter: product, Models: []adapter.ModelInfo{{ID: "provider/model", DisplayName: "Native model"}}}, nil
	}
	w := request(t, a, "GET", "/v2/harnesses/opencode/models?workspaceId=desktop_1", "", nil)
	requireStatus(t, w, http.StatusOK)
	var result struct {
		Code string `json:"code"`
		Data struct {
			Catalog adapter.ModelCatalog `json:"catalog"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "harness.models" || result.Data.Catalog.Adapter != "opencode" || result.Data.Catalog.DefaultKnown || result.Data.Catalog.DefaultModel != "" || !result.Data.Catalog.Contains("provider/model") {
		t.Fatalf("unexpected catalog response: %s", w.Body.String())
	}
	if _, err := os.Stat(queryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("query workspace survived: %v", err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("catalog was cacheable")
	}
	health := request(t, a, "GET", "/v2/health", "", nil)
	if !strings.Contains(health.Body.String(), "harness_models") {
		t.Fatal("catalog capability was not advertised")
	}
	botID := newTestBot(t, a, "credential owner")
	requireStatus(t, request(t, a, "GET", "/v2/harnesses/opencode/models", botToken(t, a, botID), nil), http.StatusForbidden)
	requireStatus(t, request(t, a, "GET", "/v2/harnesses/opencode/models?workspaceId=..%2Fproject", "", nil), http.StatusBadRequest)
	requireStatus(t, request(t, a, "GET", "/v2/harnesses/opencode/models?botId="+botID, "", nil), http.StatusBadRequest)
}

func TestExplicitModelsValidatedBeforePersistenceAndOnlyWhenChanged(t *testing.T) {
	a := testDaemon(t)
	queries := 0
	a.manager.modelQuery = func(context.Context, string, string) (adapter.ModelCatalog, error) {
		queries++
		return adapter.ModelCatalog{Adapter: "opencode", Models: []adapter.ModelInfo{{ID: "p/allowed", DisplayName: "Allowed"}}}, nil
	}
	cfg := config.DefaultBotConfig()
	cfg.DisplayName = "native model"
	cfg.Harness.Adapter = "opencode"
	cfg.Harness.Model = "p/not-available"
	requireStatus(t, request(t, a, "POST", "/v2/bots", "", cfg), http.StatusBadRequest)
	if len(a.manager.List()) != 0 {
		t.Fatal("invalid explicit model persisted a Bot")
	}
	entries, err := os.ReadDir(filepath.Join(a.manager.cfg.DataDir, "bots"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid explicit model left Bot files: %v, %v", entries, err)
	}
	cfg.Harness.Model = "p/allowed"
	bot, err := a.manager.Create(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 2 {
		t.Fatalf("queries = %d, want 2", queries)
	}
	cfg, _ = a.manager.Config(bot.BotID)
	cfg.DisplayName = "renamed while engine unavailable"
	a.manager.modelQuery = func(context.Context, string, string) (adapter.ModelCatalog, error) {
		queries++
		return adapter.ModelCatalog{}, &adapter.CatalogError{Code: "harness.models_unavailable", Message: "native Harness executable could not be started in the daemon environment"}
	}
	if _, err = a.manager.Update(bot.BotID, cfg); err != nil {
		t.Fatalf("non-model update queried unavailable engine: %v", err)
	}
	if queries != 2 {
		t.Fatal("unchanged explicit model queried engine")
	}
	cfg, _ = a.manager.Config(bot.BotID)
	before := cfg.Revision
	cfg.Harness.Model = "p/new"
	w := request(t, a, "PUT", "/v2/bots/"+bot.BotID+"/config", "", cfg)
	requireStatus(t, w, http.StatusServiceUnavailable)
	persisted, _ := a.manager.Config(bot.BotID)
	if persisted.Revision != before || persisted.Harness.Model != "p/allowed" {
		t.Fatal("failed model validation changed persisted configuration")
	}
	cfg.Harness.Model = ""
	if _, err = a.manager.Update(bot.BotID, cfg); err != nil {
		t.Fatalf("clearing explicit model queried unavailable engine: %v", err)
	}
	if queries != 3 {
		t.Fatalf("queries = %d, want 3", queries)
	}
}

func TestCatalogNativeErrorsStaySafe(t *testing.T) {
	a := testDaemon(t)
	a.manager.modelQuery = func(context.Context, string, string) (adapter.ModelCatalog, error) {
		return adapter.ModelCatalog{}, errors.New("private credential-bearing native output")
	}
	w := request(t, a, "GET", "/v2/harnesses/pi/models", "", nil)
	requireStatus(t, w, http.StatusBadGateway)
	if strings.Contains(w.Body.String(), "credential") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("raw native error leaked: %s", w.Body.String())
	}
}
