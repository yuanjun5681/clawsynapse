package migration

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/store"
)

// roleTransition is a publication journal, not a layout schema. Both copies must
// be committed before a daemon may accept writes. Recovery always rolls forward
// after preparation, so it can never overwrite new history with an old backup.
type roleTransition struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Status        string `json:"status"`
	Source        string `json:"source"`
	Stage         string `json:"stage"`
	Backup        string `json:"backup"`
}

func roleMarker(root string) string { return root + ".role-migration.json" }

// CheckStartup must be called before creating the root and again after taking
// daemon.lock. An incomplete publication is resolved only by explicit Recover.
func CheckStartup(dataDir string) error {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	for _, path := range []string{roleMarker(root), filepath.Join(root, "role-migration.json")} {
		data, err := store.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var m roleTransition
		if err := decodeManifest(data, &m); err != nil {
			return err
		}
		if err := validateRoleMarker(root, m); err != nil {
			return err
		}
		if m.Status != "complete" && m.Status != "rolled-back" {
			return errors.New("role migration is incomplete; run migrate --recover before startup")
		}
		if path == roleMarker(root) && m.Status == "complete" {
			currentRaw, err := store.ReadFile(filepath.Join(root, "role-migration.json"))
			if err != nil {
				return fmt.Errorf("committed migration root is unavailable: %w", err)
			}
			var current roleTransition
			if err := decodeManifest(currentRaw, &current); err != nil {
				return err
			}
			if current.ID != m.ID || current.Status != "complete" {
				return errors.New("migration commit markers disagree; run migrate --recover")
			}
		}
		if path == filepath.Join(root, "role-migration.json") && m.Status == "complete" {
			sidecarRaw, err := store.ReadFile(roleMarker(root))
			if err != nil {
				return fmt.Errorf("migration sidecar is unavailable: %w", err)
			}
			var sidecar roleTransition
			if err := decodeManifest(sidecarRaw, &sidecar); err != nil {
				return err
			}
			if sidecar.ID != m.ID || sidecar.Status != "complete" {
				return errors.New("migration commit markers disagree; run migrate --recover")
			}
		}
	}
	data, err := store.ReadFile(root + ".migration.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var m marker
	if err := decodeManifest(data, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 2 || m.Target != root {
		return errors.New("invalid migration recovery marker")
	}
	if m.Status != "complete" && m.Status != "rolled-back" {
		return errors.New("migration is incomplete; run migrate --recover before startup")
	}
	return nil
}

func validateRoleMarker(root string, m roleTransition) error {
	parent := filepath.Dir(root)
	if m.SchemaVersion != 1 || m.Source != root || len(m.ID) != 32 || m.Stage == root || m.Backup == root || m.Stage == m.Backup || filepath.Dir(m.Stage) != parent || filepath.Dir(m.Backup) != parent || !strings.HasPrefix(filepath.Base(m.Stage), "."+filepath.Base(root)+".roles-") || m.Backup != root+".v2-backup-"+m.ID {
		return errors.New("invalid role migration recovery marker")
	}
	if _, err := hex.DecodeString(m.ID); err != nil {
		return errors.New("invalid role migration transaction ID")
	}
	switch m.Status {
	case "preparing", "prepared", "backed-up", "published", "complete", "rolled-back":
		return nil
	}
	return errors.New("invalid role migration recovery phase")
}

// Upgrade converts the entire stopped version 2 DataDir, including archived
// Bots, in a sibling stage. The original layout remains as a permanent backup.
// Repeating a completed upgrade does not rewrite configuration or Bindings.
func Upgrade(dataDir string) error {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if root == string(filepath.Separator) {
		return errors.New("filesystem root cannot be migrated")
	}
	if err := store.CheckPath(root); err != nil {
		return err
	}
	transaction, err := store.Lock(root + ".migration.lock")
	if err != nil {
		return err
	}
	defer transaction.Close()
	if err := CheckStartup(root); err != nil {
		return err
	}
	locks, err := lockLayout(root)
	if err != nil {
		return err
	}
	defer closeLocks(locks)
	d, cat, err := readLayout(root, 2)
	if err != nil {
		// An already-current layout is a no-op only if it is fully verifiable.
		if current, currentCat, currentErr := readLayout(root, 3); currentErr == nil {
			return validateRoleLayout(root, root, current, currentCat)
		}
		return err
	}
	size, err := treeSize(root)
	if err != nil {
		return err
	}
	available, err := availableBytes(filepath.Dir(root))
	if err != nil {
		return err
	}
	if available < uint64(size)+16*1024*1024 {
		return errors.New("insufficient disk space for role migration stage")
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), "."+filepath.Base(root)+".roles-")
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	m := roleTransition{SchemaVersion: 1, ID: id, Status: "preparing", Source: root, Stage: stage, Backup: root + ".v2-backup-" + id}
	if err := store.WriteJSONAtomic(roleMarker(root), m, 0600); err != nil {
		return err
	}
	if err := copyLayout(root, stage); err != nil {
		return err
	}
	stageLocks, err := lockLayout(stage)
	if err != nil {
		return err
	}
	defer closeLocks(stageLocks)
	for _, entry := range cat.Bots {
		path := filepath.Join(stage, "bots", entry.BotID, "config.json")
		raw, err := store.ReadFile(path)
		if err != nil {
			return err
		}
		converted, err := convertOldBot(raw)
		if err != nil {
			return fmt.Errorf("Bot %s: %w", entry.BotID, err)
		}
		if err := store.WriteAtomic(path, converted, fileMode(path)); err != nil {
			return err
		}
	}
	d.SchemaVersion = 3
	cat.SchemaVersion = 3
	if err := store.WriteJSONAtomic(filepath.Join(stage, "config.json"), d, fileMode(filepath.Join(stage, "config.json"))); err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "bots.json"), cat, fileMode(filepath.Join(stage, "bots.json"))); err != nil {
		return err
	}
	if err := validateRoleLayout(stage, root, d, cat); err != nil {
		return err
	}
	m.Status = "prepared"
	if err := writeRolePhase(m); err != nil {
		return err
	}
	return publishRoleTransition(m)
}

