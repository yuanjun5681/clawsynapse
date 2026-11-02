package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDaemonRejectsLegacyAndUnknownFields(t *testing.T) {
	for _, body := range []string{`{"dataDir":"/legacy"}`, `{"schemaVersion":1}`, `{"schemaVersion":2}`, `{"schemaVersion":4}`, `{"schemaVersion":3,"displayName":"not a daemon setting"}`, `{"schemaVersion":3} {}`} {
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
	for _, mutate := range []func(*BotConfig){func(b *BotConfig) { b.Role = "" }, func(b *BotConfig) { b.Role = "device" }, func(b *BotConfig) { b.Harness = nil }, func(b *BotConfig) { b.MaxConcurrent = 0 }, func(b *BotConfig) { b.Harness.Timeout = "0s" }, func(b *BotConfig) { b.BindingRevision = "../another-bot" }, func(b *BotConfig) { b.FileQuotaBytes = -1 }, func(b *BotConfig) { b.Harness.Adapter = "console" }, func(b *BotConfig) { b.Harness.Adapter = "openclaw"; b.Harness.Model = "unsupported" }} {
		candidate := DefaultBotConfig()
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
func TestDaemonLoadsStrictV3DefaultsAndOverrides(t *testing.T) {
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
	if got.SchemaVersion != DaemonSchemaVersion || got.LocalAPIAddr != "127.0.0.1:19001" {
		t.Fatalf("unexpected config: %#v", got)
	}
}

func TestConsoleConfigurationRejectsExecutionFields(t *testing.T) {
	cfg := DefaultBotConfigForRole(RoleConsole)
	if err := ValidateBot(cfg); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BotConfig){
		func(b *BotConfig) { b.Harness = &HarnessBinding{} },
		func(b *BotConfig) { b.Harness = &HarnessBinding{Adapter: "console"} },
		func(b *BotConfig) { b.MaxConcurrent = 1 },
		func(b *BotConfig) { b.MaxQueued = 1 },
	} {
		candidate := cfg
		mutate(&candidate)
		if err := ValidateBot(candidate); err == nil {
			t.Fatal("Console accepted execution configuration")
		}
	}
}

func TestBotDecodeRequiresExplicitRoleAndApplicableFields(t *testing.T) {
	for _, body := range []string{
		`{}`, `null`, `{"role":null}`, `{"role":""}`, `{"role":"device"}`,
		`{"role":"unknown"}`, `{"roles":["agent"]}`, `{"role":"agent","roles":["agent"]}`,
		`{"role":"console","harness":null}`, `{"role":"console","harness":{}}`,
		`{"role":"console","harness":{"adapter":"console"}}`,
		`{"role":"console","maxConcurrent":0}`, `{"role":"console","maxConcurrent":null}`,
		`{"role":"console","maxQueued":0}`, `{"role":"console","maxQueued":null}`,
		`{"role":"console","MAXQUEUED":0}`,
		`{"role":"agent"}`, `{"role":"agent","harness":null}`, `{"role":"agent","harness":{}}`,
		`{"role":"agent","harness":{"adapter":null}}`, `{"role":"agent","harness":{"adapter":""}}`,
		`{"role":"agent","harness":{"adapter":"console"}}`, `{"role":"agent","harness":{"adapter":"unknown"}}`,
		`{"role":"agent","harness":{"adapter":"codex","timeout":null}}`,
		`{"role":"agent","harness":{"adapter":"codex"},"maxConcurrent":null}`,
		`{"role":"agent","harness":{"adapter":"codex"},"maxQueued":null}`,
		`{"role":"agent","harness":{"adapter":"codex","unexpected":true}}`,
		`{"role":"agent","role":"console","harness":{"adapter":"codex"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			cfg := DefaultBotConfig()
			cfg.DisplayName = "unchanged"
			if err := DecodeStrict([]byte(body), &cfg); err == nil {
				t.Fatalf("accepted invalid JSON configuration: %s", body)
			}
			if cfg.DisplayName != "unchanged" || cfg.Role != RoleAgent || cfg.Harness == nil || cfg.Harness.Adapter != "default" {
				t.Fatal("failed decode changed destination")
			}
		})
	}
}

func TestBotDecodeDefaultsOnlyApplicableFields(t *testing.T) {
	for _, adapter := range []string{"default", "openclaw", "opencode", "codex", "webhook"} {
		body := `{"role":"agent","harness":{"adapter":"` + adapter + `"`
		if adapter == "webhook" {
			body += `,"webhookUrl":"https://localhost/hook"`
		}
		body += `}}`
		var cfg BotConfig
		if err := DecodeStrict([]byte(body), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBot(cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Harness.Adapter != adapter || cfg.Harness.Timeout == "" || cfg.MaxConcurrent != 2 || cfg.MaxQueued != 64 {
			t.Fatalf("missing Agent defaults: %#v", cfg)
		}
	}
	cfg := DefaultBotConfig()
	if err := DecodeStrict([]byte(`{"role":"console"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Harness != nil || cfg.MaxConcurrent != 0 || cfg.MaxQueued != 0 {
		t.Fatalf("Console inherited Agent defaults: %#v", cfg)
	}
	if err := ValidateBot(cfg); err != nil {
		t.Fatal(err)
	}
	mapped := RuntimeConfig(DefaultDaemonConfig(), cfg, "console")
	if mapped.Role != RoleConsole || mapped.AgentAdapter != "" || mapped.AgentAdapterTimeout != "" || mapped.WebhookURL != "" || len(mapped.AgentAllowedIDs) != 0 {
		t.Fatalf("Console mapped execution configuration: %#v", mapped)
	}
	if err := Validate(mapped); err != nil {
		t.Fatal(err)
	}
}

func TestBotDecodeDoesNotDefaultOverInvalidValues(t *testing.T) {
	for _, body := range []string{
		`{"role":"agent","harness":{"adapter":"codex","timeout":""}}`,
		`{"role":"agent","harness":{"adapter":"codex","timeout":"0s"}}`,
		`{"role":"agent","harness":{"adapter":"codex"},"maxConcurrent":0}`,
		`{"role":"agent","harness":{"adapter":"codex"},"maxQueued":-1}`,
		`{"role":"console","heartbeatInterval":""}`,
	} {
		var cfg BotConfig
		if err := DecodeStrict([]byte(body), &cfg); err == nil {
			if err := ValidateBot(cfg); err == nil {
				t.Fatalf("defaulted over invalid configuration: %s", body)
			}
		}
	}
}

func TestBotJSONRoundTripPreservesRoleAndZeroQueueBudget(t *testing.T) {
	for _, role := range []string{RoleConsole, RoleAgent} {
		cfg := DefaultBotConfigForRole(role)
		cfg.MaxQueued = 0
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"harness", "maxConcurrent", "maxQueued"} {
			_, present := fields[name]
			if present != (role == RoleAgent) {
				t.Fatalf("%s serialized unexpected %s: %s", role, name, raw)
			}
		}
		var got BotConfig
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Role != role || got.MaxQueued != 0 {
			t.Fatalf("configuration changed on JSON round trip: %#v", got)
		}
		if err := ValidateBot(got); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedBotJSONUsesStrictRoleContract(t *testing.T) {
	var request struct {
		Config BotConfig `json:"config"`
	}
	for _, body := range []string{
		`{"config":{"harness":{"adapter":"codex"}}}`,
		`{"config":{"role":"console","maxQueued":0}}`,
		`{"config":{"role":"console","harness":null}}`,
	} {
		if err := json.Unmarshal([]byte(body), &request); err == nil {
			t.Fatalf("nested decoder bypassed role contract: %s", body)
		}
	}
	if err := json.Unmarshal([]byte(`{"config":{"role":"console"}}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.Config.Role != RoleConsole || request.Config.Harness != nil {
		t.Fatalf("invalid nested Console defaults: %#v", request.Config)
	}
}

func TestLegacyConsoleAdapterDoesNotBecomeAgentHarness(t *testing.T) {
	legacy := RuntimeConfig(DefaultDaemonConfig(), DefaultBotConfig(), "legacy")
	legacy.Role = ""
	legacy.AgentAdapter = "console"
	if err := Validate(legacy); err != nil {
		t.Fatalf("legacy migration validation rejected Console adapter: %v", err)
	}
	legacy.Role = RoleAgent
	if err := Validate(legacy); err == nil {
		t.Fatal("runtime Agent accepted legacy Console adapter")
	}
}
