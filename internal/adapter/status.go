package adapter

import (
	"context"
	"encoding/json"
	"strings"
)

// Readiness checks never run a model turn. Help validates the CLI schema, while
// login/health probes check only the product's non-interactive readiness surface.
func (a *cliAdapter) GetStatus(ctx context.Context) (*AgentStatus, error) {
	var helpArgs, required []string
	switch a.product {
	case "codex":
		helpArgs = []string{"exec", "--help"}
		required = []string{"--json", "--skip-git-repo-check", "stdin"}
	case "opencode":
		helpArgs = []string{"run", "--help"}
		required = []string{"--format", "--session", "--agent", "--standalone"}
	default:
		helpArgs = []string{"agent", "--help"}
		required = []string{"--json", "--message", "--session-id", "--agent"}
	}
	help, err := captureCommand(ctx, a.runCommand, helpArgs...)
	if err != nil {
		return &AgentStatus{Detail: "native command help is unavailable"}, err
	}
	helpText := strings.ToLower(string(help))
	for _, flag := range required {
		if !strings.Contains(helpText, flag) {
			return &AgentStatus{Degraded: true, Detail: "installed CLI does not advertise the required native command schema"}, nil
		}
	}
	switch a.product {
	case "codex":
		resume, err := captureCommand(ctx, a.runCommand, "exec", "resume", "--help")
		if err != nil {
			return &AgentStatus{Degraded: true, Detail: "resume help is unavailable"}, err
		}
		if !strings.Contains(string(resume), "--json") || !strings.Contains(strings.ToLower(string(resume)), "stdin") {
			return &AgentStatus{Degraded: true, Detail: "installed CLI does not advertise JSONL stdin session resumption"}, nil
		}
		if _, err := captureCommand(ctx, a.runCommand, "login", "status"); err != nil {
			return &AgentStatus{Degraded: true, Detail: "non-interactive login status is unavailable or not authenticated"}, err
		}
		return &AgentStatus{Healthy: true, Detail: "native JSONL/stdin schema and non-interactive login status checked; no model task executed"}, nil
	case "opencode":
		out, err := captureCommand(ctx, a.runCommand, "auth", "list")
		if err != nil {
			return &AgentStatus{Degraded: true, Detail: "non-interactive credential listing is unsupported or failed"}, err
		}
		text := strings.ToLower(strings.TrimSpace(string(out)))
		if text == "" || strings.Contains(text, "0 credentials") || strings.Contains(text, "no credentials") {
			return &AgentStatus{Degraded: true, Detail: "native event schema is available; provider authentication cannot be established from credential listing"}, nil
		}
		// A credential listing cannot prove that a configured provider accepts
		// those credentials; expose that limitation rather than claiming readiness.
		return &AgentStatus{Healthy: true, Degraded: true, Detail: "native event schema and credential listing checked; provider connectivity and model execution not probed"}, nil
	default:
		out, err := captureCommand(ctx, a.runCommand, "health", "--json")
		if err != nil {
			return &AgentStatus{Degraded: true, Detail: "non-interactive Gateway health is unsupported or failed"}, err
		}
		var status struct {
			OK *bool `json:"ok"`
		}
		if json.Unmarshal(out, &status) != nil || status.OK == nil {
			return &AgentStatus{Degraded: true, Detail: "Gateway health schema is unsupported; native agent help only was checked"}, nil
		}
		return &AgentStatus{Healthy: *status.OK, Detail: "native agent schema and Gateway health checked; no model task executed"}, nil
	}
}