func fileMode(path string) fs.FileMode {
	if st, err := os.Stat(path); err == nil {
		return st.Mode().Perm()
	}
	return 0600
}

func copyLayout(source, destination string) error {
	if err := copyTree(source, destination, nil); err != nil {
		return err
	}
	// Unlike legacy relocation, the v2 transition preserves every original mode.
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		st, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(destination, rel), st.Mode().Perm())
	})
}

func closeLocks(locks []*store.FileLock) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Close()
	}
}

func lockLayout(root string) ([]*store.FileLock, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New("migration root is not a directory")
	}
	var locks []*store.FileLock
	add := func(path string) error {
		l, err := store.Lock(path)
		if err != nil {
			return err
		}
		locks = append(locks, l)
		return nil
	}
	if err := add(filepath.Join(root, "daemon.lock")); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, "bots"))
	if err != nil {
		closeLocks(locks)
		return nil, err
	}
	for _, entry := range entries {
		botRoot := filepath.Join(root, "bots", entry.Name())
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			closeLocks(locks)
			return nil, errors.New("invalid Bot directory")
		}
		if err := add(filepath.Join(botRoot, "runtime.lock")); err != nil {
			closeLocks(locks)
			return nil, err
		}
		journalLocks, err := lockLegacyJournals(botRoot)
		if err != nil {
			closeLocks(locks)
			return nil, err
		}
		locks = append(locks, journalLocks...)
	}
	return locks, nil
}

func readLayout(root string, version int) (config.DaemonConfig, catalog, error) {
	d := config.DefaultDaemonConfig()
	var cat catalog
	raw, err := store.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		return d, cat, err
	}
	if err := config.DecodeStrict(raw, &d); err != nil {
		return d, cat, err
	}
	if d.SchemaVersion != version {
		return d, cat, fmt.Errorf("expected daemon schemaVersion %d; explicit version 2 migration is required", version)
	}
	raw, err = store.ReadFile(filepath.Join(root, "bots.json"))
	if err != nil {
		return d, cat, err
	}
	if err := decodeManifest(raw, &cat); err != nil {
		return d, cat, err
	}
	if cat.SchemaVersion != version || cat.Bots == nil {
		return d, cat, errors.New("invalid or mixed Bot catalog version")
	}
	return d, cat, nil
}

