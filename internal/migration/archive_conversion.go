package migration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/store"
)

// ConvertArchive creates a separately published version 3 bundle from a version
// 2 export. The original manifest, credentials, history and bytes are untouched.
// It is not an import and never registers or starts the exported identity.
func ConvertArchive(source, destination string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if within(source, destination) || within(destination, source) {
		return errors.New("archive source and destination must not overlap")
	}
	if err := store.CheckPath(source); err != nil {
		return err
	}
	if err := store.CheckPath(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("converted archive destination must not exist")
	}
	if err := store.EnsurePrivateDir(filepath.Dir(destination)); err != nil {
		return err
	}
	lock, err := store.Lock(source + ".conversion.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	targetLock, err := store.Lock(destination + ".conversion.lock")
	if err != nil {
		return err
	}
	defer targetLock.Close()
	raw, err := store.ReadFile(filepath.Join(source, "export.json"))
	if err != nil {
		return err
	}
	var manifest archiveManifest
	if err := decodeManifest(raw, &manifest); err != nil {
		return err
	}
	if manifest.SchemaVersion != 2 || !validBotID(manifest.BotID) || !filepath.IsAbs(manifest.ArchiveRoot) {
		return errors.New("explicit archive conversion requires a version 2 Bot export")
	}
	if err := validateArchive(source, manifest); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".convert-")
	if err != nil {
		return err
	}
	if err := copyLayout(source, stage); err != nil {
		return err
	}
	// Validate the copied snapshot against the original inventory as well.
	if err := validateArchive(stage, manifest); err != nil {
		return err
	}
	botRoot := filepath.Join(stage, "bots", manifest.BotID)
	configPath := filepath.Join(botRoot, "config.json")
	raw, err = store.ReadFile(configPath)
	if err != nil {
		return err
	}
	raw, err = convertOldBot(raw)
	if err != nil {
		return err
	}
	if err := store.WriteAtomic(configPath, raw, fileMode(configPath)); err != nil {
		return err
	}
	m := marker{SchemaVersion: 2, BotID: manifest.BotID, ArchiveOnly: true, Mappings: []pathMapping{
		{Source: manifest.ArchiveRoot, Destination: destination},
		{Source: source, Destination: destination},
	}}
	if err := rewriteLocalPaths(botRoot, stage, destination, &m); err != nil {
		return err
	}
	raw, err = store.ReadFile(configPath)
	if err != nil {
		return err
	}
	var b config.BotConfig
	if err := config.DecodeStrict(raw, &b); err != nil {
		return err
	}
	if err := validateArchiveCredentials(b, filepath.Join(destination, "bots", manifest.BotID)); err != nil {
		return err
	}
	if err := config.ValidateBot(b); err != nil {
		return err
	}
	if err := validateBotState(botRoot, manifest.BotID, stage, destination, b); err != nil {
		return err
	}
	// Validate the archive's immutable grants without modifying the archive.
	if err := alignOldArchiveReferences(botRoot, filepath.Join(stage, "content")); err != nil {
		return err
	}
	if err := validateArchiveContent(botRoot, filepath.Join(stage, "content"), manifest.BotID, b.FileQuotaBytes); err != nil {
		return err
	}
	files, err := archiveInventory(stage)
	if err != nil {
		return err
	}
	manifest.SchemaVersion = 3
	manifest.ArchiveRoot = destination
	manifest.Files = files
	if err := store.WriteJSONAtomic(filepath.Join(stage, "export.json"), manifest, fileMode(filepath.Join(stage, "export.json"))); err != nil {
		return err
	}
	if err := validateArchive(stage, manifest); err != nil {
		return err
	}
	if err := syncTree(stage); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("converted archive destination appeared during conversion")
	}
	if err := os.Rename(stage, destination); err != nil {
		return err
	}
	return store.SyncDir(filepath.Dir(destination))
}

func validateArchiveContent(botRoot, contentRoot, botID string, quota int64) error {
	validation, err := os.MkdirTemp("", ".archive-content-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(validation)
	if err := matchingGrantNames(botRoot, contentRoot); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(botRoot, "transfers"), filepath.Join(validation, "bots", botID, "transfers"), nil); err != nil {
		return err
	}
	blobs, err := blob.New(validation, blob.Limits{})
	if err != nil {
		return err
	}
	if err := blobs.ImportBot(botID, contentRoot, quota); err != nil {
		return fmt.Errorf("exported file references: %w", err)
	}
	return blobs.ValidateBotReferences(botID)
}

