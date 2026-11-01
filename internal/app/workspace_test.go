package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"clawsynapse/internal/api"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

func requireBindingFailure(t *testing.T, err error) {
	t.Helper()
	var failure *api.Error
	if !errors.As(err, &failure) || failure.Code != "workspace.binding_invalid" {
		t.Fatalf("expected closed binding failure, got %v", err)
	}
}

func TestWorkspaceBindingAtomicConcurrentReplayAndRestart(t *testing.T) {
	a := testDaemon(t)
	ordinary := newTestBot(t, a, "Bot-A")
	const count = 12
	var wg sync.WaitGroup
	results := make(chan api.BotInfo, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bot, token, err := a.manager.BindWorkspace("desktop-1", "Bot-A")
			if err != nil || token == "" {
				t.Errorf("binding failed: %v", err)
				return
			}
			if scope, ok := a.manager.Authenticate(token); !ok || scope != bot.BotID {
				t.Error("binding did not deliver its own scoped credential")
			}
			results <- bot
		}()
	}
	wg.Wait()
	close(results)
	var first api.BotInfo
	for bot := range results {
		if first.BotID == "" {
			first = bot
		}
		if bot.BotID != first.BotID || bot.DID != first.DID || bot.BotID == ordinary || bot.HarnessApplicable || bot.HarnessHealthy {
			t.Fatal("binding reused a named identity or changed its Console identity")
		}
	}
	if first.BotID == "" || len(a.manager.List()) != 2 {
		t.Fatal("concurrent retries created duplicate identities")
	}
	before := botToken(t, a, first.BotID)
	if err := a.manager.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := newManager(a.manager.cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Shutdown(context.Background()) })
	bot, token, err := restored.BindWorkspace("desktop-1", "changed retry name")
	if err != nil || bot.BotID != first.BotID || bot.DisplayName != "Bot-A" || token != before {
		t.Fatalf("restart did not recover the original binding: bot=%#v error=%v", bot, err)
	}
	other, otherToken, err := restored.BindWorkspace("desktop-2", "Bot-A")
	if err != nil || other.BotID == bot.BotID || otherToken == token {
		t.Fatal("different workspaces share an identity or credential")
	}
}

func TestWorkspaceBindingFailsClosedOnLostCredentialsAndArchive(t *testing.T) {
	for _, file := range []string{"credentials/identity.key", "credentials/api.token", "workspace-binding.json"} {
		t.Run(file, func(t *testing.T) {
			a := testDaemon(t)
			bot, _, err := a.manager.BindWorkspace("desktop", "Bot-A")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.manager.botDir(bot.BotID), file)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			_, _, err = a.manager.BindWorkspace("desktop", "Bot-A")
			requireBindingFailure(t, err)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || len(a.manager.List()) != 1 {
				t.Fatal("binding repair silently minted credentials or another identity")
			}
		})
	}
	a := testDaemon(t)
	bot, _, err := a.manager.BindWorkspace("desktop", "Bot-A")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := a.manager.Config(bot.BotID)
	cfg.Harness.Adapter = "opencode"
	if _, err := a.manager.Update(bot.BotID, cfg); err == nil {
		t.Fatal("workspace identity was rebound to an execution Harness")
	}
	if _, err := a.manager.Transition(bot.BotID, "archive"); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.manager.BindWorkspace("desktop", "Bot-A")
	requireBindingFailure(t, err)
	if _, err := a.manager.Transition(bot.BotID, "restore"); err != nil {
		t.Fatal(err)
	}
	if replay, _, err := a.manager.BindWorkspace("desktop", "Bot-A"); err != nil || replay.BotID != bot.BotID {
		t.Fatalf("explicit restore lost workspace claim: %v", err)
	}
}

func TestWorkspaceLostCatalogClaimAndUnregisteredCreationFailClosed(t *testing.T) {
	for _, removeRecord := range []bool{false, true} {
		a := testDaemon(t)
		bot, _, err := a.manager.BindWorkspace("desktop", "Bot-A")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.manager.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		c := catalog{SchemaVersion: 2, Bots: []catalogRecord{}}
		if !removeRecord {
			c.Bots = append(c.Bots, catalogRecord{BotID: bot.BotID, DesiredState: "stopped"})
		}
		if err := store.WriteJSONAtomic(filepath.Join(a.manager.cfg.DataDir, "bots.json"), c, 0600); err != nil {
			t.Fatal(err)
		}
		restored, err := newManager(a.manager.cfg, "test", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = restored.BindWorkspace("desktop", "Bot-A")
		requireBindingFailure(t, err)
		_ = restored.Shutdown(context.Background())
		if _, err := os.Stat(filepath.Join(a.manager.botDir(bot.BotID), "credentials", "identity.key")); err != nil {
			t.Fatal("original recoverable identity was removed")
		}
	}
}

func TestWorkspaceBindingHTTPAuthorizationValidationAndCapabilities(t *testing.T) {
	a := testDaemon(t)
	admin := adminToken(t, a)
	path := "/v2/workspaces/desktop/binding"
	for _, body := range []any{map[string]string{}, map[string]string{"displayName": " "}, map[string]string{"displayName": "Bot-A", "token": "not-accepted"}} {
		requireStatus(t, request(t, a, "POST", path, admin, body), 400)
	}
	for _, workspace := range []string{"bad.id", "bad%20id"} {
		requireStatus(t, request(t, a, "POST", "/v2/workspaces/"+workspace+"/binding", admin, map[string]string{"displayName": "Bot-A"}), 400)
	}
	response := request(t, a, "POST", path, admin, map[string]string{"displayName": "Bot-A"})
	requireStatus(t, response, 200)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential response may be cached")
	}
	var result types.APIResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	token, ok := result.Data["token"].(string)
	if !ok || token == "" || token == admin {
		t.Fatal("workspace credential is missing or administrative")
	}
	requireStatus(t, request(t, a, "POST", path, token, map[string]string{"displayName": "Bot-A"}), 403)
	bot := result.Data["bot"].(map[string]any)
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+bot["botId"].(string)+"/messages", token, nil), 200)
	health := request(t, a, "GET", "/v2/health", admin, nil)
	requireStatus(t, health, 200)
	if err := json.Unmarshal(health.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	caps := result.Data["capabilities"].([]any)
	if len(caps) != 2 || caps[0] != "console" || caps[1] != "workspace_binding" {
		t.Fatalf("unexpected capabilities: %#v", caps)
	}
}
