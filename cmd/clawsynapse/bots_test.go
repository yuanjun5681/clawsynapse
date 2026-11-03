package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/config"
	"clawsynapse/pkg/types"
)

func TestResolveBotRequiresExplicitSelectionEvenWithOneBot(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true, Data: map[string]any{"items": []any{map[string]any{"botId": "n1-abc"}}}}}
	if _, err := resolveBot(context.Background(), client, ""); err == nil {
		t.Fatal("implicitly selected sole Bot")
	}
	if client.endpoint != "" {
		t.Fatal("missing selection requested daemon")
	}
	id, err := resolveBot(context.Background(), client, "n1-ab")
	if err != nil || id != "n1-abc" {
		t.Fatalf("prefix resolution: %q %v", id, err)
	}
	client.result.Data["items"] = []any{map[string]any{"botId": "n1-abc"}, map[string]any{"botId": "n1-abd"}}
	if _, err := resolveBot(context.Background(), client, "n1-ab"); err == nil {
		t.Fatal("ambiguous prefix accepted")
	}
	if _, err := resolveBot(context.Background(), client, "missing"); err == nil {
		t.Fatal("unknown prefix accepted")
	}
}

func TestFullBotIDDoesNotRequireCatalogAccess(t *testing.T) {
	client := &stubAPIClient{}
	id := "n1-" + strings.Repeat("a", 32)
	got, err := resolveBot(context.Background(), client, id)
	if err != nil || got != id || client.endpoint != "" {
		t.Fatalf("full identity resolution requested the catalog: %q %v", got, err)
	}
}

func TestBotCreateDoesNotStartOrSelectAnIdentity(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true}}
	if _, err := runBot(context.Background(), client, "", []string{"create", "--role", "agent", "--adapter", "default", "--name", "new"}); err != nil {
		t.Fatal(err)
	}
	cfg, ok := client.payload.(config.BotConfig)
	if !ok || cfg.Role != config.RoleAgent || cfg.DisplayName != "new" || cfg.Harness == nil || cfg.Harness.Timeout == "" || client.method != "POST" || client.endpoint != "/v2/bots" {
		t.Fatalf("unexpected create: %s %#v", client.endpoint, client.payload)
	}
}

func TestBotCreateConsoleOmitsExecutionConfiguration(t *testing.T) {
	client := &stubAPIClient{}
	if _, err := runBot(context.Background(), client, "", []string{"create", "--role", "console", "--name", "inbox"}); err != nil {
		t.Fatal(err)
	}
	cfg := client.payload.(config.BotConfig)
	if cfg.Role != config.RoleConsole || cfg.Harness != nil || cfg.MaxConcurrent != 0 || cfg.MaxQueued != 0 {
		t.Fatalf("console has execution configuration: %#v", cfg)
	}
}

func TestBotCreateRejectsImplicitRoleAndAdapter(t *testing.T) {
	for _, args := range [][]string{
		{"create", "--name", "missing-role"},
		{"create", "--role", "agent", "--name", "missing-adapter"},
		{"create", "--role", "console", "--adapter", "default"},
		{"create", "--role", "device", "--name", "unsupported"},
	} {
		client := &stubAPIClient{}
		if _, err := runBot(context.Background(), client, "", args); err == nil || client.endpoint != "" {
			t.Fatalf("invalid creation reached API: %v, %v", args, err)
		}
	}
}

