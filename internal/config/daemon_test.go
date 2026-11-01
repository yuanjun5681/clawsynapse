package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDaemonRejectsLegacyAndUnknownFields(t *testing.T) {
	for _, body := range []string{`{"dataDir":"/legacy"}`, `{"schemaVersion":1}`, `{"schemaVersion":2,"displayName":"not a daemon setting"}`, `{"schemaVersion":2} {}`} {
		root := t.TempDir()
		path := filepath.Join(root, "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDaemon([]string{"--config", path, "--data-dir", root}); err == nil {
			t.Fatalf("accepted incompatible configuration: %s", body)
		}
	}
}
func TestLoadDaemonCheckConfigIsReadOnlyAndIgnoresBotEnvironment(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("DISPLAY_NAME", "ambient Bot")
	t.Setenv("TRUST_MODE", "open")
	t.Setenv("AGENT_ADAPTER", "webhook")
	cfg, err := LoadDaemon([]string{"--data-dir", root, "--check-config"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CheckConfig || cfg.AdminTokenPath != filepath.Join(root, "admin.token") {
		t.Fatalf("unexpected daemon config: %#v", cfg)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("check-config changed filesystem: %v", err)
	}
	b := DefaultBotConfig()
	mapped := RuntimeConfig(cfg, b, "n1-example")
	if mapped.DisplayName != "" || mapped.TrustMode != "tofu" || mapped.AgentAdapter != "default" {
		t.Fatal("ambient Bot config leaked")
	}
}
func TestDaemonAndBotValidation(t *testing.T) {
	b := DefaultBotConfig()
	if err := ValidateBot(b); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BotConfig){func(b *BotConfig) { b.MaxConcurrent = 0 }, func(b *BotConfig) { b.Harness.Timeout = "0s" }, func(b *BotConfig) { b.BindingRevision = "../another-bot" }, func(b *BotConfig) { b.FileQuotaBytes = -1 }, func(b *BotConfig) { b.Harness.Adapter = "openclaw"; b.Harness.Model = "unsupported" }} {
		candidate := b
		mutate(&candidate)
		if err := ValidateBot(candidate); err == nil {
			t.Fatalf("accepted invalid Bot: %#v", candidate)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "trust.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDaemon([]string{"--data-dir", root}); err == nil {
		t.Fatal("accepted legacy directory")
	}
}
func TestDaemonLoadsStrictV2DefaultsAndOverrides(t *testing.T) {
	root := t.TempDir()
	d := DefaultDaemonConfig()
	d.DataDir = root
	d.AdminTokenPath = filepath.Join(root, "admin.token")
	d.LocalAPIAddr = "127.0.0.1:19000"
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDaemon([]string{"--data-dir", root, "--local-api-addr", "127.0.0.1:19001"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 2 || got.LocalAPIAddr != "127.0.0.1:19001" {
		t.Fatalf("unexpected config: %#v", got)
	}
}
