package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type OpenCodeConfig struct {
	BotID, BindingRevision, Workspace, Model string
	Logger                                   *slog.Logger
	// AllowAgentIDs is local authorization, not a remotely supplied selector.
	AllowAgentIDs []string
	SessionStore  *store.FSStore
}

// OpenCodeAdapter consumes run --format json events, exposing only text parts.
type OpenCodeAdapter struct{ *cliAdapter }

func NewOpenCodeAdapter(cfg OpenCodeConfig) (*OpenCodeAdapter, error) {
	cli, err := newCLIAdapter("opencode", cfg.BotID, cfg.BindingRevision, cfg.Workspace, cfg.Model, cfg.Logger, cfg.SessionStore, cfg.AllowAgentIDs)
	if err != nil {
		return nil, err
	}
	return &OpenCodeAdapter{cli}, nil
}
