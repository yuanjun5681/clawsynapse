package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type OpenCodeConfig struct {
	NodeID string
	Logger *slog.Logger
	// AllowAgentIDs is local authorization, not a remotely supplied selector.
	AllowAgentIDs []string
	SessionStore  *store.FSStore
}

// OpenCodeAdapter consumes run --format json events, exposing only text parts.
type OpenCodeAdapter struct{ *cliAdapter }

func NewOpenCodeAdapter(cfg OpenCodeConfig) (*OpenCodeAdapter, error) {
	return &OpenCodeAdapter{newCLIAdapter("opencode", cfg.NodeID, cfg.Logger, cfg.SessionStore, cfg.AllowAgentIDs)}, nil
}
