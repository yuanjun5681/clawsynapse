package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/config"
)

func TestRunInitWritesDaemonOnlyConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	data := filepath.Join(root, "data")
	if err := runInit([]string{"--config", path, "--data-dir", data, "--nats-servers", "nats://127.0.0.1:4222,nats://127.0.0.1:4223"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.DaemonConfig
	if err = config.DecodeStrict(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != 2 || cfg.DataDir != data || cfg.AdminTokenPath != filepath.Join(data, "admin.token") || len(cfg.NATSServers) != 2 {
		t.Fatalf("unexpected daemon config: %#v", cfg)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"botId", "nodeId", "harness", "identityKeyPath", "trustMode", "agentAdapter", "transferDir"} {
		if _, ok := fields[key]; ok {
			t.Fatalf("init generated Bot field %q", key)
		}
	}
	entries, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("init created identity/state: %v", entries)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration is not private")
	}
}

func TestRunInitRejectsBotConfigurationFlags(t *testing.T) {
	err := runInit([]string{"--agent-adapter", "opencode"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("daemon init accepted a Bot binding")
	}
}

func TestRunInitRequiresOverwriteForExistingConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runInit([]string{"--config", path, "--data-dir", root}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "use --overwrite") {
		t.Fatalf("unexpected error: %v", err)
	}
}
