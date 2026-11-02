package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/store"
)

type archiveFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type archiveManifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	BotID         string        `json:"botId"`
	ArchiveRoot   string        `json:"archiveRoot"`
	Files         []archiveFile `json:"files"`
}

// Export writes a new private directory bundle, including every Bot file and
// every immutable entity referenced by that Bot. The daemon must be stopped.
func Export(dataDir, botID, archivePath string) error {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	archivePath, err = filepath.Abs(archivePath)
	if err != nil {
		return err
	}
	if !validBotID(botID) {
		return errors.New("a complete Bot ID is required")
	}
	if within(dataDir, archivePath) || within(archivePath, dataDir) {
		return errors.New("export bundle and daemon root must not overlap")
	}
	if err := store.CheckPath(archivePath); err != nil {
		return err
	}
	if _, err := os.Lstat(archivePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("export destination already exists or is inaccessible")
	}
	if err := CheckStartup(dataDir); err != nil {
		return err
	}
	lock, err := store.Lock(filepath.Join(dataDir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := CheckStartup(dataDir); err != nil {
		return err
	}
	d, err := config.LoadDaemon([]string{"--data-dir", dataDir})
	if err != nil {
		return err
	}
	cat, err := loadCatalog(dataDir)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range cat.Bots {
		if entry.BotID == botID {
			found = true
		}
	}
	if !found {
		return errors.New("Bot is not registered")
	}
	sourceBot := filepath.Join(dataDir, "bots", botID)
	id, err := identity.Load(filepath.Join(sourceBot, "credentials", "identity.key"), filepath.Join(sourceBot, "credentials", "identity.pub"))
	if err != nil {
		return err
	}
	if identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey)) != botID {
		return errors.New("Bot directory and identity disagree")
	}
	journalLocks, err := lockLegacyJournals(sourceBot)
	if err != nil {
		return err
	}
	defer func() {
		for _, l := range journalLocks {
			l.Close()
		}
	}()
	if err := store.EnsurePrivateDir(filepath.Dir(archivePath)); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(archivePath), "."+filepath.Base(archivePath)+".export-")
	if err != nil {
		return err
	}
	stagedBot := filepath.Join(stage, "bots", botID)
	if err := copyTree(sourceBot, stagedBot, nil); err != nil {
		return err
	}
	raw, err := store.ReadFile(filepath.Join(stagedBot, "config.json"))
	if err != nil {
		return err
	}
	var b config.BotConfig
	if err := config.DecodeStrict(raw, &b); err != nil {
		return err
	}
	if err := config.ValidateBot(b); err != nil {
		return err
	}
	if err := validateBotState(sourceBot, botID, dataDir, dataDir, b); err != nil {
		return err
	}
	effective := config.RuntimeConfig(d, b, botID)
	b.NATSCredentialsFile = effective.NATSCredentialsFile
	b.NATSTLSCAFile = effective.NATSTLSCAFile
	b.NATSTLSCertFile = effective.NATSTLSCertFile
	b.NATSTLSKeyFile = effective.NATSTLSKeyFile
	b.NATSTLSServerName = effective.NATSTLSServerName
	m := marker{SchemaVersion: 2, BotID: botID, Mappings: []pathMapping{{Source: sourceBot, Destination: filepath.Join(archivePath, "bots", botID)}}}
	prefix, err := randomID()
	if err != nil {
		return err
	}
	credentials := map[string]*string{"nats-credentials": &b.NATSCredentialsFile, "nats-ca": &b.NATSTLSCAFile, "nats-cert": &b.NATSTLSCertFile, "nats-key": &b.NATSTLSKeyFile}
	if b.Harness != nil {
		credentials["harness"] = &b.Harness.CredentialsFile
	}
	for name, path := range credentials {
		if *path == "" {
			continue
		}
		if within(sourceBot, *path) {
			rel, _ := filepath.Rel(sourceBot, *path)
			*path = filepath.Join(archivePath, "bots", botID, rel)
			continue
		}
		source := *path
		rel := filepath.Join("credentials", "export-"+prefix+"-"+name)
		if err := copyFile(source, filepath.Join(stagedBot, rel), nil); err != nil {
			return err
		}
		*path = filepath.Join(archivePath, "bots", botID, rel)
		m.Mappings = append(m.Mappings, pathMapping{Source: source, Destination: *path})
	}
	if err := rewriteLocalPaths(stagedBot, stage, archivePath, &m); err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(filepath.Join(stagedBot, "config.json"), b, 0o600); err != nil {
		return err
	}
	blobs, err := blob.New(dataDir, blob.Limits{MaxBytes: d.BlobMaxBytes, TempBytes: d.TempMaxBytes, WorkspaceBytes: d.WorkspaceMaxBytes})
	if err != nil {
		return err
	}
	if err := blobs.ExportBot(botID, filepath.Join(stage, "content")); err != nil {
		return err
	}
	if err := rewriteLocalPaths(filepath.Join(stage, "content", "transfers"), stage, archivePath, &m); err != nil {
		return err
	}
	files, err := archiveInventory(stage)
	if err != nil {
		return err
	}
	manifest := archiveManifest{SchemaVersion: 3, BotID: botID, ArchiveRoot: archivePath, Files: files}
	if err := store.WriteJSONAtomic(filepath.Join(stage, "export.json"), manifest, 0o600); err != nil {
		return err
	}
	if err := syncTree(stage); err != nil {
		return err
	}
	// The destination is checked again immediately before publication.
	if _, err := os.Lstat(archivePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("export destination appeared during export")
	}
	if err := os.Rename(stage, archivePath); err != nil {
		return err
	}
	return store.SyncDir(filepath.Dir(archivePath))
}