// convertOldBot alone may infer a role. Decode into raw fields first: the new
// BotConfig decoder intentionally rejects old data before any defaults apply.
func convertOldBot(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := decodeManifest(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("missing old Bot configuration")
	}
	if _, exists := fields["role"]; exists {
		return nil, errors.New("version 2 Bot contains mixed role format")
	}
	if _, exists := fields["roles"]; exists {
		return nil, errors.New("version 2 Bot contains mixed roles format")
	}
	// A defined type deliberately does not inherit BotConfig.UnmarshalJSON.
	type oldBotConfig config.BotConfig
	var old oldBotConfig
	if err := config.DecodeStrict(raw, &old); err != nil {
		return nil, err
	}
	if old.MaxConcurrent <= 0 || old.MaxQueued < 0 {
		return nil, errors.New("invalid old execution budget")
	}
	if old.Revision == 0 || old.BindingRevision == "" {
		return nil, errors.New("old Bot is missing its revision or Binding revision")
	}
	var h config.HarnessBinding
	if len(fields["harness"]) == 0 || bytes.Equal(bytes.TrimSpace(fields["harness"]), []byte("null")) {
		return nil, errors.New("old Bot is missing Harness")
	}
	if err := config.DecodeStrict(fields["harness"], &h); err != nil {
		return nil, err
	}
	if timeout, err := time.ParseDuration(h.Timeout); err != nil || timeout <= 0 {
		return nil, errors.New("invalid old Harness timeout")
	}
	role := config.RoleAgent
	switch h.Adapter {
	case "console":
		if h.Model != "" || h.Instance != "" || h.WebhookURL != "" || h.CredentialsFile != "" || len(h.AllowedAgentIDs) != 0 {
			return nil, errors.New("invalid old Console Harness execution fields")
		}
		role = config.RoleConsole
		delete(fields, "harness")
		delete(fields, "maxConcurrent")
		delete(fields, "maxQueued")
	case "default", "opencode", "codex", "openclaw", "webhook":
	default:
		return nil, fmt.Errorf("unknown old Harness adapter %q", h.Adapter)
	}
	fields["role"], _ = json.Marshal(role)
	converted, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	var b config.BotConfig
	if err := config.DecodeStrict(converted, &b); err != nil {
		return nil, err
	}
	if err := config.ValidateBot(b); err != nil {
		return nil, err
	}
	// Keep original optional-field presence and all execution values exactly.
	return append(converted, '\n'), nil
}
func writeRolePhase(m roleTransition) error {
	root := m.Stage
	if m.Status == "published" || m.Status == "complete" {
		root = m.Source
	}
	if err := store.WriteJSONAtomic(filepath.Join(root, "role-migration.json"), m, 0600); err != nil {
		return err
	}
	if err := syncTree(root); err != nil {
		return err
	}
	return store.WriteJSONAtomic(roleMarker(m.Source), m, 0600)
}

func publishRoleTransition(m roleTransition) error {
	if m.Status == "prepared" {
		if _, err := os.Lstat(m.Backup); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(m.Source, m.Backup); err != nil {
				return err
			}
			if err := store.SyncDir(filepath.Dir(m.Source)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		m.Status = "backed-up"
		if err := writeRolePhase(m); err != nil {
			return err
		}
	}
	if m.Status == "backed-up" {
		if _, err := os.Lstat(m.Source); !errors.Is(err, os.ErrNotExist) {
			return errors.New("unexpected root during role migration publication")
		}
		if err := os.Rename(m.Stage, m.Source); err != nil {
			return err
		}
		if err := store.SyncDir(filepath.Dir(m.Source)); err != nil {
			return err
		}
		m.Status = "published"
		if err := writeRolePhase(m); err != nil {
			return err
		}
	}
	m.Status = "complete"
	return writeRolePhase(m)
}

func recoverRoles(root string) (bool, error) {
	raw, err := store.ReadFile(roleMarker(root))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	transaction, err := store.Lock(root + ".migration.lock")
	if err != nil {
		return true, err
	}
	defer transaction.Close()
	// Re-read under the transaction lock; another recovery may have completed.
	raw, err = store.ReadFile(roleMarker(root))
	if err != nil {
		return true, err
	}
	var m roleTransition
	if err := decodeManifest(raw, &m); err != nil {
		return true, err
	}
	if err := validateRoleMarker(root, m); err != nil {
		return true, err
	}
	if m.Status == "complete" || m.Status == "rolled-back" {
		return true, CheckStartup(root)
	}
	for _, path := range []string{root, m.Stage, m.Backup} {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return true, err
		}
		if path == m.Stage && m.Status == "preparing" {
			stageLock, err := store.Lock(filepath.Join(path, "daemon.lock"))
			if err != nil {
				return true, err
			}
			defer stageLock.Close()
			continue
		}
		locks, err := lockLayout(path)
		if err != nil {
			return true, err
		}
		defer closeLocks(locks)
	}
	if m.Status == "preparing" {
		// No publication can occur before the durable prepared sidecar.
		if _, err := os.Stat(root); err != nil {
			return true, err
		}
		m.Status = "rolled-back"
		return true, store.WriteJSONAtomic(roleMarker(root), m, 0600)
	}
	validationRoot := m.Stage
	if currentRaw, err := store.ReadFile(filepath.Join(root, "role-migration.json")); err == nil {
		var current roleTransition
		if err := decodeManifest(currentRaw, &current); err != nil {
			return true, err
		}
		if err := validateRoleMarker(root, current); err != nil {
			return true, err
		}
		if current.ID == m.ID {
			validationRoot = root
			m.Status = "published"
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return true, err
	}
	d, cat, err := readLayout(validationRoot, 3)
	if err != nil {
		return true, err
	}
	if err := validateRoleLayout(validationRoot, root, d, cat); err != nil {
		return true, err
	}
	return true, publishRoleTransition(m)
}

func resolveStaged(path, stage, final string) string {
	if filepath.IsAbs(path) && within(final, path) {
		rel, _ := filepath.Rel(final, path)
		return filepath.Join(stage, rel)
	}
	return path
}

func requireFile(path string) error {
	f, err := store.OpenRegular(path)
	if err != nil {
		return err
	}
	return f.Close()
}

func requirePrivateFile(path string) error {
	if path == "" {
		return nil
	}
	if err := requireFile(path); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("credential must remain private: %s", path)
	}
	return nil
}

