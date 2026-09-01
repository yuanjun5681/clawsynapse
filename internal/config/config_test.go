package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFromOSReadsHomeConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	chdirTempProject(t, project)

	configDir := filepath.Join(home, ".clawsynapse")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte("natsServers:\n  - nats://10.0.0.1:4222\nlocalApiAddr: 127.0.0.1:19090\ntrustMode: explicit\ntrustAutoApprove: true\nheartbeatInterval: 20s\nannounceTtl: 45s\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS(nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.1:4222" {
		t.Fatalf("unexpected nats servers: %#v", cfg.NATSServers)
	}
	if cfg.TrustMode != "explicit" {
		t.Fatalf("expected trust mode explicit, got %q", cfg.TrustMode)
	}
	if !cfg.TrustAutoApprove {
		t.Fatal("expected trust auto approve true")
	}
	if cfg.HeartbeatInterval != "20s" {
		t.Fatalf("expected heartbeat 20s, got %q", cfg.HeartbeatInterval)
	}
	if cfg.AnnounceTTL != "45s" {
		t.Fatalf("expected announce ttl 45s, got %q", cfg.AnnounceTTL)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected default log level info, got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("expected default log format json, got %q", cfg.LogFormat)
	}
	if cfg.AgentAdapter != "default" {
		t.Fatalf("expected default agent adapter, got %q", cfg.AgentAdapter)
	}
	if cfg.AgentAdapterTimeout != "10m0s" {
		t.Fatalf("expected default agent adapter timeout 10m0s, got %q", cfg.AgentAdapterTimeout)
	}
	if cfg.AgentProduct != "clawsynapse" {
		t.Fatalf("expected default agent product clawsynapse, got %q", cfg.AgentProduct)
	}
	if len(cfg.Capabilities) != 2 || cfg.Capabilities[0] != "chat" || cfg.Capabilities[1] != "tools" {
		t.Fatalf("unexpected default capabilities: %#v", cfg.Capabilities)
	}
}

func TestLoadFromOSMergesDotEnvEnvAndFlags(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	t.Setenv("LOCAL_API_ADDR", "127.0.0.1:28080")

	configDir := filepath.Join(home, ".clawsynapse")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte("natsServers:\n  - nats://10.0.0.1:4222\nlocalApiAddr: 127.0.0.1:19090\ntrustMode: tofu\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("TRUST_MODE=open\nTRUST_AUTO_APPROVE=true\nNATS_SERVERS=nats://10.0.0.2:4222\nAGENT_ADAPTER=openclaw\nAGENT_ADAPTER_TIMEOUT=15m\nAGENT_PRODUCT=deepseek-harness\nAGENT_VERSION=0.1.2-alpha.3\nAGENT_CAPABILITIES=chat,task,transfer\nLOG_LEVEL=debug\nLOG_FORMAT=text\nLOG_ADD_SOURCE=true\nLOG_FILE_PATH=./runtime/clawsynapsed.log\nLOG_ROTATE_MAX_SIZE_MB=64\nLOG_ROTATE_MAX_BACKUPS=5\nLOG_ROTATE_MAX_AGE_DAYS=14\nLOG_ROTATE_COMPRESS=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS([]string{"--trust-mode", "explicit"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TrustMode != "explicit" {
		t.Fatalf("expected flag trust mode, got %q", cfg.TrustMode)
	}
	if !cfg.TrustAutoApprove {
		t.Fatal("expected dotenv trust auto approve true")
	}
	if cfg.LocalAPIAddr != "127.0.0.1:28080" {
		t.Fatalf("expected os env api addr, got %q", cfg.LocalAPIAddr)
	}
	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.2:4222" {
		t.Fatalf("expected dotenv nats servers, got %#v", cfg.NATSServers)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("expected dotenv log level, got %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Fatalf("expected dotenv log format, got %q", cfg.LogFormat)
	}
	if !cfg.LogAddSource {
		t.Fatal("expected dotenv log add source to be true")
	}
	if !filepath.IsAbs(cfg.LogFilePath) {
		t.Fatalf("expected dotenv log file path to be absolute, got %q", cfg.LogFilePath)
	}
	if !strings.HasSuffix(cfg.LogFilePath, filepath.Join("runtime", "clawsynapsed.log")) {
		t.Fatalf("expected dotenv log file path to end with runtime/clawsynapsed.log, got %q", cfg.LogFilePath)
	}
	if cfg.LogRotateMaxSizeMB != 64 {
		t.Fatalf("expected dotenv log rotate size 64, got %d", cfg.LogRotateMaxSizeMB)
	}
	if cfg.LogRotateMaxBackups != 5 {
		t.Fatalf("expected dotenv log rotate backups 5, got %d", cfg.LogRotateMaxBackups)
	}
	if cfg.LogRotateMaxAgeDays != 14 {
		t.Fatalf("expected dotenv log rotate age 14, got %d", cfg.LogRotateMaxAgeDays)
	}
	if !cfg.LogRotateCompress {
		t.Fatal("expected dotenv log rotate compress to be true")
	}
	if cfg.AgentAdapter != "openclaw" {
		t.Fatalf("expected dotenv agent adapter openclaw, got %q", cfg.AgentAdapter)
	}
	if cfg.AgentAdapterTimeout != "15m0s" {
		t.Fatalf("expected dotenv agent adapter timeout 15m0s, got %q", cfg.AgentAdapterTimeout)
	}
	if cfg.AgentProduct != "deepseek-harness" {
		t.Fatalf("expected dotenv agent product deepseek-harness, got %q", cfg.AgentProduct)
	}
	if cfg.AgentVersion != "0.1.2-alpha.3" {
		t.Fatalf("expected dotenv agent version 0.1.2-alpha.3, got %q", cfg.AgentVersion)
	}
	if len(cfg.Capabilities) != 3 || cfg.Capabilities[2] != "transfer" {
		t.Fatalf("unexpected dotenv capabilities: %#v", cfg.Capabilities)
	}
}

