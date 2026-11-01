package main

import (
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

func TestFullBotIDDoesNotRequireAdminList(t *testing.T) {
	client := &stubAPIClient{}
	id := "n1-" + strings.Repeat("a", 32)
	got, err := resolveBot(context.Background(), client, id)
	if err != nil || got != id || client.endpoint != "" {
		t.Fatalf("full identity resolution used admin privileges: %q %v", got, err)
	}
}

func TestBotCreateDoesNotStartOrSelectAnIdentity(t *testing.T) {
	client := &stubAPIClient{result: types.APIResult{OK: true}}
	if _, err := runBot(context.Background(), client, "", []string{"create", "--name", "new"}); err != nil {
		t.Fatal(err)
	}
	cfg, ok := client.payload.(config.BotConfig)
	if !ok || cfg.DisplayName != "new" || cfg.Harness.Timeout == "" || client.method != "POST" || client.endpoint != "/v2/bots" {
		t.Fatalf("unexpected create: %s %#v", client.endpoint, client.payload)
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
