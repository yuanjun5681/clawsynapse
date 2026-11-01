package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"clawsynapse/internal/store"
	"gopkg.in/yaml.v3"
)

// LoadLegacy reads one explicitly selected legacy config. Neither the process
// environment nor .env may silently change the source of an offline migration.
func LoadLegacy(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return Config{}, errors.New("legacy config path is required")
	}
	path, err := expandPath(path)
	if err != nil {
		return Config{}, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	data, err := store.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var header map[string]any
	if err := yaml.Unmarshal(data, &header); err != nil {
		return Config{}, err
	}
	if _, ok := header["schemaVersion"]; ok {
		return Config{}, errors.New("versioned configuration is not a legacy config")
	}
	loaded, err := loadConfigValues(path, true)
	if err != nil {
		return Config{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}
	root := loaded.DataDir
	if root == "" {
		root = filepath.Join(home, ".clawsynapse")
	}
	root, err = expandPath(root)
	if err != nil {
		return Config{}, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Config{}, err
	}
	v := mergeConfigValues(defaultConfigValues(root), loaded)
	c := Config{APITokenPath: v.APITokenPath, AllowRemoteAPI: v.AllowRemoteAPI, APITLSCertFile: v.APITLSCertFile, APITLSKeyFile: v.APITLSKeyFile, Production: v.Production, NATSCredentialsFile: v.NATSCredentialsFile, NATSTLSCAFile: v.NATSTLSCAFile, NATSTLSCertFile: v.NATSTLSCertFile, NATSTLSKeyFile: v.NATSTLSKeyFile, NATSTLSServerName: v.NATSTLSServerName, NATSServers: v.NATSServers, LocalAPIAddr: v.LocalAPIAddr, DataDir: root, IdentityKeyPath: v.IdentityKeyPath, IdentityPubPath: v.IdentityPubPath, HeartbeatInterval: v.Heartbeat.String(), AnnounceTTL: v.AnnounceTTL.String(), AgentProduct: v.AgentProduct, AgentVersion: v.AgentVersion, DisplayName: v.DisplayName, Labels: v.Labels, TrustMode: v.TrustMode, TrustAutoApprove: v.TrustAutoApprove, AgentAdapter: v.AgentAdapter, AgentAllowedIDs: v.AgentAllowedIDs, AgentAdapterTimeout: v.AgentAdapterTimeout.String(), WebhookURL: v.WebhookURL, WebhookAuthToken: v.WebhookAuthToken, LogLevel: v.LogLevel, LogFormat: v.LogFormat, LogFilePath: v.LogFilePath, LogRotateMaxSizeMB: v.LogRotateMaxSizeMB, LogRotateMaxBackups: v.LogRotateMaxBackups, LogRotateMaxAgeDays: v.LogRotateMaxAgeDays, LogRotateCompress: v.LogRotateCompress, LogAddSource: v.LogAddSource, DeliverablePrefixes: v.DeliverablePrefixes, TransferDir: v.TransferDir, TransferMaxFileSize: v.TransferMaxFileSize, TransferTTL: v.TransferTTL.String(), ConfigPath: path}
	// The old file format never persisted the webhook secret. An explicit
	// credentials-file extension is accepted only here, never by normal startup.
	if raw, ok := header["webhookAuthToken"].(string); ok {
		c.WebhookAuthToken = raw
	}
	for _, p := range []*string{&c.IdentityKeyPath, &c.IdentityPubPath, &c.TransferDir, &c.APITokenPath, &c.APITLSCertFile, &c.APITLSKeyFile, &c.NATSCredentialsFile, &c.NATSTLSCAFile, &c.NATSTLSCertFile, &c.NATSTLSKeyFile, &c.LogFilePath} {
		if *p != "" {
			*p, err = expandPath(*p)
			if err != nil {
				return Config{}, err
			}
			*p, err = filepath.Abs(*p)
			if err != nil {
				return Config{}, err
			}
		}
	}
	if c.APITokenPath == "" {
		c.APITokenPath = filepath.Join(root, "api.token")
	}
	if err := Validate(c); err != nil {
		return Config{}, fmt.Errorf("legacy config: %w", err)
	}
	return c, nil
}
