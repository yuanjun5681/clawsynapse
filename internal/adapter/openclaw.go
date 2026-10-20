package adapter

import "log/slog"

type OpenClawConfig struct {
	NodeID string
	Logger *slog.Logger
	// AllowAgentIDs is local authorization, not a remotely supplied selector.
	AllowAgentIDs []string
}

// OpenClawAdapter uses the native Gateway-backed agent command, never shell
// dispatch or external delivery flags. Its JSON response is one-shot.
type OpenClawAdapter struct{ *cliAdapter }

func NewOpenClawAdapter(cfg OpenClawConfig) (*OpenClawAdapter, error) {
	return &OpenClawAdapter{newCLIAdapter("openclaw", cfg.NodeID, cfg.Logger, nil, cfg.AllowAgentIDs)}, nil
}
