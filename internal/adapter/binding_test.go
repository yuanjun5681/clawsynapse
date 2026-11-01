package adapter

import "testing"

func testCodexConfig(t *testing.T, cfg CodexConfig) CodexConfig {
	t.Helper()
	if cfg.BotID == "" {
		cfg.BotID = "test-bot"
	}
	if cfg.BindingRevision == "" {
		cfg.BindingRevision = "test-binding"
	}
	if cfg.Workspace == "" {
		cfg.Workspace = privateTestDir(t)
	}
	return cfg
}
func testOpenCodeConfig(t *testing.T, cfg OpenCodeConfig) OpenCodeConfig {
	t.Helper()
	if cfg.BotID == "" {
		cfg.BotID = "test-bot"
	}
	if cfg.BindingRevision == "" {
		cfg.BindingRevision = "test-binding"
	}
	if cfg.Workspace == "" {
		cfg.Workspace = privateTestDir(t)
	}
	return cfg
}
func testOpenClawConfig(t *testing.T, cfg OpenClawConfig) OpenClawConfig {
	t.Helper()
	if cfg.BotID == "" {
		cfg.BotID = "test-bot"
	}
	if cfg.BindingRevision == "" {
		cfg.BindingRevision = "test-binding"
	}
	if cfg.Workspace == "" {
		cfg.Workspace = privateTestDir(t)
	}
	return cfg
}
func testWebhookConfig(t *testing.T, cfg WebhookConfig) WebhookConfig {
	t.Helper()
	if cfg.BotID == "" {
		cfg.BotID = "test-bot"
	}
	if cfg.BindingRevision == "" {
		cfg.BindingRevision = "test-binding"
	}
	if cfg.Instance == "" {
		cfg.Instance = "test-receiver"
	}
	return cfg
}