func matchingGrantNames(botRoot, contentRoot string) error {
	botEntries, err := os.ReadDir(filepath.Join(botRoot, "transfers"))
	if err != nil {
		return err
	}
	contentEntries, err := os.ReadDir(filepath.Join(contentRoot, "transfers"))
	if err != nil {
		return err
	}
	expected := make(map[string]bool, len(botEntries))
	for _, entry := range botEntries {
		if strings.HasPrefix(entry.Name(), ".tmp-") && entry.Type().IsRegular() {
			continue
		}
		expected[entry.Name()] = true
	}
	if len(expected) != len(contentEntries) {
		return errors.New("Bot and content archive grant inventories differ")
	}
	for _, entry := range contentEntries {
		if !expected[entry.Name()] {
			return errors.New("Bot and content archive grants differ")
		}
	}
	return nil
}

// Version 2 exports relocated Bot-local paths but left the duplicate grant Data
// in content/transfers at its original root. The Bot copy is authoritative for
// those unsigned local paths; no identity, digest, size or authorization change
// is permitted while aligning the two copies for the strict version 3 bundle.
func alignOldArchiveReferences(botRoot, contentRoot string) error {
	if err := matchingGrantNames(botRoot, contentRoot); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(contentRoot, "transfers"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		botPath := filepath.Join(botRoot, "transfers", entry.Name())
		contentPath := filepath.Join(contentRoot, "transfers", entry.Name())
		botRaw, err := store.ReadFile(botPath)
		if err != nil {
			return err
		}
		contentRaw, err := store.ReadFile(contentPath)
		if err != nil {
			return err
		}
		var botRef, contentRef blob.Reference
		if err := decodeManifest(botRaw, &botRef); err != nil {
			return err
		}
		if err := decodeManifest(contentRaw, &contentRef); err != nil {
			return err
		}
		if botRef.SchemaVersion != contentRef.SchemaVersion || botRef.BotID != contentRef.BotID || botRef.TransferID != contentRef.TransferID || botRef.Digest != contentRef.Digest || botRef.Size != contentRef.Size {
			return errors.New("old archive contains inconsistent immutable grants")
		}
		var botData, contentData any
		for _, item := range []struct {
			raw   []byte
			value *any
		}{{botRef.Data, &botData}, {contentRef.Data, &contentData}} {
			decoder := json.NewDecoder(bytes.NewReader(item.raw))
			decoder.UseNumber()
			if err := decoder.Decode(item.value); err != nil {
				return err
			}
		}
		if !sameArchiveData(contentData, botData, "") {
			return errors.New("old archive contains inconsistent grant authorization data")
		}
		if !reflect.DeepEqual(contentData, botData) {
			if err := store.WriteAtomic(contentPath, botRaw, fileMode(contentPath)); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameArchiveData(a, b any, key string) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	switch left := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		if _, signed := left["signature"]; signed {
			return false
		}
		if _, signed := left["protocolVersion"]; signed {
			return false
		}
		for k, child := range left {
			other, ok := right[k]
			if !ok || !sameArchiveData(child, other, k) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i, child := range left {
			if !sameArchiveData(child, right[i], key) {
				return false
			}
		}
		return true
	case string:
		right, ok := b.(string)
		local := strings.ToLower(key)
		return ok && filepath.IsAbs(left) && filepath.IsAbs(right) && (local == "cwd" || strings.Contains(local, "path") || strings.Contains(local, "directory"))
	}
	return false
}

func validateArchiveCredentials(b config.BotConfig, botRoot string) error {
	paths := []string{b.NATSCredentialsFile, b.NATSTLSCAFile, b.NATSTLSCertFile, b.NATSTLSKeyFile}
	if b.Harness != nil {
		paths = append(paths, b.Harness.CredentialsFile)
	}
	for _, path := range paths {
		if path != "" && (!filepath.IsAbs(path) || !within(botRoot, path)) {
			return errors.New("archive credentials must be contained in the exported Bot")
		}
	}
	return nil
}
