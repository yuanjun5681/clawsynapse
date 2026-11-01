package app

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/api"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
)

type workspaceClaim struct {
	WorkspaceID string `json:"workspaceId"`
}

func validWorkspaceID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func readWorkspaceClaim(dir string) (string, error) {
	path := filepath.Join(dir, "workspace-binding.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return "", errors.New("invalid workspace binding marker")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var claim workspaceClaim
	if err := protocol.DecodeStrict(data, &claim); err != nil {
		return "", err
	}
	if !validWorkspaceID(claim.WorkspaceID) {
		return "", errors.New("invalid workspace binding marker")
	}
	return claim.WorkspaceID, nil
}

func (m *Manager) validateWorkspaceBinding(botID, workspaceID string) error {
	claim, err := readWorkspaceClaim(m.botDir(botID))
	if err != nil {
		return err
	}
	if claim != workspaceID {
		return errors.New("workspace binding does not match catalog; restore binding before use")
	}
	return nil
}

// BindWorkspace never selects an existing identity by its mutable display name.
// The catalog commits the Bot and workspace together; a private marker detects a
// lost claim or an interrupted pre-commit creation rather than minting a new key.
func (m *Manager) BindWorkspace(workspaceID, displayName string) (api.BotInfo, string, error) {
	if !validWorkspaceID(workspaceID) || strings.TrimSpace(displayName) == "" {
		return api.BotInfo{}, "", api.Failure(400, "invalid_argument", "workspaceId must be 1-128 ASCII letters, digits, underscores or hyphens; displayName is required")
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return api.BotInfo{}, "", api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	var bound *managedBot
	for id, rec := range m.catalog {
		if rec.WorkspaceID == workspaceID {
			bound = m.bots[id]
			break
		}
	}
	if bound == nil {
		entries, err := os.ReadDir(filepath.Join(m.cfg.DataDir, "bots"))
		if err != nil {
			m.mu.Unlock()
			return api.BotInfo{}, "", err
		}
		for _, entry := range entries {
			claim, err := readWorkspaceClaim(filepath.Join(m.cfg.DataDir, "bots", entry.Name()))
			if err != nil || claim == workspaceID {
				m.mu.Unlock()
				return api.BotInfo{}, "", api.Failure(409, "workspace.binding_invalid", "workspace binding is missing or damaged; restore the catalog and original Bot data")
			}
		}
		cfg := config.DefaultBotConfig()
		cfg.DisplayName = strings.TrimSpace(displayName)
		cfg.Harness.Adapter = "console"
		cfg.TrustMode = "explicit"
		cfg.Labels = cfg.Labels.WithCaps([]string{"chat"})
		cfg.BindingRevision = randomID()
		info, err := m.createLocked(cfg, workspaceID)
		if err != nil {
			m.mu.Unlock()
			return api.BotInfo{}, "", err
		}
		bound = m.bots[info.BotID]
	}
	m.mu.Unlock()

	bound.mu.Lock()
	defer bound.mu.Unlock()
	invalid := func() (api.BotInfo, string, error) {
		return api.BotInfo{}, "", api.Failure(409, "workspace.binding_invalid", "workspace Console Bot is unavailable; restore its original binding, identity and credential")
	}
	if bound.info.Archived || bound.info.State == "failed" || bound.cfg.Harness.Adapter != "console" || !bound.hasToken {
		return invalid()
	}
	if err := m.validateWorkspaceBinding(bound.info.BotID, workspaceID); err != nil {
		return invalid()
	}
	dir := filepath.Join(m.botDir(bound.info.BotID), "credentials")
	id, err := identity.Load(filepath.Join(dir, "identity.key"), filepath.Join(dir, "identity.pub"))
	if err != nil || identity.DeriveBotDID(id.PublicKey) != bound.info.DID {
		return invalid()
	}
	token, err := loadPrivateToken(filepath.Join(dir, "api.token"))
	if err != nil || sha256.Sum256([]byte(token)) != bound.token {
		return invalid()
	}
	return bound.info, token, nil
}
