package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type CodexConfig struct {
	BotID, BindingRevision, Workspace, Model string
	Logger                                   *slog.Logger
	SessionStore                             *store.FSStore
}

// CodexAdapter supports native exec JSONL and stdin prompts. Codex has no
// named-agent selector; nonempty AgentID is rejected before execution.
type CodexAdapter struct{ *cliAdapter }

func NewCodexAdapter(cfg CodexConfig) (*CodexAdapter, error) {
	cli, err := newCLIAdapter("codex", cfg.BotID, cfg.BindingRevision, cfg.Workspace, cfg.Model, cfg.Logger, cfg.SessionStore, nil)
	if err != nil {
		return nil, err
	}
	return &CodexAdapter{cli}, nil
}
