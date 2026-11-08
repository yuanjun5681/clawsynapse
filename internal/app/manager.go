package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/api"
	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

type catalogRecord struct {
	BotID        string `json:"botId"`
	DesiredState string `json:"desiredState"`
	Archived     bool   `json:"archived"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
}
type catalog struct {
	SchemaVersion int             `json:"schemaVersion"`
	Bots          []catalogRecord `json:"bots"`
}

var errBotConfigFormat = errors.New("unsupported Bot configuration format")

type managedBot struct {
	mu          sync.Mutex
	info        api.BotInfo
	cfg         config.BotConfig
	runtime     *Runtime
	refs        sync.WaitGroup
	transition  sync.WaitGroup
	token       [32]byte
	hasToken    bool
	workspaceID string
}

type Manager struct {
	mu         sync.RWMutex
	cfg        config.DaemonConfig
	version    string
	bots       map[string]*managedBot
	catalog    map[string]catalogRecord
	bindings   map[string]*config.HarnessBinding
	tokens     map[string][32]byte
	blobs      *blob.Store
	scheduler  *adapter.Scheduler
	purges     sync.WaitGroup
	closing    bool
	recovery   []string
	modelQuery func(context.Context, string, string) (adapter.ModelCatalog, error)
}

func newManager(cfg config.DaemonConfig, version string, blobs *blob.Store) (*Manager, error) {
	m := &Manager{cfg: cfg, version: version, bots: map[string]*managedBot{}, catalog: map[string]catalogRecord{}, bindings: map[string]*config.HarnessBinding{}, tokens: map[string][32]byte{}, blobs: blobs, scheduler: adapter.NewScheduler(cfg.MaxConcurrent)}
	path := filepath.Join(cfg.DataDir, "bots.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		entries, inspectErr := os.ReadDir(filepath.Join(cfg.DataDir, "bots"))
		if inspectErr != nil {
			return nil, inspectErr
		}
		if len(entries) != 0 {
			return nil, errors.New("Bot catalog is missing while Bot data exists; restore the catalog before starting")
		}
		if err = store.WriteJSONAtomic(path, catalog{SchemaVersion: 3, Bots: []catalogRecord{}}, 0600); err != nil {
			return nil, err
		}
		data = []byte(`{"schemaVersion":3,"bots":[]}`)
	} else if err != nil {
		return nil, err
	}
	var c catalog
	if err = protocol.DecodeStrictLimit(data, &c, 64<<20); err != nil {
		return nil, fmt.Errorf("load Bot catalog: %w", err)
	}
	if c.SchemaVersion != 3 || c.Bots == nil {
		return nil, fmt.Errorf("invalid or unsupported Bot catalog")
	}
	workspaceIDs := make(map[string]bool)
	for _, rec := range c.Bots {
		if !validBotID(rec.BotID) || (rec.DesiredState != "running" && rec.DesiredState != "stopped") || (rec.Archived && rec.DesiredState != "stopped") {
			return nil, fmt.Errorf("invalid Bot catalog entry")
		}
		if rec.WorkspaceID != "" {
			if !validWorkspaceID(rec.WorkspaceID) || workspaceIDs[rec.WorkspaceID] {
				return nil, errors.New("invalid or duplicate workspace binding")
			}
			workspaceIDs[rec.WorkspaceID] = true
		}
		if _, exists := m.bots[rec.BotID]; exists {
			return nil, fmt.Errorf("duplicate Bot catalog identity")
		}
		b := &managedBot{workspaceID: rec.WorkspaceID, info: api.BotInfo{BotID: rec.BotID, DesiredState: rec.DesiredState, State: "stopped", Archived: rec.Archived}}
		if rec.Archived {
			b.info.State = "archived"
		}
		m.bots[rec.BotID] = b
		m.catalog[rec.BotID] = rec
		if err = m.loadEntry(b); err != nil {
			if errors.Is(err, errBotConfigFormat) {
				return nil, fmt.Errorf("Bot %s: %w", rec.BotID, err)
			}
			b.info.State = "failed"
			b.info.Error = err.Error()
		}
		m.bindings[rec.BotID] = b.cfg.Harness
		if b.hasToken {
			m.tokens[rec.BotID] = b.token
		}
	}
	entries, err := os.ReadDir(filepath.Join(cfg.DataDir, "bots"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if _, ok := m.bots[entry.Name()]; !ok {
			m.recovery = append(m.recovery, entry.Name())
		}
	}
	return m, nil
}
func validBotID(id string) bool {
	if !strings.HasPrefix(id, "n1-") || len(id) > 128 || len(id) < 4 {
		return false
	}
	for _, c := range id[3:] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func (m *Manager) botDir(id string) string { return filepath.Join(m.cfg.DataDir, "bots", id) }
func (m *Manager) loadEntry(b *managedBot) error {
	b.hasToken = false
	dir := m.botDir(b.info.BotID)
	if err := store.EnsurePrivateDir(dir); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return fmt.Errorf("load Bot config: %w", err)
	}
	// A schema-3 catalog cannot activate a mixed legacy Bot layout. Other
	// damaged configuration remains an isolated failed Bot as before.
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) == nil && fields != nil {
		if _, present := fields["role"]; !present {
			return fmt.Errorf("%w: role is missing; run offline migration", errBotConfigFormat)
		}
		var legacy struct {
			Adapter string `json:"adapter"`
		}
		if json.Unmarshal(fields["harness"], &legacy) == nil && legacy.Adapter == "console" {
			return fmt.Errorf("%w: console Harness requires offline migration", errBotConfigFormat)
		}
		if _, present := fields["revision"]; !present {
			return errors.New("persisted Bot configuration is missing revision")
		}
	}
	var cfg config.BotConfig
	if err = config.DecodeStrict(data, &cfg); err != nil {
		return fmt.Errorf("parse Bot config: %w", err)
	}
	if err = config.ValidateBot(cfg); err != nil {
		return fmt.Errorf("validate Bot config: %w", err)
	}
	b.cfg = cfg
	b.info.DisplayName = b.cfg.DisplayName
	b.info.Revision = b.cfg.Revision
	b.info.Role = b.cfg.Role
	b.info.HarnessApplicable = b.cfg.Role == config.RoleAgent
	b.info.BindingRevision = ""
	if b.cfg.Role == config.RoleAgent {
		b.info.BindingRevision = b.cfg.BindingRevision
	}
	id, err := identity.Load(filepath.Join(dir, "credentials", "identity.key"), filepath.Join(dir, "credentials", "identity.pub"))
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}
	did := identity.DeriveBotDID(id.PublicKey)
	if identity.DeriveBotID(did) != b.info.BotID {
		return errors.New("identity does not match catalog")
	}
	if err := m.validateWorkspaceBinding(b.info.BotID, b.workspaceID); err != nil {
		return err
	}
	if b.workspaceID != "" && b.cfg.Role != config.RoleConsole {
		return errors.New("workspace binding requires a Console Bot")
	}
	b.info.DID = did
	token, err := loadPrivateToken(filepath.Join(dir, "credentials", "api.token"))
	if err != nil {
		return err
	}
	b.token = sha256.Sum256([]byte(token))
	b.hasToken = true
	return nil
}
func loadPrivateToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return "", errors.New("credential must be a regular 0600 file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 || strings.ContainsAny(token, " \r\n\t") {
		return "", errors.New("invalid Bot credential")
	}
	return token, nil
}
func (m *Manager) entry(id string) (*managedBot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.bots[id]
	if !ok {
		return nil, api.Failure(404, "bot.not_found", "unknown Bot")
	}
	return b, nil
}
func (m *Manager) List() []api.BotInfo {
	m.mu.RLock()
	entries := make([]*managedBot, 0, len(m.bots))
	for _, b := range m.bots {
		entries = append(entries, b)
	}
	m.mu.RUnlock()
	out := make([]api.BotInfo, 0, len(entries))
	for _, b := range entries {
		b.mu.Lock()
		if b.runtime != nil && b.info.State == "running" {
			b.info.NetworkHealthy = b.runtime.bus.Status().Connected
		}
		out = append(out, b.info)
		b.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BotID < out[j].BotID })
	return out
}
func (m *Manager) Info(id string) (api.BotInfo, error) {
	b, err := m.entry(id)
	if err != nil {
		return api.BotInfo{}, err
	}
	b.mu.Lock()
	if b.info.State == "deleting" {
		b.mu.Unlock()
		return api.BotInfo{}, api.Failure(409, "bot.busy", "Bot is deleting")
	}
	info := b.info
	r := b.runtime
	if r != nil && b.info.State == "running" {
		b.refs.Add(1)
	} else {
		r = nil
	}
	b.mu.Unlock()
	if r != nil {
		defer b.refs.Done()
		info.NetworkHealthy = r.bus.Status().Connected
		if info.Role == config.RoleAgent && r.harness != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			status, err := r.harness.GetStatus(ctx)
			info.HarnessHealthy = err == nil && status != nil && status.Healthy
		}
		b.mu.Lock()
		if b.runtime == r && b.info.State == "running" {
			b.info.NetworkHealthy = info.NetworkHealthy
			b.info.HarnessHealthy = info.HarnessHealthy
		}
		b.mu.Unlock()
	}
	return info, nil
}
func cloneConfig(cfg config.BotConfig) config.BotConfig {
	cfg.Labels = maps.Clone(cfg.Labels)
	cfg.DeliverablePrefixes = slices.Clone(cfg.DeliverablePrefixes)
	if cfg.Harness != nil {
		h := *cfg.Harness
		h.AllowedAgentIDs = slices.Clone(h.AllowedAgentIDs)
		cfg.Harness = &h
	}
	return cfg
}
func (m *Manager) Config(id string) (config.BotConfig, error) {
	b, err := m.entry(id)
	if err != nil {
		return config.BotConfig{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.info.State == "deleting" {
		return config.BotConfig{}, api.Failure(409, "bot.busy", "Bot is deleting")
	}
	return cloneConfig(b.cfg), nil
}
func (m *Manager) Authenticate(token string) (string, bool) {
	actual := sha256.Sum256([]byte(token))
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, expected := range m.tokens {
		if subtle.ConstantTimeCompare(actual[:], expected[:]) == 1 {
			return id, true
		}
	}
	return "", false
}
func randomID() string { return rand.Text() }
func (m *Manager) saveCatalogLocked() error {
	c := catalog{SchemaVersion: 3, Bots: make([]catalogRecord, 0, len(m.catalog))}
	for _, rec := range m.catalog {
		c.Bots = append(c.Bots, rec)
	}
	sort.Slice(c.Bots, func(i, j int) bool { return c.Bots[i].BotID < c.Bots[j].BotID })
	return store.WriteJSONAtomic(filepath.Join(m.cfg.DataDir, "bots.json"), c, 0600)
}

// Caller holds the entry lock; only short persistent management work holds the directory lock.
func (m *Manager) desiredLocked(b *managedBot, desired string, archived bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	if desired == "running" || (b.info.Archived && !archived) {
		if err := m.checkBindingLocked(b.info.BotID, b.cfg.Harness); err != nil {
			return err
		}
	}
	old := m.catalog[b.info.BotID]
	next := old
	next.DesiredState, next.Archived = desired, archived
	m.catalog[b.info.BotID] = next
	if err := m.saveCatalogLocked(); err != nil {
		m.catalog[b.info.BotID] = old
		return err
	}
	b.info.DesiredState = desired
	b.info.Archived = archived
	return nil
}
func (m *Manager) checkBindingLocked(id string, h *config.HarnessBinding) error {
	if h == nil || (h.Adapter != "webhook" && h.Adapter != "openclaw") {
		return nil
	}
	if h.Instance == "" {
		return api.Failure(400, "bot.binding_invalid", "shared-process Harness requires an explicit instance identifier")
	}
	for other, binding := range m.bindings {
		if other != id && binding != nil && !m.catalog[other].Archived && binding.Adapter == h.Adapter && binding.Instance == h.Instance {
			return api.Failure(409, "bot.binding_shared", "Harness instance sharing cannot guarantee execution isolation")
		}
	}
	return nil
}
func (m *Manager) Create(cfg config.BotConfig) (api.BotInfo, error) {
	if strings.TrimSpace(cfg.DisplayName) == "" {
		return api.BotInfo{}, api.Failure(400, "bot.config_invalid", "displayName is required when creating a Bot")
	}
	cfg.Revision = 1
	cfg.BindingRevision = ""
	if cfg.Role == config.RoleAgent {
		cfg.BindingRevision = randomID()
	}
	if err := config.ValidateBot(cfg); err != nil {
		return api.BotInfo{}, api.Failure(400, "bot.config_invalid", err.Error())
	}
	if err := m.validateExplicitModel(cfg); err != nil {
		return api.BotInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(cfg, "")
}

// createLocked commits the identity and optional workspace claim in one catalog write.
func (m *Manager) createLocked(cfg config.BotConfig, workspaceID string) (api.BotInfo, error) {
	if m.closing {
		return api.BotInfo{}, api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	if err := m.checkBindingLocked("", cfg.Harness); err != nil {
		return api.BotInfo{}, err
	}
	staging, err := os.MkdirTemp(filepath.Join(m.cfg.DataDir, "bots"), ".create-")
	if err != nil {
		return api.BotInfo{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	credentials := filepath.Join(staging, "credentials")
	if err = store.EnsurePrivateDir(credentials); err != nil {
		return api.BotInfo{}, err
	}
	id, err := identity.Create(filepath.Join(credentials, "identity.key"), filepath.Join(credentials, "identity.pub"))
	if err != nil {
		return api.BotInfo{}, err
	}
	botID := identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey))
	if _, exists := m.bots[botID]; exists {
		return api.BotInfo{}, errors.New("new identity collides with registered Bot")
	}
	fs := store.NewFSStore(staging)
	if err = fs.EnsureLayout(); err != nil {
		return api.BotInfo{}, err
	}
	if err = store.EnsurePrivateDir(filepath.Join(staging, "workspace", cfg.BindingRevision)); err != nil {
		return api.BotInfo{}, err
	}
	if err = store.WriteJSONAtomic(filepath.Join(staging, "config.json"), cfg, 0600); err != nil {
		return api.BotInfo{}, err
	}
	if err = writeToken(filepath.Join(credentials, "api.token")); err != nil {
		return api.BotInfo{}, err
	}
	if workspaceID != "" {
		if err = store.WriteJSONAtomic(filepath.Join(staging, "workspace-binding.json"), workspaceClaim{WorkspaceID: workspaceID}, 0600); err != nil {
			return api.BotInfo{}, err
		}
	}
	destination := m.botDir(botID)
	if _, err = os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return api.BotInfo{}, errors.New("Bot directory already exists")
	}
	if err = os.Rename(staging, destination); err != nil {
		return api.BotInfo{}, err
	}
	published = true
	if err = syncDir(filepath.Dir(destination)); err != nil {
		return api.BotInfo{}, err
	}
	b := &managedBot{workspaceID: workspaceID, cfg: cfg, info: api.BotInfo{BotID: botID, DesiredState: "stopped", State: "stopped"}}
	m.catalog[botID] = catalogRecord{BotID: botID, DesiredState: "stopped", WorkspaceID: workspaceID}
	if err = m.loadEntry(b); err != nil {
		delete(m.catalog, botID)
		m.recovery = append(m.recovery, botID)
		return api.BotInfo{}, err
	}
	if err = m.saveCatalogLocked(); err != nil {
		delete(m.catalog, botID)
		m.recovery = append(m.recovery, botID)
		return api.BotInfo{}, err
	}
	m.bots[botID] = b
	m.bindings[botID] = b.cfg.Harness
	m.tokens[botID] = b.token
	return b.info, nil
}
func writeToken(path string) error {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(hex.EncodeToString(raw[:]) + "\n"); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDir(filepath.Dir(path))
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (m *Manager) Update(id string, cfg config.BotConfig) (api.BotInfo, error) {
	b, err := m.entry(id)
	if err != nil {
		return api.BotInfo{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.info.State == "deleting" {
		return api.BotInfo{}, api.Failure(409, "bot.busy", "Bot is deleting")
	}
	if b.info.State == "running" || b.info.State == "starting" || b.info.State == "stopping" {
		return api.BotInfo{}, api.Failure(409, "bot.must_stop", "stop Bot before changing configuration")
	}
	if cfg.Revision != b.cfg.Revision {
		return api.BotInfo{}, api.Failure(409, "bot.revision_conflict", "configuration revision changed")
	}
	if cfg.Role != b.cfg.Role {
		return api.BotInfo{}, api.Failure(400, "bot.role_immutable", "Role cannot change after Bot creation")
	}
	// Binding identity is server controlled. A changed binding never revives old sessions.
	cfg.BindingRevision = b.cfg.BindingRevision
	if cfg.Role == config.RoleAgent && !reflect.DeepEqual(cfg.Harness, b.cfg.Harness) {
		cfg.BindingRevision = randomID()
	}
	cfg.Revision++
	if err = config.ValidateBot(cfg); err != nil {
		return api.BotInfo{}, api.Failure(400, "bot.config_invalid", err.Error())
	}
	if explicitModelChanged(b.cfg.Harness, cfg.Harness) {
		if err := m.validateExplicitModel(cfg); err != nil {
			return api.BotInfo{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return api.BotInfo{}, api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	if err = m.checkBindingLocked(id, cfg.Harness); err != nil {
		return api.BotInfo{}, err
	}
	if err = store.WriteJSONAtomic(filepath.Join(m.botDir(id), "config.json"), cfg, 0600); err != nil {
		return api.BotInfo{}, err
	}
	b.cfg = cloneConfig(cfg)
	m.bindings[id] = b.cfg.Harness
	b.info.Revision = cfg.Revision
	b.info.BindingRevision = ""
	if cfg.Role == config.RoleAgent {
		b.info.BindingRevision = cfg.BindingRevision
	}
	b.info.DisplayName = cfg.DisplayName
	b.info.Role = cfg.Role
	b.info.HarnessApplicable = cfg.Role == config.RoleAgent
	// Configuration repair does not claim failed keys/state are repaired.
	return b.info, nil
}
func (m *Manager) Transition(id, op string) (api.BotInfo, error) {
	b, err := m.entry(id)
	if err != nil {
		return api.BotInfo{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.info.State == "deleting" {
		return api.BotInfo{}, api.Failure(409, "bot.busy", "Bot is deleting")
	}
	m.mu.RLock()
	closing := m.closing
	m.mu.RUnlock()
	if closing {
		return api.BotInfo{}, api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	switch op {
	case "start":
		if b.info.Archived {
			return api.BotInfo{}, api.Failure(409, "bot.archived", "restore archived Bot before starting")
		}
		if b.info.State == "stopping" {
			return api.BotInfo{}, api.Failure(409, "bot.transition_conflict", "Bot is stopping")
		}
		if b.info.State == "running" || b.info.State == "starting" {
			return b.info, nil
		}
		if err = m.desiredLocked(b, "running", false); err != nil {
			return api.BotInfo{}, err
		}
		b.info.State = "starting"
		b.info.Error = ""
		b.transition.Add(1)
		go m.start(b)
	case "stop":
		if b.info.State == "starting" {
			return api.BotInfo{}, api.Failure(409, "bot.transition_conflict", "Bot is starting")
		}
		if err = m.desiredLocked(b, "stopped", b.info.Archived); err != nil {
			return api.BotInfo{}, err
		}
		if b.info.State == "stopping" {
			return b.info, nil
		}
		if b.runtime == nil {
			if b.info.Archived {
				b.info.State = "archived"
			} else {
				b.info.State = "stopped"
			}
			return b.info, nil
		}
		b.info.State = "stopping"
		b.transition.Add(1)
		go m.stop(b)
	case "archive":
		if b.runtime != nil || b.info.State == "starting" || b.info.State == "stopping" {
			return api.BotInfo{}, api.Failure(409, "bot.must_stop", "stop Bot and wait before archiving")
		}
		if err = m.desiredLocked(b, "stopped", true); err != nil {
			return api.BotInfo{}, err
		}
		b.info.State = "archived"
	case "restore":
		if b.runtime != nil || b.info.State == "starting" || b.info.State == "stopping" {
			return api.BotInfo{}, api.Failure(409, "bot.transition_conflict", "Bot is transitioning")
		}
		if err = m.desiredLocked(b, "stopped", false); err != nil {
			return api.BotInfo{}, err
		}
		b.info.State = "stopped"
	default:
		return api.BotInfo{}, api.Failure(400, "invalid_argument", "unknown lifecycle operation")
	}
	return b.info, nil
}
func (m *Manager) start(b *managedBot) {
	defer b.transition.Done()
	b.mu.Lock()
	err := m.loadEntry(b)
	cfg := cloneConfig(b.cfg)
	id := b.info.BotID
	token, hasToken := b.token, b.hasToken
	b.mu.Unlock()
	m.mu.Lock()
	delete(m.tokens, id)
	if hasToken {
		m.tokens[id] = token
	}
	if err == nil {
		err = m.checkBindingLocked(id, cfg.Harness)
		if err == nil {
			m.bindings[id] = cfg.Harness
		}
	}
	m.mu.Unlock()
	var r *Runtime
	if err == nil {
		r, err = NewRuntime(m.cfg, cfg, id, m.version, m.blobs, m.scheduler)
	}
	if err == nil {
		err = r.Start(context.Background())
	}
	if err != nil && r != nil {
		r.Close()
	}
	harnessHealthy := false
	if err == nil && cfg.Role == config.RoleAgent && r.harness != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		status, probeErr := r.harness.GetStatus(ctx)
		cancel()
		harnessHealthy = probeErr == nil && status != nil && status.Healthy
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.info.State = "failed"
		b.info.Error = err.Error()
		slog.Error("Bot startup failed", "botId", id, "error", err)
		return
	}
	b.runtime = r
	b.info.State = "running"
	b.info.NetworkHealthy = r.bus.Status().Connected
	b.info.HarnessHealthy = harnessHealthy
}
func (m *Manager) stop(b *managedBot) {
	defer b.transition.Done()
	b.refs.Wait()
	b.mu.Lock()
	r := b.runtime
	b.mu.Unlock()
	if r != nil {
		r.Close()
	}
	b.mu.Lock()
	b.runtime = nil
	b.info.State = "stopped"
	b.info.NetworkHealthy = false
	b.info.HarnessHealthy = false
	b.mu.Unlock()
}

func (m *Manager) Acquire(id string, write bool) (http.Handler, func(), error) {
	b, err := m.entry(id)
	if err != nil {
		return nil, nil, err
	}
	b.mu.Lock()
	if b.info.State == "deleting" {
		b.mu.Unlock()
		return nil, nil, api.Failure(409, "bot.busy", "Bot is deleting")
	}
	if b.info.State == "running" && b.runtime != nil {
		b.refs.Add(1)
		handler := b.runtime.Handler()
		b.mu.Unlock()
		return handler, b.refs.Done, nil
	}
	if write || b.info.State == "starting" || b.info.State == "stopping" {
		b.mu.Unlock()
		return nil, nil, api.Failure(409, "bot.must_start", "Bot must be running")
	}
	// A stopped-state reader holds the entry lock until its journal is closed. A
	// start cannot race this independent read context into a second journal owner.
	handler, close, err := m.offline(b)
	if err != nil {
		b.mu.Unlock()
		return nil, nil, err
	}
	return handler, func() { close(); b.mu.Unlock() }, nil
}
func (m *Manager) Resume() {
	m.mu.RLock()
	entries := make([]*managedBot, 0, len(m.bots))
	for _, b := range m.bots {
		entries = append(entries, b)
	}
	m.mu.RUnlock()
	for _, b := range entries {
		b.mu.Lock()
		m.mu.RLock()
		closing := m.closing
		m.mu.RUnlock()
		if !closing && b.info.DesiredState == "running" && !b.info.Archived && (b.info.State == "stopped" || b.info.State == "failed") && b.runtime == nil {
			b.info.State = "starting"
			b.transition.Add(1)
			go m.start(b)
		}
		b.mu.Unlock()
	}
}

// Shutdown preserves desired state for crash/restart recovery, unlike an explicit stop.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	entries := make([]*managedBot, 0, len(m.bots))
	for _, b := range m.bots {
		entries = append(entries, b)
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, b := range entries {
			wg.Add(1)
			go func(b *managedBot) {
				defer wg.Done()
				// A previously accepted lifecycle call may still hold the entry
				// lock between persisting intent and registering its worker.
				// Closing rejects later calls; join that registration before Wait.
				b.mu.Lock()
				b.mu.Unlock()
				b.transition.Wait()
				b.mu.Lock()
				if b.runtime == nil {
					b.mu.Unlock()
					return
				}
				b.info.State = "stopping"
				b.mu.Unlock()
				b.refs.Wait()
				b.mu.Lock()
				r := b.runtime
				b.mu.Unlock()
				r.Close()
				b.mu.Lock()
				b.runtime = nil
				b.info.State = "stopped"
				b.mu.Unlock()
			}(b)
		}
		wg.Wait()
		// A purge can remove its Bot before this shutdown snapshots the catalog.
		// Closing prevents new registrations; join removed Bots' cleanup as well.
		m.purges.Wait()
		m.scheduler.Close()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) RecoveryIssues() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.recovery...)
}
func (m *Manager) Version() string { return m.version }
