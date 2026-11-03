package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type OMPConfig struct {
	BotID, BindingRevision, Workspace, Model string
	Logger                                   *slog.Logger
	SessionStore                             *store.FSStore
}

// OMPAdapter runs native JSONL print mode without named-agent routing.
type OMPAdapter struct{ *cliAdapter }

func NewOMPAdapter(cfg OMPConfig) (*OMPAdapter, error) {
	cli, err := newPiFamilyAdapter("omp", cfg.BotID, cfg.BindingRevision, cfg.Workspace, cfg.Model, cfg.Logger, cfg.SessionStore)
	if err != nil {
		return nil, err
	}
	return &OMPAdapter{cli}, nil
}
