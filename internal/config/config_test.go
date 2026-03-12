package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromOSReadsHomeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)

	configDir := filepath.Join(home, ".clawsynapse")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	content := []byte("nodeId: home-node\nnatsServers:\n  - nats://10.0.0.1:4222\nlocalApiAddr: 127.0.0.1:19090\ntrustMode: explicit\nheartbeatInterval: 20s\nannounceTtl: 45s\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS(nil)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.NodeID != "home-node" {
		t.Fatalf("expected node id from config, got %q", cfg.NodeID)
	}
	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.1:4222" {
		t.Fatalf("unexpected nats servers: %#v", cfg.NATSServers)
	}
	if cfg.TrustMode != "explicit" {
		t.Fatalf("expected trust mode explicit, got %q", cfg.TrustMode)
	}
	if cfg.HeartbeatInterval != "20s" {
		t.Fatalf("expected heartbeat 20s, got %q", cfg.HeartbeatInterval)
	}
	if cfg.AnnounceTTL != "45s" {
		t.Fatalf("expected announce ttl 45s, got %q", cfg.AnnounceTTL)
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
	content := []byte("nodeId: config-node\nnatsServers:\n  - nats://10.0.0.1:4222\nlocalApiAddr: 127.0.0.1:19090\ntrustMode: tofu\n")
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("NODE_ID=dotenv-node\nTRUST_MODE=open\nNATS_SERVERS=nats://10.0.0.2:4222\n"), 0o644); err != nil {
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

	cfg, err := LoadFromOS([]string{"--node-id", "flag-node", "--trust-mode", "explicit"})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.NodeID != "flag-node" {
		t.Fatalf("expected flag node id, got %q", cfg.NodeID)
	}
	if cfg.TrustMode != "explicit" {
		t.Fatalf("expected flag trust mode, got %q", cfg.TrustMode)
	}
	if cfg.LocalAPIAddr != "127.0.0.1:28080" {
		t.Fatalf("expected os env api addr, got %q", cfg.LocalAPIAddr)
	}
	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.2:4222" {
		t.Fatalf("expected dotenv nats servers, got %#v", cfg.NATSServers)
	}
}

func TestLoadFromOSUsesExplicitConfigPath(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	clearConfigEnv(t)

	customPath := filepath.Join(project, "custom.yaml")
	content := []byte("nodeId: custom-node\nnatsServers:\n  - nats://10.0.0.3:4222\n")
	if err := os.WriteFile(customPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromOS([]string{"--config", customPath})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.NodeID != "custom-node" {
		t.Fatalf("expected custom node id, got %q", cfg.NodeID)
	}
	if len(cfg.NATSServers) != 1 || cfg.NATSServers[0] != "nats://10.0.0.3:4222" {
		t.Fatalf("unexpected nats servers: %#v", cfg.NATSServers)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NODE_ID",
		"NATS_SERVERS",
		"LOCAL_API_ADDR",
		"DATA_DIR",
		"IDENTITY_KEY_PATH",
		"IDENTITY_PUB_PATH",
		"HEARTBEAT_INTERVAL_MS",
		"ANNOUNCE_TTL_MS",
		"TRUST_MODE",
	} {
		t.Setenv(key, "")
	}
}
