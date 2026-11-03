package adapter

import (
	"log/slog"

	"clawsynapse/internal/store"
)

type PiConfig struct {
	BotID, BindingRevision, Workspace, Model string
	Logger                                   *slog.Logger
	SessionStore                             *store.FSStore
}

// PiAdapter runs native JSONL print mode without named-agent routing.
type PiAdapter struct{ *cliAdapter }

func NewPiAdapter(cfg PiConfig) (*PiAdapter, error) {
	cli, err := newPiFamilyAdapter("pi", cfg.BotID, cfg.BindingRevision, cfg.Workspace, cfg.Model, cfg.Logger, cfg.SessionStore)
	if err != nil {
		return nil, err
	}
	return &PiAdapter{cli}, nil
}
