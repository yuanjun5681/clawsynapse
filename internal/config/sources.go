package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type fileConfig struct {
	SecurityValues      `yaml:",inline"`
	NATSServers         []string `yaml:"natsServers"`
	LocalAPIAddr        string   `yaml:"localApiAddr"`
	DataDir             string   `yaml:"dataDir"`
	IdentityKeyPath     string   `yaml:"identityKeyPath"`
	IdentityPubPath     string   `yaml:"identityPubPath"`
	HeartbeatInterval   string   `yaml:"heartbeatInterval"`
	AnnounceTTL         string   `yaml:"announceTtl"`
	AgentProduct        string   `yaml:"agentProduct"`
	AgentVersion        string   `yaml:"agentVersion"`
	Capabilities        []string `yaml:"capabilities"`
	TrustMode           string   `yaml:"trustMode"`
	TrustAutoApprove    *bool    `yaml:"trustAutoApprove"`
	AgentAdapter        string   `yaml:"agentAdapter"`
	AgentAllowedIDs     []string `yaml:"agentAllowedIds"`
	AgentAdapterTimeout string   `yaml:"agentAdapterTimeout"`
	WebhookURL          string   `yaml:"webhookUrl"`
	LogFilePath         string   `yaml:"logFilePath"`
	LogRotateMaxSizeMB  *int     `yaml:"logRotateMaxSizeMb"`
	LogRotateMaxBackups *int     `yaml:"logRotateMaxBackups"`
	LogRotateMaxAgeDays *int     `yaml:"logRotateMaxAgeDays"`
	LogRotateCompress   *bool    `yaml:"logRotateCompress"`
	DeliverablePrefixes []string `yaml:"deliverablePrefixes"`
	TransferDir         string   `yaml:"transferDir"`
	TransferMaxFileSize *int64   `yaml:"transferMaxFileSize"`
	TransferTTL         string   `yaml:"transferTtl"`
	LogLevel            string   `yaml:"logLevel"`
	LogFormat           string   `yaml:"logFormat"`
	LogAddSource        *bool    `yaml:"logAddSource"`
}

func toFileConfig(cfg Config) fileConfig {
	las := cfg.LogAddSource
	taa := cfg.TrustAutoApprove
	mfs := cfg.TransferMaxFileSize
	return fileConfig{
		SecurityValues:      securityValuesFromConfig(cfg),
		NATSServers:         cfg.NATSServers,
		LocalAPIAddr:        cfg.LocalAPIAddr,
		DataDir:             cfg.DataDir,
		IdentityKeyPath:     cfg.IdentityKeyPath,
		IdentityPubPath:     cfg.IdentityPubPath,
		HeartbeatInterval:   cfg.HeartbeatInterval,
		AnnounceTTL:         cfg.AnnounceTTL,
		AgentProduct:        cfg.AgentProduct,
		AgentVersion:        cfg.AgentVersion,
		Capabilities:        cloneStrings(cfg.Capabilities),
		TrustMode:           cfg.TrustMode,
		TrustAutoApprove:    &taa,
		AgentAdapter:        cfg.AgentAdapter,
		AgentAllowedIDs:     cloneStrings(cfg.AgentAllowedIDs),
		AgentAdapterTimeout: cfg.AgentAdapterTimeout,
		WebhookURL:          cfg.WebhookURL,
		LogFilePath:         cfg.LogFilePath,
		LogRotateMaxSizeMB:  &cfg.LogRotateMaxSizeMB,
		LogRotateMaxBackups: &cfg.LogRotateMaxBackups,
		LogRotateMaxAgeDays: &cfg.LogRotateMaxAgeDays,
		LogRotateCompress:   &cfg.LogRotateCompress,
		DeliverablePrefixes: cfg.DeliverablePrefixes,
		TransferDir:         cfg.TransferDir,
		TransferMaxFileSize: &mfs,
		TransferTTL:         cfg.TransferTTL,
		LogLevel:            cfg.LogLevel,
		LogFormat:           cfg.LogFormat,
		LogAddSource:        &las,
	}
}

