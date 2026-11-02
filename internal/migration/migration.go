// Package migration implements explicit, offline layout transitions. Originals
// and failed staging directories are retained; ordinary startup never migrates.
package migration

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/store"
	"clawsynapse/internal/transfer"
)

type botEntry struct {
	BotID        string `json:"botId"`
	DesiredState string `json:"desiredState"`
	Archived     bool   `json:"archived"`
	WorkspaceID  string `json:"workspaceId,omitempty"`
}
type catalog struct {
	SchemaVersion int        `json:"schemaVersion"`
	Bots          []botEntry `json:"bots"`
}
type pathMapping struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}
type inventoryEntry struct {
	Source string `json:"source"`
	Backup string `json:"backup"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type marker struct {
	SchemaVersion  int              `json:"schemaVersion"`
	Status         string           `json:"status"`
	BotID          string           `json:"botId"`
	Source         string           `json:"source"`
	Target         string           `json:"target"`
	OriginalBackup string           `json:"originalBackup,omitempty"`
	CreatedAt      string           `json:"createdAt"`
	Mappings       []pathMapping    `json:"pathMappings"`
	Inventory      []inventoryEntry `json:"inventory"`
	ArchiveOnly    bool             `json:"-"`
}

// Migrate requires exclusive ownership of the legacy root and message journal.
// target may be the original root, a nonexistent root, or an empty directory.
// The complete marker is the commit point; all source bytes remain backed up.
func Migrate(legacy config.Config, target string) (string, error) {
	source, err := filepath.Abs(legacy.DataDir)
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if source == string(filepath.Separator) || target == string(filepath.Separator) {
		return "", errors.New("filesystem root cannot be migrated")
	}
	if source != target && (within(source, target) || within(target, source)) {
		return "", errors.New("migration source and target directories may not overlap")
	}
	if err := store.CheckPath(source); err != nil {
		return "", err
	}
	if err := store.CheckPath(target); err != nil {
		return "", err
	}
	st, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", errors.New("legacy dataDir is not a directory")
	}
	if err := config.Validate(legacy); err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	if err := store.EnsurePrivateDir(parent); err != nil {
		return "", err
	}
	transactionLock, err := store.Lock(target + ".migration.lock")
	if err != nil {
		return "", err
	}
	defer transactionLock.Close()
	if err := CheckStartup(target); err != nil {
		return "", err
	}
	sourceLock, err := store.Lock(filepath.Join(source, "daemon.lock"))
	if err != nil {
		return "", err
	}
	defer sourceLock.Close()
	journalLocks, err := lockLegacyJournals(source)
	if err != nil {
		return "", err
	}
	defer func() {
		for _, l := range journalLocks {
			l.Close()
		}
	}()
	id, err := identity.Load(legacy.IdentityKeyPath, legacy.IdentityPubPath)
	if err != nil {
		return "", err
	}
	botID := identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey))
	exists := false
	var targetLock *store.FileLock
	if _, err := os.Lstat(target); err == nil {
		exists = true
		if source != target {
			entries, e := os.ReadDir(target)
			if e != nil {
				return "", e
			}
			if len(entries) != 0 {
				return "", errors.New("migration target is not empty")
			}
			targetLock, e = store.Lock(filepath.Join(target, "daemon.lock"))
			if e != nil {
				return "", e
			}
			defer targetLock.Close()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	rootBytes, err := treeSize(source)
	if err != nil {
		return "", err
	}
	transferBytes, err := treeSizeOptional(legacy.TransferDir)
	if err != nil {
		return "", err
	}
	available, err := availableBytes(parent)
	if err != nil {
		return "", err
	}
	required := uint64(rootBytes)*3 + uint64(transferBytes)*3 + 16*1024*1024
	if available < required {
		return "", fmt.Errorf("migration needs at least %d free bytes", required)
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".migration-")
	if err != nil {
		return "", err
	}
	// Never delete a failed stage: it is recoverable evidence, not an empty retry.
	stageLock, err := store.Lock(filepath.Join(stage, "daemon.lock"))
	if err != nil {
		return "", err
	}
	defer stageLock.Close()
	m := marker{SchemaVersion: 2, Status: "preparing", BotID: botID, Source: source, Target: target, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "migration.json"), m, 0o600); err != nil {
		return "", err
	}
	backup := filepath.Join(stage, "legacy-backup")
	if err := copyTree(source, filepath.Join(backup, "data"), &m.Inventory); err != nil {
		return "", err
	}
	if legacy.ConfigPath != "" {
		if err := backupFile(legacy.ConfigPath, filepath.Join(backup, "config-source"), &m.Inventory); err != nil {
			return "", err
		}
	}
	botRoot := filepath.Join(stage, "bots", botID)
	finalBot := filepath.Join(target, "bots", botID)
	if err := copyTree(source, botRoot, nil); err != nil {
		return "", err
	}
	// Known writable roots are relocated, not duplicated as live legacy state.
	for _, name := range []string{"sessions", "transfers", "daemon.lock", "config.json", "config.yaml", "config.yml"} {
		if err := os.RemoveAll(filepath.Join(botRoot, name)); err != nil {
			return "", err
		}
	}
	for _, name := range []string{"identity.key", "identity.pub", "api.token"} {
		if err := os.Remove(filepath.Join(botRoot, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := store.EnsurePrivateDir(filepath.Join(botRoot, "credentials")); err != nil {
		return "", err
	}
	for _, pair := range [][2]string{{legacy.IdentityKeyPath, "identity.key"}, {legacy.IdentityPubPath, "identity.pub"}} {
		if err := backupFile(pair[0], filepath.Join(backup, "credentials", pair[1]), &m.Inventory); err != nil {
			return "", err
		}
		if err := copyFile(pair[0], filepath.Join(botRoot, "credentials", pair[1]), nil); err != nil {
			return "", err
		}
	}
	if legacy.APITokenPath != "" {
		if err := backupOptional(legacy.APITokenPath, filepath.Join(backup, "credentials", "legacy-api.token"), &m.Inventory); err != nil {
			return "", err
		}
	}
	if err := newToken(filepath.Join(stage, "admin.token")); err != nil {
		return "", err
	}
	if err := newToken(filepath.Join(botRoot, "credentials", "api.token")); err != nil {
		return "", err
	}
	binding, err := randomID()
	if err != nil {
		return "", err
	}
	d, b := splitConfig(legacy, target, binding)
	if transferBytes > b.FileQuotaBytes {
		b.FileQuotaBytes = transferBytes
	}
	if b.TransferMaxFileSize > b.FileQuotaBytes {
		b.FileQuotaBytes = b.TransferMaxFileSize
	}
	if rootBytes+transferBytes > d.WorkspaceMaxBytes {
		d.WorkspaceMaxBytes = rootBytes + transferBytes
	}
	if transferBytes > d.BlobMaxBytes {
		d.BlobMaxBytes = transferBytes
	}
	if err := copyCredentialPaths(stage, target, &d, &b, &m); err != nil {
		return "", err
	}
	if legacy.WebhookAuthToken != "" && b.Harness != nil {
		b.Harness.CredentialsFile = filepath.Join(finalBot, "credentials", "harness.token")
		if err := store.WriteAtomic(filepath.Join(botRoot, "credentials", "harness.token"), []byte(legacy.WebhookAuthToken+"\n"), 0o600); err != nil {
			return "", err
		}
	}
	m.Mappings = append(m.Mappings, []pathMapping{{Source: source, Destination: finalBot}, {Source: filepath.Join(source, "sessions"), Destination: filepath.Join(finalBot, "sessions", binding)}, {Source: legacy.TransferDir, Destination: filepath.Join(finalBot, "workspace", "legacy-transfers")}, {Source: legacy.IdentityKeyPath, Destination: filepath.Join(finalBot, "credentials", "identity.key")}, {Source: legacy.IdentityPubPath, Destination: filepath.Join(finalBot, "credentials", "identity.pub")}}...)
	if err := copyOptionalTree(filepath.Join(source, "sessions"), filepath.Join(botRoot, "sessions", binding), nil); err != nil {
		return "", err
	}
	if legacy.TransferDir != "" {
		if err := copyOptionalTree(legacy.TransferDir, filepath.Join(botRoot, "workspace", "legacy-transfers"), nil); err != nil {
			return "", err
		}
		if !within(source, legacy.TransferDir) {
			if err := copyOptionalTree(legacy.TransferDir, filepath.Join(backup, "transfers-source"), &m.Inventory); err != nil {
				return "", err
			}
		}
	}
	if err := rewriteLocalPaths(botRoot, stage, target, &m); err != nil {
		return "", err
	}
	if err := migrateSessions(botRoot, binding, botID); err != nil {
		return "", err
	}
	// A standalone root-level journal was used by some explicit installations.
	oldJournal := filepath.Join(botRoot, "messages.jsonl")
	if _, err := os.Lstat(oldJournal); err == nil {
		dst := filepath.Join(botRoot, "state", "messages.jsonl")
		if _, e := os.Lstat(dst); e == nil {
			return "", errors.New("both legacy journal locations exist; resolve conflict explicitly")
		}
		if err := store.EnsurePrivateDir(filepath.Dir(dst)); err != nil {
			return "", err
		}
		if err := os.Rename(oldJournal, dst); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := validateJournal(filepath.Join(botRoot, "state", "messages.jsonl")); err != nil {
		return "", err
	}
	if err := store.NewFSStore(botRoot).EnsureLayout(); err != nil {
		return "", fmt.Errorf("legacy state: %w", err)
	}
	if err := config.ValidateBot(b); err != nil {
		return "", err
	}
	if err := store.WriteJSONAtomic(filepath.Join(botRoot, "config.json"), b, 0o600); err != nil {
		return "", err
	}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "config.json"), d, 0o600); err != nil {
		return "", err
	}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "bots.json"), catalog{SchemaVersion: 3, Bots: []botEntry{{BotID: botID, DesiredState: "stopped"}}}, 0o600); err != nil {
		return "", err
	}
	blobs, err := blob.New(stage, blob.Limits{MaxBytes: d.BlobMaxBytes, TempBytes: d.TempMaxBytes, WorkspaceBytes: d.WorkspaceMaxBytes})
	if err != nil {
		return "", err
	}
	if legacy.TransferDir != "" {
		if _, err := os.Lstat(legacy.TransferDir); err == nil {
			if err := transfer.ImportLegacy(legacy.TransferDir, botID, blobs, b.TransferMaxFileSize, b.FileQuotaBytes); err != nil {
				return "", err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	m.Status = "prepared"
	if exists {
		suffix, err := randomID()
		if err != nil {
			return "", err
		}
		m.OriginalBackup = target + ".legacy-backup-" + suffix
	}
	for i := range m.Inventory {
		if within(stage, m.Inventory[i].Backup) {
			rel, _ := filepath.Rel(stage, m.Inventory[i].Backup)
			m.Inventory[i].Backup = filepath.Join(target, rel)
		}
	}
	if err := store.WriteJSONAtomic(target+".migration.json", m, 0o600); err != nil {
		return "", err
	}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "migration.json"), m, 0o600); err != nil {
		return "", err
	}
	if err := syncTree(stage); err != nil {
		return "", err
	}
	if exists {
		if err := os.Rename(target, m.OriginalBackup); err != nil {
			return "", err
		}
		if err := store.SyncDir(parent); err != nil {
			return "", err
		}
	}
	if err := os.Rename(stage, target); err != nil {
		if exists {
			_ = os.Rename(m.OriginalBackup, target)
			_ = store.SyncDir(parent)
		}
		return "", err
	}
	if err := store.SyncDir(parent); err != nil {
		return "", err
	}
	m.Status = "complete"
	if err := store.WriteJSONAtomic(filepath.Join(target, "migration.json"), m, 0o600); err != nil {
		return "", err
	}
	if err := store.WriteJSONAtomic(target+".migration.json", m, 0o600); err != nil {
		return "", err
	}
	return botID, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func newToken(path string) error {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	return store.WriteAtomic(path, []byte(hex.EncodeToString(b[:])+"\n"), 0o600)
}
func lockLegacyJournals(root string) ([]*store.FileLock, error) {
	var locks []*store.FileLock
	for _, p := range []string{filepath.Join(root, "state", "messages.jsonl"), filepath.Join(root, "messages.jsonl")} {
		if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		l, err := store.Lock(p)
		if err != nil {
			for _, old := range locks {
				old.Close()
			}
			return nil, fmt.Errorf("legacy daemon must be stopped: %w", err)
		}
		locks = append(locks, l)
	}
	return locks, nil
}
func treeSizeOptional(path string) (int64, error) {
	if path == "" {
		return 0, nil
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return treeSize(path)
}
func treeSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink cannot be migrated: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", p)
		}
		total += st.Size()
		return nil
	})
	return total, err
}
func copyOptionalTree(source, dest string, inv *[]inventoryEntry) error {
	if source == "" {
		return nil
	}
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return copyTree(source, dest, inv)
}
func copyTree(source, dest string, inv *[]inventoryEntry) error {
	if err := store.CheckPath(source); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink cannot be migrated: %s", p)
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dest, rel)
		if d.IsDir() {
			return store.EnsurePrivateDir(out)
		}
		return copyFile(p, out, inv)
	})
}
func backupOptional(source, dest string, inv *[]inventoryEntry) error {
	if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return backupFile(source, dest, inv)
}
func backupFile(source, dest string, inv *[]inventoryEntry) error { return copyFile(source, dest, inv) }
func copyFile(source, dest string, inv *[]inventoryEntry) error {
	if err := store.CheckPath(source); err != nil {
		return err
	}
	if err := store.CheckPath(dest); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", source)
	}
	if err := store.EnsurePrivateDir(filepath.Dir(dest)); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600|st.Mode().Perm()&0o100)
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), in)
	if err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := store.SyncDir(filepath.Dir(dest)); err != nil {
		return err
	}
	if inv != nil {
		*inv = append(*inv, inventoryEntry{Source: source, Backup: dest, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	return nil
}
func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		if !d.IsDir() {
			f, err := store.OpenRegular(p)
			if err != nil {
				return err
			}
			syncErr := f.Sync()
			closeErr := f.Close()
			if syncErr != nil {
				return syncErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := store.SyncDir(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func splitConfig(c config.Config, target, binding string) (config.DaemonConfig, config.BotConfig) {
	d := config.DefaultDaemonConfig()
	d.DataDir = target
	d.AdminTokenPath = filepath.Join(target, "admin.token")
	d.LocalAPIAddr = c.LocalAPIAddr
	d.AllowRemoteAPI = c.AllowRemoteAPI
	d.APITLSCertFile = c.APITLSCertFile
	d.APITLSKeyFile = c.APITLSKeyFile
	d.Production = c.Production
	d.NATSServers = c.NATSServers
	d.NATSCredentialsFile = c.NATSCredentialsFile
	d.NATSTLSCAFile = c.NATSTLSCAFile
	d.NATSTLSCertFile = c.NATSTLSCertFile
	d.NATSTLSKeyFile = c.NATSTLSKeyFile
	d.NATSTLSServerName = c.NATSTLSServerName
	d.LogLevel = c.LogLevel
	d.LogFormat = c.LogFormat
	d.LogFilePath = c.LogFilePath
	d.LogAddSource = c.LogAddSource
	d.LogRotateMaxSizeMB = c.LogRotateMaxSizeMB
	d.LogRotateMaxBackups = c.LogRotateMaxBackups
	d.LogRotateMaxAgeDays = c.LogRotateMaxAgeDays
	d.LogRotateCompress = c.LogRotateCompress
	role := config.RoleAgent
	if c.AgentAdapter == "console" {
		role = config.RoleConsole
	}
	b := config.DefaultBotConfigForRole(role)
	b.BindingRevision = binding
	b.DisplayName = c.DisplayName
	b.Labels = c.Labels
	b.TrustMode = c.TrustMode
	b.TrustAutoApprove = c.TrustAutoApprove
	b.HeartbeatInterval = c.HeartbeatInterval
	b.AnnounceTTL = c.AnnounceTTL
	b.AgentProduct = c.AgentProduct
	b.AgentVersion = c.AgentVersion
	if role == config.RoleAgent {
		b.Harness = &config.HarnessBinding{Adapter: c.AgentAdapter, Timeout: c.AgentAdapterTimeout, WebhookURL: c.WebhookURL, AllowedAgentIDs: c.AgentAllowedIDs}
	}
	b.DeliverablePrefixes = c.DeliverablePrefixes
	b.TransferMaxFileSize = c.TransferMaxFileSize
	b.TransferTTL = c.TransferTTL
	return d, b
}

func copyCredentialPaths(stage, target string, d *config.DaemonConfig, b *config.BotConfig, m *marker) error {
	for name, p := range map[string]*string{"api-cert": &d.APITLSCertFile, "api-key": &d.APITLSKeyFile, "nats-credentials": &d.NATSCredentialsFile, "nats-ca": &d.NATSTLSCAFile, "nats-cert": &d.NATSTLSCertFile, "nats-key": &d.NATSTLSKeyFile} {
		if *p == "" {
			continue
		}
		source := *p
		dest := filepath.Join(stage, "credentials", name)
		if err := copyFile(source, dest, nil); err != nil {
			return err
		}
		if err := backupFile(source, filepath.Join(stage, "legacy-backup", "credentials", name), &m.Inventory); err != nil {
			return err
		}
		*p = filepath.Join(target, "credentials", name)
		m.Mappings = append(m.Mappings, pathMapping{Source: source, Destination: *p})
	}
	if d.LogFilePath != "" && within(m.Source, d.LogFilePath) {
		rel, _ := filepath.Rel(m.Source, d.LogFilePath)
		d.LogFilePath = filepath.Join(target, "bots", m.BotID, rel)
	}
	return nil
}

// Signed protocol envelopes remain byte-semantically unchanged. Only unsigned
// local persisted paths are rewritten; external local resources get private copies.
func rewriteLocalPaths(botRoot, stage, target string, m *marker) error {
	return filepath.WalkDir(botRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == filepath.Join(botRoot, "workspace") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".json" && ext != ".jsonl" {
			return nil
		}
		data, err := store.ReadFile(path)
		if err != nil {
			return err
		}
		var output bytes.Buffer
		changed := false
		process := func(raw []byte) ([]byte, error) {
			var value any
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&value); err != nil {
				return raw, nil
			}
			updated, err := rewriteValue(value, "", botRoot, stage, target, m)
			if err != nil {
				return nil, err
			}
			if !updated {
				return raw, nil
			}
			changed = true
			return json.Marshal(value)
		}
		if ext == ".jsonl" {
			reader := bufio.NewReader(bytes.NewReader(data))
			for {
				line, e := reader.ReadBytes('\n')
				if len(line) > 0 {
					raw := bytes.TrimSuffix(line, []byte{'\n'})
					out, err := process(raw)
					if err != nil {
						return err
					}
					output.Write(out)
					if line[len(line)-1] == '\n' {
						output.WriteByte('\n')
					}
				}
				if e == io.EOF {
					break
				}
				if e != nil {
					return e
				}
			}
		} else {
			out, err := process(data)
			if err != nil {
				return err
			}
			output.Write(out)
			if changed {
				output.WriteByte('\n')
			}
		}
		if changed {
			return store.WriteAtomic(path, output.Bytes(), 0o600)
		}
		return nil
	})
}
func rewriteValue(value any, key, botRoot, stage, target string, m *marker) (bool, error) {
	changed := false
	switch v := value.(type) {
	case map[string]any:
		if _, ok := v["signature"]; ok {
			return false, nil
		}
		if _, ok := v["protocolVersion"]; ok {
			return false, nil
		}
		for k, child := range v {
			if s, ok := child.(string); ok && filepath.IsAbs(s) {
				replacement, err := mapLocalPath(s, k, botRoot, stage, target, m)
				if err != nil {
					return false, err
				}
				if replacement != s {
					v[k] = replacement
					changed = true
				}
			} else {
				c, err := rewriteValue(child, k, botRoot, stage, target, m)
				if err != nil {
					return false, err
				}
				changed = changed || c
			}
		}
	case []any:
		for i, child := range v {
			if s, ok := child.(string); ok && filepath.IsAbs(s) {
				replacement, err := mapLocalPath(s, key, botRoot, stage, target, m)
				if err != nil {
					return false, err
				}
				if replacement != s {
					v[i] = replacement
					changed = true
				}
			} else {
				c, err := rewriteValue(child, key, botRoot, stage, target, m)
				if err != nil {
					return false, err
				}
				changed = changed || c
			}
		}
	}
	return changed, nil
}
func mapLocalPath(path, key, botRoot, stage, target string, m *marker) (string, error) {
	sort.SliceStable(m.Mappings, func(i, j int) bool { return len(m.Mappings[i].Source) > len(m.Mappings[j].Source) })
	for _, mapping := range m.Mappings {
		if mapping.Source != "" && within(mapping.Source, path) {
			rel, err := filepath.Rel(mapping.Source, path)
			if err != nil {
				return "", err
			}
			return filepath.Join(mapping.Destination, rel), nil
		}
	}
	local := strings.ToLower(key)
	if local != "cwd" && !strings.Contains(local, "path") && !strings.Contains(local, "directory") {
		return path, nil
	}
	if m.ArchiveOnly {
		return "", fmt.Errorf("archive contains an external local reference: %s", path)
	}
	if _, err := os.Lstat(path); err != nil {
		return "", fmt.Errorf("cannot preserve persisted local path %s: %w", path, err)
	}
	if within(path, stage) || within(path, target) {
		return "", fmt.Errorf("external local path overlaps migration destination: %s", path)
	}
	hash := sha256.Sum256([]byte(path))
	rel := filepath.Join("workspace", "legacy-external", hex.EncodeToString(hash[:8]), filepath.Base(path))
	dest := filepath.Join(botRoot, rel)
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		if err := copyTree(path, dest, &m.Inventory); err != nil {
			return "", err
		}
	} else {
		if err := copyFile(path, dest, &m.Inventory); err != nil {
			return "", err
		}
	}
	final := filepath.Join(target, "bots", m.BotID, rel)
	m.Mappings = append(m.Mappings, pathMapping{Source: path, Destination: final})
	return final, nil
}

func migrateSessions(botRoot, binding, botID string) error {
	root := filepath.Join(botRoot, "sessions", binding)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".json" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(filepath.Separator))
		// Unrecognized session-directory files remain intact as unknown data.
		if len(parts) != 3 || len(parts[1]) != 2 || len(parts[2]) != 69 {
			continue
		}
		data, err := store.ReadFile(path)
		if err != nil {
			return err
		}
		var st store.SessionState
		if err := config.DecodeStrict(data, &st); err != nil {
			return fmt.Errorf("invalid legacy session %s: %w", path, err)
		}
		if st.SchemaVersion != 1 || st.SessionKey == "" || st.Adapter == "" || st.SessionID == "" {
			return fmt.Errorf("invalid legacy session: %s", path)
		}
		oldHash := sha256.Sum256([]byte(st.SessionKey))
		if st.Adapter != parts[0] || hex.EncodeToString(oldHash[:])+".json" != parts[2] {
			return fmt.Errorf("mismatched legacy session path: %s", path)
		}
		st.SessionKey = migrateSessionKey(botID, binding, st.Adapter, st.SessionKey)
		stateStore := store.NewFSStore(botRoot)
		stateStore.SessionDir = root
		dest, err := stateStore.SessionPath(st.Adapter, st.SessionKey)
		if err != nil {
			return err
		}
		if dest != path {
			if _, err := os.Lstat(dest); err == nil {
				return fmt.Errorf("session migration conflict: %s", dest)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		if err := store.WriteJSONAtomic(dest, st, 0o600); err != nil {
			return err
		}
		if dest != path {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateSessionKey(botID, binding, adapter, key string) string {
	data, _ := json.Marshal([]string{botID, binding, adapter, key})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func validateJournal(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !json.Valid(line) {
			return fmt.Errorf("corrupt committed message journal record at %d", offset)
		}
		offset += int64(len(line))
	}
}
