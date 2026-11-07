package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/api"
	"clawsynapse/internal/config"
)

// Models queries global native configuration from an empty private prospective
// binding workspace. A desktop workspace ID is an identifier, not a filesystem
// path; its Console binding never selects an Agent Harness working directory.
func (m *Manager) Models(ctx context.Context, product, workspaceID, botID string) (adapter.ModelCatalog, error) {
	if workspaceID != "" && !validWorkspaceID(workspaceID) {
		return adapter.ModelCatalog{}, api.Failure(400, "invalid_argument", "invalid workspaceId")
	}
	if botID != "" {
		cfg, err := m.Config(botID)
		if err != nil {
			return adapter.ModelCatalog{}, err
		}
		if cfg.Harness == nil || cfg.Harness.Adapter != product {
			return adapter.ModelCatalog{}, api.Failure(400, "harness.models_context_invalid", "Bot does not use this Harness")
		}
	}
	return m.queryModels(ctx, product)
}

func (m *Manager) queryModels(ctx context.Context, product string) (adapter.ModelCatalog, error) {
	// This is the same parent used by createLocked. Never enter a desktop
	// project, existing session or existing binding workspace during discovery.
	dir, err := os.MkdirTemp(filepath.Join(m.cfg.DataDir, "bots"), ".models-")
	if err != nil {
		return adapter.ModelCatalog{}, api.Failure(503, "harness.models_unavailable", "could not prepare a private model query workspace")
	}
	defer os.RemoveAll(dir)
	query := m.modelQuery
	if query == nil {
		query = adapter.QueryModelCatalog
	}
	catalog, err := query(ctx, product, dir)
	if err != nil {
		var native *adapter.CatalogError
		if errors.As(err, &native) {
			status := 502
			switch native.Code {
			case "harness.models_unsupported", "harness.models_instance_managed":
				status = 400
			case "harness.models_timeout":
				status = 504
			case "harness.models_unavailable":
				status = 503
			}
			return adapter.ModelCatalog{}, api.Failure(status, native.Code, native.Message)
		}
		return adapter.ModelCatalog{}, api.Failure(502, "harness.models_failed", "native Harness model query failed")
	}
	return catalog, nil
}

func (m *Manager) validateExplicitModel(cfg config.BotConfig) error {
	if cfg.Harness == nil || cfg.Harness.Model == "" {
		return nil
	}
	catalog, err := m.queryModels(context.Background(), cfg.Harness.Adapter)
	if err != nil {
		return err
	}
	if !catalog.Contains(cfg.Harness.Model) {
		return api.Failure(400, "harness.model_invalid", "selected model is not in the native Harness model directory")
	}
	return nil
}

func explicitModelChanged(before, after *config.HarnessBinding) bool {
	return after != nil && after.Model != "" && (before == nil || before.Adapter != after.Adapter || before.Model != after.Model)
}
