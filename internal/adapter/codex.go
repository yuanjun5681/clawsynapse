package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type CodexConfig struct {
	NodeID       string
	Logger       *slog.Logger
	SessionStore *store.FSStore
}

// CodexAdapter supports native exec JSONL and stdin prompts. Codex has no
// named-agent selector; nonempty AgentID is rejected before execution.
type CodexAdapter struct{ *cliAdapter }

func NewCodexAdapter(cfg CodexConfig) (*CodexAdapter, error) {
	return &CodexAdapter{newCLIAdapter("codex", cfg.NodeID, cfg.Logger, cfg.SessionStore, nil)}, nil
}