func loadConfigValues(path string, required bool) (configValues, error) {
	path, err := expandPath(path)
	if err != nil {
		return configValues{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return configValues{}, nil
		}
		return configValues{}, fmt.Errorf("read config file: %w", err)
	}

	var cfg fileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return configValues{}, fmt.Errorf("parse config file: %w", err)
	}

	values := configValues{
		SecurityValues:      cfg.SecurityValues,
		NATSServers:         cloneStrings(cfg.NATSServers),
		LocalAPIAddr:        strings.TrimSpace(cfg.LocalAPIAddr),
		DataDir:             strings.TrimSpace(cfg.DataDir),
		IdentityKeyPath:     strings.TrimSpace(cfg.IdentityKeyPath),
		IdentityPubPath:     strings.TrimSpace(cfg.IdentityPubPath),
		AgentProduct:        strings.TrimSpace(cfg.AgentProduct),
		AgentVersion:        strings.TrimSpace(cfg.AgentVersion),
		Capabilities:        cloneStrings(cfg.Capabilities),
		TrustMode:           strings.TrimSpace(cfg.TrustMode),
		AgentAdapter:        strings.TrimSpace(cfg.AgentAdapter),
		AgentAllowedIDs:     append([]string(nil), cfg.AgentAllowedIDs...),
		AgentAdapterTimeout: parseDurationValue(cfg.AgentAdapterTimeout, 0),
		WebhookURL:          strings.TrimSpace(cfg.WebhookURL),
		LogFilePath:         strings.TrimSpace(cfg.LogFilePath),
		DeliverablePrefixes: cloneStrings(cfg.DeliverablePrefixes),
		TransferDir:         strings.TrimSpace(cfg.TransferDir),
		LogLevel:            strings.TrimSpace(cfg.LogLevel),
		LogFormat:           strings.TrimSpace(cfg.LogFormat),
	}
	var securityFlags struct {
		AllowRemoteAPI *bool `yaml:"allowRemoteAPI"`
		Production     *bool `yaml:"production"`
	}
	if err := yaml.Unmarshal(data, &securityFlags); err != nil {
		return configValues{}, fmt.Errorf("parse security config: %w", err)
	}
	values.allowRemoteSet = securityFlags.AllowRemoteAPI != nil
	values.productionSet = securityFlags.Production != nil
	if cfg.LogRotateMaxSizeMB != nil {
		values.LogRotateMaxSizeMB = *cfg.LogRotateMaxSizeMB
	}
	if cfg.LogRotateMaxBackups != nil {
		values.LogRotateMaxBackups = *cfg.LogRotateMaxBackups
	}
	if cfg.LogRotateMaxAgeDays != nil {
		values.LogRotateMaxAgeDays = *cfg.LogRotateMaxAgeDays
	}
	if cfg.LogRotateCompress != nil {
		values.LogRotateCompress = *cfg.LogRotateCompress
	}
	if cfg.LogAddSource != nil {
		values.LogAddSource = *cfg.LogAddSource
	}
	if cfg.TrustAutoApprove != nil {
		values.TrustAutoApprove = *cfg.TrustAutoApprove
		values.TrustAutoApproveSet = true
	}

	if cfg.HeartbeatInterval != "" {
		values.Heartbeat = parseDurationValue(cfg.HeartbeatInterval, 0)
	}
	if cfg.AnnounceTTL != "" {
		values.AnnounceTTL = parseDurationValue(cfg.AnnounceTTL, 0)
	}
	if cfg.TransferMaxFileSize != nil {
		values.TransferMaxFileSize = *cfg.TransferMaxFileSize
	}
	if cfg.TransferTTL != "" {
		values.TransferTTL = parseDurationValue(cfg.TransferTTL, 0)
	}

	return values, nil
}

func loadDotEnvValues() configValues {
	wd, err := os.Getwd()
	if err != nil {
		return configValues{}
	}
	root := findProjectRoot(wd)

	data, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		return configValues{}
	}

	entries := parseDotEnv(string(data))
	return loadValuesFromMap(entries)
}