func TestLoadFromOSUsesExplicitConfigPath(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	chdirTempProject(t, project)

	customPath := filepath.Join(project, "custom.yaml")
	content := []byte("natsServers:\n  - nats://10.0.0.3:4222\n")
	if err := os.WriteFile(customPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS([]string{"--config", customPath})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.3:4222" {
		t.Fatalf("unexpected nats servers: %#v", cfg.NATSServers)
	}
}

func TestLoadFromOSCanDisableTrustAutoApproveFromEnvAndFlag(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	chdirTempProject(t, project)

	configDir := filepath.Join(home, ".clawsynapse")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte("natsServers:\n  - nats://10.0.0.1:4222\ntrustAutoApprove: true\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TRUST_AUTO_APPROVE", "false")
	cfg, err := LoadFromOS(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustAutoApprove {
		t.Fatal("expected env to disable trust auto approve")
	}

	t.Setenv("TRUST_AUTO_APPROVE", "true")
	cfg, err = LoadFromOS([]string{"--trust-auto-approve=false"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustAutoApprove {
		t.Fatal("expected flag to disable trust auto approve")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NATS_SERVERS",
		"LOCAL_API_ADDR",
		"DATA_DIR",
		"IDENTITY_KEY_PATH",
		"IDENTITY_PUB_PATH",
		"HEARTBEAT_INTERVAL_MS",
		"ANNOUNCE_TTL_MS",
		"AGENT_PRODUCT",
		"AGENT_VERSION",
		"AGENT_CAPABILITIES",
		"TRUST_MODE",
		"TRUST_AUTO_APPROVE",
		"AGENT_ADAPTER",
		"AGENT_ADAPTER_TIMEOUT",
		"WEBHOOK_URL",
		"WEBHOOK_AUTH_TOKEN",
		"LOG_LEVEL",
		"LOG_FORMAT",
		"LOG_ADD_SOURCE",
		"LOG_FILE_PATH",
		"LOG_ROTATE_MAX_SIZE_MB",
		"LOG_ROTATE_MAX_BACKUPS",
		"LOG_ROTATE_MAX_AGE_DAYS",
		"LOG_ROTATE_COMPRESS",
	} {
		t.Setenv(key, "")
	}
}

func TestWebhookAuthTokenLoadsFromDotEnvAndProcessEnv(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)
	chdirTempProject(t, project)

	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("WEBHOOK_AUTH_TOKEN=dotenv-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookAuthToken != "dotenv-secret" {
		t.Fatalf("webhook token = %q, want dotenv-secret", cfg.WebhookAuthToken)
	}

	t.Setenv("WEBHOOK_AUTH_TOKEN", " process-secret ")
	cfg, err = LoadFromOS(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookAuthToken != "process-secret" {
		t.Fatalf("webhook token = %q, want process-secret", cfg.WebhookAuthToken)
	}

	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "process-secret") || strings.Contains(string(encoded), "webhookAuthToken") {
		t.Fatalf("webhook token leaked through json: %s", encoded)
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveToFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "process-secret") || strings.Contains(string(saved), "webhookAuthToken") {
		t.Fatalf("webhook token leaked through yaml: %s", saved)
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	cfg := Config{
		NATSServers:         []string{"nats://127.0.0.1:4222"},
		TrustMode:           "tofu",
		AgentAdapter:        "default",
		LogLevel:            "info",
		LogFormat:           "json",
		LogRotateMaxSizeMB:  10,
		LogRotateMaxBackups: 3,
		LogRotateMaxAgeDays: 7,
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidateRejectsInvalidTrustMode(t *testing.T) {
	cfg := Config{
		NATSServers:         []string{"nats://127.0.0.1:4222"},
		TrustMode:           "invalid",
		AgentAdapter:        "default",
		LogLevel:            "info",
		LogFormat:           "json",
		LogRotateMaxSizeMB:  10,
		LogRotateMaxBackups: 3,
		LogRotateMaxAgeDays: 7,
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid trust mode")
	}
}

func TestValidateRejectsInvalidAdapter(t *testing.T) {
	cfg := Config{
		NATSServers:         []string{"nats://127.0.0.1:4222"},
		TrustMode:           "tofu",
		AgentAdapter:        "unknown",
		LogLevel:            "info",
		LogFormat:           "json",
		LogRotateMaxSizeMB:  10,
		LogRotateMaxBackups: 3,
		LogRotateMaxAgeDays: 7,
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid adapter")
	}
}

func TestValidateRejectsInvalidAgentAdapterTimeout(t *testing.T) {
	cfg := Config{
		NATSServers:         []string{"nats://127.0.0.1:4222"},
		TrustMode:           "tofu",
		AgentAdapter:        "default",
		AgentAdapterTimeout: "0s",
		LogLevel:            "info",
		LogFormat:           "json",
		LogRotateMaxSizeMB:  10,
		LogRotateMaxBackups: 3,
		LogRotateMaxAgeDays: 7,
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for invalid agent adapter timeout")
	}
}

func TestValidateRejectsEmptyNATS(t *testing.T) {
	cfg := Config{
		TrustMode:           "tofu",
		AgentAdapter:        "default",
		LogLevel:            "info",
		LogFormat:           "json",
		LogRotateMaxSizeMB:  10,
		LogRotateMaxBackups: 3,
		LogRotateMaxAgeDays: 7,
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty NATS servers")
	}
}

func TestSaveToFileAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	cfg := Config{
		NATSServers:         []string{"nats://10.0.0.1:4222", "nats://10.0.0.2:4222"},
		LocalAPIAddr:        "127.0.0.1:19090",
		TrustMode:           "explicit",
		TrustAutoApprove:    true,
		AgentAdapter:        "openclaw",
		AgentAdapterTimeout: "20m",
		AgentProduct:        "deepseek-harness",
		AgentVersion:        "0.1.2-alpha.3",
		Capabilities:        []string{"chat", "task", "transfer"},
		LogLevel:            "debug",
		LogFormat:           "text",
		LogFilePath:         filepath.Join(dir, "clawsynapsed.log"),
		LogRotateMaxSizeMB:  64,
		LogRotateMaxBackups: 5,
		LogRotateMaxAgeDays: 14,
		LogRotateCompress:   true,
		HeartbeatInterval:   "20s",
		AnnounceTTL:         "45s",
		TransferTTL:         "12h",
		LogAddSource:        true,
	}

	if err := SaveToFile(path, cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := loadConfigValues(path, true)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}

	if len(loaded.NATSServers) != 2 || loaded.NATSServers[0] != "nats://10.0.0.1:4222" {
		t.Fatalf("unexpected nats servers: %#v", loaded.NATSServers)
	}
	if loaded.TrustMode != "explicit" {
		t.Fatalf("expected trust mode explicit, got %q", loaded.TrustMode)
	}
	if !loaded.TrustAutoApprove {
		t.Fatal("expected trust auto approve true")
	}
	if loaded.LogLevel != "debug" {
		t.Fatalf("expected log level debug, got %q", loaded.LogLevel)
	}
	if loaded.AgentAdapterTimeout != 20*time.Minute {
		t.Fatalf("expected agent adapter timeout 20m, got %s", loaded.AgentAdapterTimeout)
	}
	if loaded.AgentProduct != "deepseek-harness" || loaded.AgentVersion != "0.1.2-alpha.3" {
		t.Fatalf("unexpected agent identity: product=%q version=%q", loaded.AgentProduct, loaded.AgentVersion)
	}
	if len(loaded.Capabilities) != 3 || loaded.Capabilities[2] != "transfer" {
		t.Fatalf("unexpected capabilities: %#v", loaded.Capabilities)
	}
	if !loaded.LogAddSource {
		t.Fatal("expected log add source true")
	}
	if loaded.LogFilePath != filepath.Join(dir, "clawsynapsed.log") {
		t.Fatalf("expected log file path to round trip, got %q", loaded.LogFilePath)
	}
	if loaded.LogRotateMaxSizeMB != 64 {
		t.Fatalf("expected rotate size 64, got %d", loaded.LogRotateMaxSizeMB)
	}
	if loaded.LogRotateMaxBackups != 5 {
		t.Fatalf("expected rotate backups 5, got %d", loaded.LogRotateMaxBackups)
	}
	if loaded.LogRotateMaxAgeDays != 14 {
		t.Fatalf("expected rotate age 14, got %d", loaded.LogRotateMaxAgeDays)
	}
	if !loaded.LogRotateCompress {
		t.Fatal("expected rotate compress true")
	}
}

func chdirTempProject(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
}