func validateToken(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return fmt.Errorf("invalid private credential %s", path)
	}
	raw, err := store.ReadFile(path)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) < 32 {
		return fmt.Errorf("invalid private credential %s", path)
	}
	return nil
}

func validateRoleLayout(stage, final string, d config.DaemonConfig, cat catalog) error {
	if filepath.Clean(d.DataDir) != final {
		return errors.New("daemon dataDir does not match migration source")
	}
	if err := config.ValidateDaemon(d); err != nil {
		return err
	}
	if err := validateToken(resolveStaged(d.AdminTokenPath, stage, final)); err != nil {
		return err
	}
	for _, path := range []string{d.APITLSCertFile, d.APITLSKeyFile, d.NATSCredentialsFile, d.NATSTLSCAFile, d.NATSTLSCertFile, d.NATSTLSKeyFile} {
		if path != "" {
			if err := requireFile(resolveStaged(path, stage, final)); err != nil {
				return err
			}
		}
	}
	for _, path := range []string{d.APITLSKeyFile, d.NATSCredentialsFile, d.NATSTLSKeyFile} {
		if path != "" {
			if err := requirePrivateFile(resolveStaged(path, stage, final)); err != nil {
				return err
			}
		}
	}
	seen, claims := map[string]bool{}, map[string]bool{}
	blobs, err := blob.New(stage, blob.Limits{MaxBytes: d.BlobMaxBytes, TempBytes: d.TempMaxBytes, WorkspaceBytes: d.WorkspaceMaxBytes})
	if err != nil {
		return err
	}
	for _, entry := range cat.Bots {
		if !validBotID(entry.BotID) || seen[entry.BotID] || (entry.DesiredState != "running" && entry.DesiredState != "stopped") || (entry.Archived && entry.DesiredState != "stopped") {
			return errors.New("invalid Bot catalog entry")
		}
		seen[entry.BotID] = true
		botRoot := filepath.Join(stage, "bots", entry.BotID)
		raw, err := store.ReadFile(filepath.Join(botRoot, "config.json"))
		if err != nil {
			return err
		}
		var b config.BotConfig
		if err := config.DecodeStrict(raw, &b); err != nil {
			return fmt.Errorf("Bot %s: %w", entry.BotID, err)
		}
		if err := config.ValidateBot(b); err != nil {
			return err
		}
		if err := validateBotState(botRoot, entry.BotID, stage, final, b); err != nil {
			return fmt.Errorf("Bot %s: %w", entry.BotID, err)
		}
		claim, err := readWorkspaceClaim(botRoot)
		if err != nil {
			return err
		}
		if claim != entry.WorkspaceID {
			return errors.New("workspace binding disagrees with catalog")
		}
		if entry.WorkspaceID != "" {
			if !validWorkspaceClaim(entry.WorkspaceID) || claims[entry.WorkspaceID] || b.Role != config.RoleConsole {
				return errors.New("invalid or duplicate workspace claim")
			}
			claims[entry.WorkspaceID] = true
		}
		if err := blobs.ValidateBotReferences(entry.BotID); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(filepath.Join(stage, "bots"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !seen[entry.Name()] {
			return errors.New("unregistered Bot data requires repair before migration")
		}
	}
	return nil
}

func readWorkspaceClaim(botRoot string) (string, error) {
	path := filepath.Join(botRoot, "workspace-binding.json")
	raw, err := store.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Mode().Perm() != 0600 || st.Size() > 4096 {
		return "", errors.New("invalid private workspace marker")
	}
	var claim struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if err := decodeManifest(raw, &claim); err != nil {
		return "", err
	}
	if !validWorkspaceClaim(claim.WorkspaceID) {
		return "", errors.New("invalid workspace claim")
	}
	return claim.WorkspaceID, nil
}

func validWorkspaceClaim(id string) bool {
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

func validateBotState(botRoot, botID, stage, final string, b config.BotConfig) error {
	id, err := identity.Load(filepath.Join(botRoot, "credentials", "identity.key"), filepath.Join(botRoot, "credentials", "identity.pub"))
	if err != nil {
		return err
	}
	if identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey)) != botID {
		return errors.New("Bot identity disagrees with directory")
	}
	if err := validateToken(filepath.Join(botRoot, "credentials", "api.token")); err != nil {
		return err
	}
	if err := requirePrivateFile(filepath.Join(botRoot, "credentials", "identity.key")); err != nil {
		return err
	}
	claim, err := readWorkspaceClaim(botRoot)
	if err != nil {
		return err
	}
	if claim != "" && b.Role != config.RoleConsole {
		return errors.New("only Console Bots may claim workspaces")
	}
	paths := []string{b.NATSCredentialsFile, b.NATSTLSCAFile, b.NATSTLSCertFile, b.NATSTLSKeyFile}
	if b.Harness != nil {
		paths = append(paths, b.Harness.CredentialsFile)
	}
	for _, path := range paths {
		if path != "" {
			if err := requireFile(resolveStaged(path, stage, final)); err != nil {
				return err
			}
		}
	}
	privatePaths := []string{b.NATSCredentialsFile, b.NATSTLSKeyFile}
	if b.Harness != nil {
		privatePaths = append(privatePaths, b.Harness.CredentialsFile)
	}
	for _, path := range privatePaths {
		if path != "" {
			if err := requirePrivateFile(resolveStaged(path, stage, final)); err != nil {
				return err
			}
		}
	}
	state := store.NewFSStore(botRoot)
	if _, err := state.LoadTrustState(); err != nil {
		return err
	}
	if _, err := state.LoadReplayState(); err != nil {
		return err
	}
	if _, err := state.LoadPeerAttrsState(); err != nil {
		return err
	}
	return filepath.WalkDir(botRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in Bot state")
		}
		if entry.IsDir() {
			if path == filepath.Join(botRoot, "workspace") || path == filepath.Join(botRoot, "credentials") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".json" && ext != ".jsonl" {
			return nil
		}
		raw, err := store.ReadFile(path)
		if err != nil {
			return err
		}
		if ext == ".json" && within(filepath.Join(botRoot, "sessions"), path) {
			rel, err := filepath.Rel(filepath.Join(botRoot, "sessions"), path)
			if err != nil {
				return err
			}
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) == 4 {
				var session store.SessionState
				if err := config.DecodeStrict(raw, &session); err != nil {
					return err
				}
				sessionStore := store.NewFSStore(botRoot)
				sessionStore.SessionDir = filepath.Join(botRoot, "sessions", parts[0])
				expected, err := sessionStore.SessionPath(session.Adapter, session.SessionKey)
				if err != nil {
					return err
				}
				if expected != path {
					return errors.New("native Session path disagrees with its Binding or key")
				}
				if _, found, err := sessionStore.LoadSessionState(session.Adapter, session.SessionKey); err != nil || !found {
					if err != nil {
						return err
					}
					return errors.New("missing native Session state")
				}
			}
		}
		check := func(raw []byte) error {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("corrupt state %s: %w", path, err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return fmt.Errorf("trailing state data: %s", path)
			}
			return validateLocalReferences(value, "", stage, final)
		}
		if ext == ".json" {
			return check(raw)
		}
		reader := bufio.NewReader(bytes.NewReader(raw))
		for {
			line, err := reader.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				if e := check(line); e != nil {
					return e
				}
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	})
}

func validateLocalReferences(value any, key, stage, final string) error {
	switch v := value.(type) {
	case map[string]any:
		if _, signed := v["signature"]; signed {
			return nil
		}
		if _, signed := v["protocolVersion"]; signed {
			return nil
		}
		for k, child := range v {
			if err := validateLocalReferences(child, k, stage, final); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := validateLocalReferences(child, key, stage, final); err != nil {
				return err
			}
		}
	case string:
		local := strings.ToLower(key)
		if filepath.IsAbs(v) && (local == "cwd" || strings.Contains(local, "path") || strings.Contains(local, "directory")) {
			path := resolveStaged(v, stage, final)
			if err := store.CheckPath(path); err != nil {
				return err
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("unresolvable persisted reference %s: %w", v, err)
			}
		}
	}
	return nil
}