func findProjectRoot(start string) string {
	dir := start
	for {
		if fileExists(filepath.Join(dir, ".git")) || fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadOSEnvValues() configValues {
	entries := make(map[string]string)
	for _, raw := range os.Environ() {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		entries[key] = value
	}
	return loadValuesFromMap(entries)
}

func loadValuesFromMap(values map[string]string) configValues {
	trustAutoApproveRaw := strings.TrimSpace(values["TRUST_AUTO_APPROVE"])
	cfg := configValues{
		SecurityValues: SecurityValues{
			APITokenPath:        strings.TrimSpace(values["API_TOKEN_PATH"]),
			AllowRemoteAPI:      parseBoolValue(values["ALLOW_REMOTE_API"]),
			APITLSCertFile:      strings.TrimSpace(values["API_TLS_CERT_FILE"]),
			APITLSKeyFile:       strings.TrimSpace(values["API_TLS_KEY_FILE"]),
			Production:          parseBoolValue(values["PRODUCTION"]),
			NATSCredentialsFile: strings.TrimSpace(values["NATS_CREDENTIALS_FILE"]),
			NATSTLSCAFile:       strings.TrimSpace(values["NATS_TLS_CA_FILE"]),
			NATSTLSCertFile:     strings.TrimSpace(values["NATS_TLS_CERT_FILE"]),
			NATSTLSKeyFile:      strings.TrimSpace(values["NATS_TLS_KEY_FILE"]),
			NATSTLSServerName:   strings.TrimSpace(values["NATS_TLS_SERVER_NAME"]),
			allowRemoteSet:      strings.TrimSpace(values["ALLOW_REMOTE_API"]) != "",
			productionSet:       strings.TrimSpace(values["PRODUCTION"]) != "",
		},
		NATSServers:         splitCSV(values["NATS_SERVERS"]),
		LocalAPIAddr:        strings.TrimSpace(values["LOCAL_API_ADDR"]),
		DataDir:             strings.TrimSpace(values["DATA_DIR"]),
		IdentityKeyPath:     strings.TrimSpace(values["IDENTITY_KEY_PATH"]),
		IdentityPubPath:     strings.TrimSpace(values["IDENTITY_PUB_PATH"]),
		Heartbeat:           parseDurationValue(values["HEARTBEAT_INTERVAL_MS"], 0),
		AnnounceTTL:         parseDurationValue(values["ANNOUNCE_TTL_MS"], 0),
		AgentProduct:        strings.TrimSpace(values["AGENT_PRODUCT"]),
		AgentVersion:        strings.TrimSpace(values["AGENT_VERSION"]),
		Capabilities:        splitCSV(values["AGENT_CAPABILITIES"]),
		TrustMode:           strings.TrimSpace(values["TRUST_MODE"]),
		AgentAdapter:        strings.TrimSpace(values["AGENT_ADAPTER"]),
		AgentAllowedIDs:     splitCSV(values["AGENT_ALLOWED_IDS"]),
		AgentAdapterTimeout: parseDurationValue(values["AGENT_ADAPTER_TIMEOUT"], 0),
		WebhookURL:          strings.TrimSpace(values["WEBHOOK_URL"]),
		WebhookAuthToken:    strings.TrimSpace(values["WEBHOOK_AUTH_TOKEN"]),
		LogFilePath:         strings.TrimSpace(values["LOG_FILE_PATH"]),
		LogRotateMaxSizeMB:  int(parseIntValue(values["LOG_ROTATE_MAX_SIZE_MB"])),
		LogRotateMaxBackups: int(parseIntValue(values["LOG_ROTATE_MAX_BACKUPS"])),
		LogRotateMaxAgeDays: int(parseIntValue(values["LOG_ROTATE_MAX_AGE_DAYS"])),
		LogRotateCompress:   parseBoolValue(values["LOG_ROTATE_COMPRESS"]),
		DeliverablePrefixes: splitCSV(values["DELIVERABLE_PREFIXES"]),
		TransferDir:         strings.TrimSpace(values["TRANSFER_DIR"]),
		TransferMaxFileSize: parseIntValue(values["TRANSFER_MAX_FILE_SIZE"]),
		TransferTTL:         parseDurationValue(values["TRANSFER_TTL"], 0),
		LogLevel:            strings.TrimSpace(values["LOG_LEVEL"]),
		LogFormat:           strings.TrimSpace(values["LOG_FORMAT"]),
		LogAddSource:        parseBoolValue(values["LOG_ADD_SOURCE"]),
	}
	if trustAutoApproveRaw != "" {
		cfg.TrustAutoApprove = parseBoolValue(trustAutoApproveRaw)
		cfg.TrustAutoApproveSet = true
	}
	return cfg
}

func parseDurationValue(v string, fallback time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}

	if d, err := time.ParseDuration(v); err == nil {
		return d
	}

	ms, err := time.ParseDuration(v + "ms")
	if err != nil {
		return fallback
	}
	return ms
}

func parseDotEnv(data string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				value = strings.Trim(value, "\"")
			}
			if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
				value = strings.Trim(value, "'")
			}
		}
		values[key] = value
	}
	return values
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseIntValue(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseBoolValue(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	ok, err := strconv.ParseBool(v)
	if err != nil {
		return false
	}
	return ok
}
