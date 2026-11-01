package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionRejectsWeakTransportsAndTrust(t *testing.T) {
	strong := Config{NATSServers: []string{"tls://nats.example:4222"}, TrustMode: "explicit", Production: true, NATSCredentialsFile: "/private/bot.creds", LocalAPIAddr: "127.0.0.1:18080"}
	if err := ValidateSecurity(strong); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.TrustMode = "open" },
		func(c *Config) { c.NATSServers = []string{"nats://nats.example:4222"} },
		func(c *Config) { c.NATSServers = []string{"tls://one:4222", "nats://two:4222"} },
		func(c *Config) { c.NATSCredentialsFile = "" },
		func(c *Config) { c.NATSTLSCertFile = "cert.pem" },
	} {
		cfg := strong
		change(&cfg)
		if err := ValidateSecurity(cfg); err == nil {
			t.Fatal("weak production configuration accepted")
		}
	}
	strong.NATSCredentialsFile = ""
	strong.NATSTLSCertFile, strong.NATSTLSKeyFile = "cert.pem", "key.pem"
	if err := ValidateSecurity(strong); err != nil {
		t.Fatalf("mutual TLS identity rejected: %v", err)
	}
}

func TestRemoteAPIRequiresExplicitTLS(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "[::]:8080", ":8080", "192.0.2.1:8080"} {
		if err := ValidateAPIListener(addr, false, "cert", "key"); err == nil {
			t.Fatalf("implicit remote address accepted: %s", addr)
		}
		if err := ValidateAPIListener(addr, true, "", ""); err == nil {
			t.Fatalf("plaintext remote address accepted: %s", addr)
		}
		if err := ValidateAPIListener(addr, true, "cert", "key"); err != nil {
			t.Fatal(err)
		}
	}
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if err := ValidateAPIListener(addr, false, "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSecurityConfigurationSourcesAndPersistence(t *testing.T) {
	clearConfigEnv(t)
	chdirTempProject(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	data := []byte("production: true\ntrustMode: explicit\nnatsServers: [tls://nats.example:4222]\nnatsCredentialsFile: /private/bot.creds\nnatsTLSCAFile: /private/ca.pem\nnatsTLSCertFile: /private/client.pem\nnatsTLSKeyFile: /private/client.key\nnatsTLSServerName: nats.example\nallowRemoteAPI: true\nlocalApiAddr: 0.0.0.0:18080\napiTLSCertFile: /private/api.pem\napiTLSKeyFile: /private/api.key\napiTokenPath: /private/api.token\nagentAdapter: codex\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromOS([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Production || !cfg.AllowRemoteAPI || cfg.APITokenPath != "/private/api.token" || cfg.AgentAdapter != "codex" || cfg.NATSTLSServerName != "nats.example" {
		t.Fatalf("security YAML not loaded: %+v", cfg)
	}
	t.Setenv("NATS_TLS_SERVER_NAME", "env.example")
	cfg, err = LoadFromOS([]string{"--config", path, "--nats-tls-server-name", "cli.example", "--api-token-path", "~/custom.token"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NATSTLSServerName != "cli.example" || cfg.APITokenPath != filepath.Join(home, "custom.token") {
		t.Fatal("CLI security precedence or path expansion failed")
	}
	saved := filepath.Join(home, "saved.yaml")
	if err := SaveToFile(saved, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFromOS([]string{"--config", saved, "--nats-tls-server-name", "cli.example"})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.NATSCredentialsFile != cfg.NATSCredentialsFile || reloaded.APITLSKeyFile != cfg.APITLSKeyFile || reloaded.Production != cfg.Production {
		t.Fatal("security settings lost during save")
	}
	t.Setenv("ALLOW_REMOTE_API", "false")
	if _, err := LoadFromOS([]string{"--config", path}); err == nil {
		t.Fatal("explicit environment false did not disable remote API")
	}
}

func TestTokenPathDefaultsToSelectedDataDir(t *testing.T) {
	clearConfigEnv(t)
	chdirTempProject(t, t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dataDir := t.TempDir()
	cfg, err := LoadFromOS([]string{"--data-dir", dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APITokenPath != filepath.Join(dataDir, "api.token") {
		t.Fatalf("token path did not follow data directory: %s", cfg.APITokenPath)
	}
}

func TestAgentRoutingAllowlistValidationAndSources(t *testing.T) {
	for _, id := range []string{"", "../other", "--option=value", "with space", "用户", strings.Repeat("x", 129)} {
		if err := ValidateSecurity(Config{AgentAllowedIDs: []string{id}}); err == nil {
			t.Fatalf("unsafe agent ID accepted: %q", id)
		}
	}
	clearConfigEnv(t)
	chdirTempProject(t, t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENT_ALLOWED_IDS", "env-worker")
	cfg, err := LoadFromOS([]string{"--agent-allowed-ids", "worker-a,worker_b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.AgentAllowedIDs, ",") != "worker-a,worker_b" {
		t.Fatal("CLI allowlist did not override environment")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveToFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_ALLOWED_IDS", "")
	reloaded, err := LoadFromOS([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(reloaded.AgentAllowedIDs, ",") != "worker-a,worker_b" {
		t.Fatal("allowlist lost during YAML save")
	}
	if _, err := LoadFromOS([]string{"--agent-allowed-ids", "../other"}); err == nil {
		t.Fatal("unsafe routing ID accepted from CLI")
	}
}