func TestBotConfigFileRejectsInapplicableAndMissingFields(t *testing.T) {
	for _, raw := range []string{
		`{"displayName":"missing-role","harness":{"adapter":"default"}}`,
		`{"displayName":"missing-adapter","role":"agent","harness":{}}`,
		`{"displayName":"null-harness","role":"console","harness":null}`,
		`{"displayName":"empty-harness","role":"console","harness":{}}`,
		`{"displayName":"budget","role":"console","maxConcurrent":0}`,
		`{"displayName":"queue","role":"console","maxQueued":null}`,
		`{"displayName":"null-role","role":null}`,
	} {
		path := filepath.Join(t.TempDir(), "bot.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		client := &stubAPIClient{}
		if _, err := runBot(context.Background(), client, "", []string{"create", "--file", path}); err == nil || client.endpoint != "" {
			t.Fatalf("invalid file reached API: %s, %v", raw, err)
		}
	}
}

func TestBotCreateFileAppliesRoleDefaultsWithoutInventingHarness(t *testing.T) {
	for _, raw := range []string{
		`{"displayName":"inbox","role":"console"}`,
		`{"displayName":"worker","role":"agent","harness":{"adapter":"default"}}`,
	} {
		path := filepath.Join(t.TempDir(), "bot.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		client := &stubAPIClient{}
		if _, err := runBot(context.Background(), client, "", []string{"create", "--file", path}); err != nil {
			t.Fatal(err)
		}
		cfg := client.payload.(config.BotConfig)
		if cfg.Role == config.RoleConsole && cfg.Harness != nil {
			t.Fatal("invented console harness")
		}
		if cfg.Role == config.RoleAgent && (cfg.Harness == nil || cfg.Harness.Timeout == "") {
			t.Fatal("missing agent defaults")
		}
	}
}

func TestBotConvertRequiresExplicitSeparateDestination(t *testing.T) {
	for _, args := range [][]string{
		{"convert", "--file", "/source"},
		{"convert", "--output", "/destination"},
		{"convert", "--file", "/source", "--output", "/destination", "--data-dir", "/daemon"},
		{"import", "--file", "/source", "--output", "/destination"},
	} {
		client := &stubAPIClient{}
		if _, err := runBot(context.Background(), client, "", args); err == nil || client.endpoint != "" {
			t.Fatalf("invalid offline conversion reached API: %v, %v", args, err)
		}
	}
}

func TestBotConvertUsesOfflineMigration(t *testing.T) {
	root := t.TempDir()
	client := &stubAPIClient{}
	result, err := runBot(context.Background(), client, "", []string{
		"convert", "--file", filepath.Join(root, "missing-source"), "--output", filepath.Join(root, "destination"),
	})
	if err == nil || result.Code != "bot.convert" || result.OK || client.endpoint != "" {
		t.Fatalf("conversion bypassed offline converter: %#v, %v, %s", result, err, client.endpoint)
	}
}

func TestMigrateRequiresRootAndExclusiveMode(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--recover"},
		{"--data-dir", "/daemon", "--recover", "--legacy-config", "/legacy.yaml"},
		{"--data-dir", "/daemon", "unexpected"},
	} {
		if err := runMigrate(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("invalid migration mode accepted: %v", args)
		}
	}
}

func TestBotLifecycleUsesSelectedIdentity(t *testing.T) {
	id := "n1-" + strings.Repeat("b", 32)
	for _, action := range []string{"start", "stop", "archive", "restore"} {
		t.Run(action, func(t *testing.T) {
			client := &stubAPIClient{}
			if _, err := runBot(context.Background(), client, id, []string{action}); err != nil {
				t.Fatal(err)
			}
			if client.endpoint != "/v2/bots/"+id+"/"+action || client.method != "POST" || client.payload != nil {
				t.Fatalf("wrong identity route: %s", client.endpoint)
			}
		})
	}
}

func TestBotConfigPreservesRevisionForCAS(t *testing.T) {
	cfg := config.DefaultBotConfig()
	cfg.Revision = 9
	cfg.Harness.Adapter = "opencode"
	cfg.Harness.Model = "free-model"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bot.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	id := "n1-" + strings.Repeat("c", 32)
	client := &stubAPIClient{}
	if _, err = runBot(context.Background(), client, id, []string{"config", "--file", path}); err != nil {
		t.Fatal(err)
	}
	sent, ok := client.payload.(config.BotConfig)
	if !ok || sent.Revision != 9 || sent.Harness.Model != "free-model" || client.endpoint != "/v2/bots/"+id+"/config" {
		t.Fatalf("lost CAS/binding: %#v", client.payload)
	}
}

func TestTransferImportAndForwardUseBotContract(t *testing.T) {
	client := &stubAPIClient{}
	if _, err := runTransfer(context.Background(), client, []string{"import", "--file", "/external/file"}); err != nil {
		t.Fatal(err)
	}
	if client.endpoint != "/transfer/import" || client.payload.(map[string]any)["filePath"] != "/external/file" {
		t.Fatal("wrong import contract")
	}
	if _, err := runTransfer(context.Background(), client, []string{"forward", "--id", "transfer-a", "--target", "bot-b"}); err != nil {
		t.Fatal(err)
	}
	if client.endpoint != "/transfer/transfer-a/forward" || client.payload.(map[string]any)["targetBot"] != "bot-b" {
		t.Fatal("wrong forward contract")
	}
}
