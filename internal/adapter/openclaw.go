package adapter

import "log/slog"

type OpenClawConfig struct {
	BotID, BindingRevision, Workspace, Model string
	Logger                                   *slog.Logger
	// AllowAgentIDs is local authorization, not a remotely supplied selector.
	AllowAgentIDs []string
}

// OpenClawAdapter uses the native Gateway-backed agent command, never shell
// dispatch or external delivery flags. Its JSON response is one-shot.
type OpenClawAdapter struct{ *cliAdapter }

func NewOpenClawAdapter(cfg OpenClawConfig) (*OpenClawAdapter, error) {
	cli, err := newCLIAdapter("openclaw", cfg.BotID, cfg.BindingRevision, cfg.Workspace, cfg.Model, cfg.Logger, nil, cfg.AllowAgentIDs)
	if err != nil {
		return nil, err
	}
	return &OpenClawAdapter{cli}, nil
}