// Import validates a complete Export bundle before publishing the original Bot
// identity, stopped, into an offline daemon. Duplicate identities are errors.
// Local access credentials are rotated; the source archive remains untouched.
func Import(archivePath, dataDir string) (string, error) {
	archivePath, err := filepath.Abs(archivePath)
	if err != nil {
		return "", err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if within(archivePath, dataDir) || within(dataDir, archivePath) {
		return "", errors.New("import bundle and daemon root must not overlap")
	}
	if err := store.CheckPath(archivePath); err != nil {
		return "", err
	}
	raw, err := store.ReadFile(filepath.Join(archivePath, "export.json"))
	if err != nil {
		return "", err
	}
	var manifest archiveManifest
	if err := decodeManifest(raw, &manifest); err != nil {
		return "", err
	}
	if manifest.SchemaVersion == 2 {
		return "", errors.New("version 2 Bot export requires explicit bot convert before import")
	}
	if manifest.SchemaVersion != 3 || !validBotID(manifest.BotID) || !filepath.IsAbs(manifest.ArchiveRoot) {
		return "", errors.New("invalid Bot export manifest")
	}
	if err := validateArchive(archivePath, manifest); err != nil {
		return "", err
	}
	sourceBot := filepath.Join(archivePath, "bots", manifest.BotID)
	id, err := identity.Load(filepath.Join(sourceBot, "credentials", "identity.key"), filepath.Join(sourceBot, "credentials", "identity.pub"))
	if err != nil {
		return "", err
	}
	if identity.DeriveBotID(identity.DeriveBotDID(id.PublicKey)) != manifest.BotID {
		return "", errors.New("export identity does not match manifest")
	}
	if err := CheckStartup(dataDir); err != nil {
		return "", err
	}
	if err := store.EnsurePrivateDir(dataDir); err != nil {
		return "", err
	}
	lock, err := store.Lock(filepath.Join(dataDir, "daemon.lock"))
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if err := CheckStartup(dataDir); err != nil {
		return "", err
	}
	d, err := config.LoadDaemon([]string{"--data-dir", dataDir})
	if err != nil {
		return "", err
	}
	cat, err := loadCatalog(dataDir)
	if err != nil {
		return "", err
	}
	for _, entry := range cat.Bots {
		if entry.BotID == manifest.BotID {
			return "", errors.New("Bot identity already registered")
		}
	}
	destination := filepath.Join(dataDir, "bots", manifest.BotID)
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("Bot directory already exists or is inaccessible")
	}
	if err := store.EnsurePrivateDir(filepath.Join(dataDir, "bots")); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(filepath.Join(dataDir, "bots"), ".import-")
	if err != nil {
		return "", err
	}
	if err := copyTree(sourceBot, stage, nil); err != nil {
		return "", err
	}
	m := marker{SchemaVersion: 2, BotID: manifest.BotID, ArchiveOnly: true, Mappings: []pathMapping{{Source: sourceBot, Destination: destination}, {Source: filepath.Join(manifest.ArchiveRoot, "bots", manifest.BotID), Destination: destination}}}
	if err := rewriteLocalPaths(stage, dataDir, dataDir, &m); err != nil {
		return "", err
	}
	contentStage, err := os.MkdirTemp(dataDir, ".import-content-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(contentStage)
	if err := copyTree(filepath.Join(archivePath, "content"), contentStage, nil); err != nil {
		return "", err
	}
	if err := rewriteLocalPaths(filepath.Join(contentStage, "transfers"), dataDir, dataDir, &m); err != nil {
		return "", err
	}
	raw, err = store.ReadFile(filepath.Join(stage, "config.json"))
	if err != nil {
		return "", err
	}
	var b config.BotConfig
	if err := config.DecodeStrict(raw, &b); err != nil {
		return "", err
	}
	if err := validateArchiveCredentials(b, destination); err != nil {
		return "", err
	}
	if err := config.ValidateBot(b); err != nil {
		return "", err
	}
	if err := validateBotState(stage, manifest.BotID, stage, destination, b); err != nil {
		return "", err
	}
	if err := validateArchiveContent(stage, contentStage, manifest.BotID, b.FileQuotaBytes); err != nil {
		return "", err
	}
	workspaceID, err := readWorkspaceClaim(stage)
	if err != nil {
		return "", err
	}
	if workspaceID != "" {
		if b.Role != config.RoleConsole {
			return "", errors.New("only Console Bots may claim workspaces")
		}
		for _, entry := range cat.Bots {
			if entry.WorkspaceID == workspaceID {
				return "", errors.New("exported workspace is already bound")
			}
		}
	}
	if err := newToken(filepath.Join(stage, "credentials", "api.token")); err != nil {
		return "", err
	}
	if err := syncTree(stage); err != nil {
		return "", err
	}
	if err := os.Rename(stage, destination); err != nil {
		return "", err
	}
	if err := store.SyncDir(filepath.Dir(destination)); err != nil {
		return "", err
	}
	blobs, err := blob.New(dataDir, blob.Limits{MaxBytes: d.BlobMaxBytes, TempBytes: d.TempMaxBytes, WorkspaceBytes: d.WorkspaceMaxBytes})
	if err != nil {
		return "", err
	}
	if err := blobs.ImportBot(manifest.BotID, contentStage, b.FileQuotaBytes); err != nil {
		return "", err
	}
	// Registration is last: a failed import cannot become an active Bot.
	if _, err := os.Lstat(filepath.Join(dataDir, "config.json")); errors.Is(err, os.ErrNotExist) {
		if err := store.WriteJSONAtomic(filepath.Join(dataDir, "config.json"), d, 0o600); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	if _, err := os.Lstat(d.AdminTokenPath); errors.Is(err, os.ErrNotExist) {
		if err := newToken(d.AdminTokenPath); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	cat.Bots = append(cat.Bots, botEntry{BotID: manifest.BotID, DesiredState: "stopped", WorkspaceID: workspaceID})
	if err := store.WriteJSONAtomic(filepath.Join(dataDir, "bots.json"), cat, 0o600); err != nil {
		return "", err
	}
	return manifest.BotID, nil
}

func validBotID(id string) bool {
	if len(id) != 35 || !strings.HasPrefix(id, "n1-") {
		return false
	}
	_, err := hex.DecodeString(id[3:])
	return err == nil && strings.ToLower(id) == id
}
func loadCatalog(root string) (catalog, error) {
	c := catalog{SchemaVersion: 3, Bots: []botEntry{}}
	data, err := store.ReadFile(filepath.Join(root, "bots.json"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := config.DecodeStrict(data, &c); err != nil {
		return c, err
	}
	if c.SchemaVersion != 3 || c.Bots == nil {
		return c, errors.New("invalid Bot catalog schema")
	}
	seen := map[string]bool{}
	for _, entry := range c.Bots {
		if !validBotID(entry.BotID) || seen[entry.BotID] || (entry.DesiredState != "running" && entry.DesiredState != "stopped") {
			return c, errors.New("invalid Bot catalog entry")
		}
		seen[entry.BotID] = true
	}
	return c, nil
}
func archiveInventory(root string) ([]archiveFile, error) {
	var result []archiveFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in Bot archive: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "export.json" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if !st.Mode().IsRegular() {
			f.Close()
			return errors.New("non-regular archive file")
		}
		hash := sha256.New()
		size, err := io.Copy(hash, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		result = append(result, archiveFile{Path: filepath.ToSlash(rel), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
		return nil
	})
	return result, err
}
func validateArchive(root string, m archiveManifest) error {
	actual, err := archiveInventory(root)
	if err != nil {
		return err
	}
	expected := make(map[string]archiveFile, len(m.Files))
	for _, f := range m.Files {
		clean := filepath.Clean(filepath.FromSlash(f.Path))
		if f.Path == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || f.Size < 0 {
			return errors.New("unsafe archive inventory path")
		}
		if _, ok := expected[f.Path]; ok {
			return errors.New("duplicate archive inventory path")
		}
		expected[f.Path] = f
	}
	if len(actual) != len(expected) {
		return errors.New("archive file inventory differs")
	}
	for _, f := range actual {
		want, ok := expected[f.Path]
		if !ok || want.Size != f.Size || want.SHA256 != f.SHA256 {
			return fmt.Errorf("archive integrity check failed: %s", f.Path)
		}
	}
	return nil
}
