package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultNATSServers         = "nats://220.168.146.21:9414"
	defaultLocalAPIAddr        = "127.0.0.1:18080"
	defaultHeartbeatInterval   = 15 * time.Second
	defaultAnnounceTTL         = 30 * time.Second
	defaultAgentProduct        = "clawsynapse"
	defaultCapabilities        = "chat,tools"
	defaultTrustMode           = "tofu"
	defaultAgentAdapter        = "default"
	defaultAgentAdapterTimeout = 10 * time.Minute
	defaultLogLevel            = "info"
	defaultLogFormat           = "json"
	defaultLogRotateMaxSizeMB  = 10
	defaultLogRotateMaxBackups = 3
	defaultLogRotateMaxAgeDays = 7
	defaultDeliverablePrefixes = "chat,task"
	defaultTransferMaxFileSize = 104857600 // 100MB
	defaultTransferTTL         = "24h"
)

type Config struct {
	NATSServers         []string `json:"natsServers"`
	LocalAPIAddr        string   `json:"localApiAddr"`
	DataDir             string   `json:"dataDir"`
	IdentityKeyPath     string   `json:"identityKeyPath"`
	IdentityPubPath     string   `json:"identityPubPath"`
	HeartbeatInterval   string   `json:"heartbeatInterval"`
	AnnounceTTL         string   `json:"announceTtl"`
	AgentProduct        string   `json:"agentProduct"`
	AgentVersion        string   `json:"agentVersion"`
	Capabilities        []string `json:"capabilities"`
	TrustMode           string   `json:"trustMode"`
	TrustAutoApprove    bool     `json:"trustAutoApprove"`
	AgentAdapter        string   `json:"agentAdapter"`
	AgentAdapterTimeout string   `json:"agentAdapterTimeout"`
	WebhookURL          string   `json:"webhookUrl"`
	WebhookAuthToken    string   `json:"-"`
	LogLevel            string   `json:"logLevel"`
	LogFormat           string   `json:"logFormat"`
	LogFilePath         string   `json:"logFilePath"`
	LogRotateMaxSizeMB  int      `json:"logRotateMaxSizeMb"`
	LogRotateMaxBackups int      `json:"logRotateMaxBackups"`
	LogRotateMaxAgeDays int      `json:"logRotateMaxAgeDays"`
	LogRotateCompress   bool     `json:"logRotateCompress"`
	DeliverablePrefixes []string `json:"deliverablePrefixes"`
	TransferDir         string   `json:"transferDir"`
	TransferMaxFileSize int64    `json:"transferMaxFileSize"`
	TransferTTL         string   `json:"transferTtl"`
	LogAddSource        bool     `json:"logAddSource"`
	CheckConfig         bool     `json:"checkConfig"`
	ConfigPath          string   `json:"-"`
}

func Validate(cfg Config) error {
	if len(cfg.NATSServers) == 0 {
		return errors.New("nats servers is empty")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.TrustMode))
	if mode != "open" && mode != "tofu" && mode != "explicit" {
		return errors.New("trust mode must be one of: open|tofu|explicit")
	}
	adapterName := strings.ToLower(strings.TrimSpace(cfg.AgentAdapter))
	if adapterName == "" {
		adapterName = defaultAgentAdapter
	}
	if adapterName != "default" && adapterName != "openclaw" && adapterName != "opencode" && adapterName != "webhook" {
		return errors.New("agent adapter must be one of: default|openclaw|opencode|webhook")
	}
	if strings.TrimSpace(cfg.AgentAdapterTimeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(cfg.AgentAdapterTimeout))
		if err != nil || d <= 0 {
			return errors.New("agent adapter timeout must be a positive duration")
		}
	}
	if adapterName == "webhook" && strings.TrimSpace(cfg.WebhookURL) == "" {
		return errors.New("webhook url is required when agent adapter is webhook")
	}
	level := strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return errors.New("log level must be one of: debug|info|warn|error")
	}
	format := strings.ToLower(strings.TrimSpace(cfg.LogFormat))
	if format != "json" && format != "text" {
		return errors.New("log format must be one of: json|text")
	}
	if strings.TrimSpace(cfg.LogFilePath) != "" {
		if cfg.LogRotateMaxSizeMB <= 0 {
			return errors.New("log rotate max size mb must be greater than 0")
		}
		if cfg.LogRotateMaxBackups < 0 {
			return errors.New("log rotate max backups must be greater than or equal to 0")
		}
		if cfg.LogRotateMaxAgeDays < 0 {
			return errors.New("log rotate max age days must be greater than or equal to 0")
		}
	}
	return nil
}

func SaveToFile(path string, cfg Config) error {
	fc := toFileConfig(cfg)
	data, err := yaml.Marshal(fc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

type runtimeConfig struct {
	NATSServers         []string
	LocalAPIAddr        string
	DataDir             string
	IdentityKeyPath     string
	IdentityPubPath     string
	Heartbeat           time.Duration
	AnnounceTTL         time.Duration
	AgentProduct        string
	AgentVersion        string
	Capabilities        []string
	TrustMode           string
	TrustAutoApprove    bool
	AgentAdapter        string
	AgentAdapterTimeout time.Duration
	WebhookURL          string
	LogFilePath         string
	LogRotateMaxSizeMB  int
	LogRotateMaxBackups int
	LogRotateMaxAgeDays int
	LogRotateCompress   bool
	DeliverablePrefixes []string
	TransferDir         string
	TransferMaxFileSize int64
	TransferTTL         time.Duration
	LogLevel            string
	LogFormat           string
	LogAddSource        bool
	CheckConfig         bool
}

type configValues struct {
	NATSServers         []string
	LocalAPIAddr        string
	DataDir             string
	IdentityKeyPath     string
	IdentityPubPath     string
	Heartbeat           time.Duration
	AnnounceTTL         time.Duration
	AgentProduct        string
	AgentVersion        string
	Capabilities        []string
	TrustMode           string
	TrustAutoApprove    bool
	TrustAutoApproveSet bool
	AgentAdapter        string
	AgentAdapterTimeout time.Duration
	WebhookURL          string
	WebhookAuthToken    string
	LogFilePath         string
	LogRotateMaxSizeMB  int
	LogRotateMaxBackups int
	LogRotateMaxAgeDays int
	LogRotateCompress   bool
	DeliverablePrefixes []string
	TransferDir         string
	TransferMaxFileSize int64
	TransferTTL         time.Duration
	LogLevel            string
	LogFormat           string
	LogAddSource        bool
}

func (c Config) Runtime() runtimeConfig {
	h, _ := time.ParseDuration(c.HeartbeatInterval)
	t, _ := time.ParseDuration(c.AnnounceTTL)
	tt, _ := time.ParseDuration(c.TransferTTL)
	at, _ := time.ParseDuration(c.AgentAdapterTimeout)
	return runtimeConfig{
		NATSServers:         c.NATSServers,
		LocalAPIAddr:        c.LocalAPIAddr,
		DataDir:             c.DataDir,
		IdentityKeyPath:     c.IdentityKeyPath,
		IdentityPubPath:     c.IdentityPubPath,
		Heartbeat:           h,
		AnnounceTTL:         t,
		AgentProduct:        c.AgentProduct,
		AgentVersion:        c.AgentVersion,
		Capabilities:        cloneStrings(c.Capabilities),
		TrustMode:           c.TrustMode,
		TrustAutoApprove:    c.TrustAutoApprove,
		AgentAdapter:        c.AgentAdapter,
		AgentAdapterTimeout: at,
		WebhookURL:          c.WebhookURL,
		LogFilePath:         c.LogFilePath,
		LogRotateMaxSizeMB:  c.LogRotateMaxSizeMB,
		LogRotateMaxBackups: c.LogRotateMaxBackups,
		LogRotateMaxAgeDays: c.LogRotateMaxAgeDays,
		LogRotateCompress:   c.LogRotateCompress,
		DeliverablePrefixes: c.DeliverablePrefixes,
		TransferDir:         c.TransferDir,
		TransferMaxFileSize: c.TransferMaxFileSize,
		TransferTTL:         tt,
		LogLevel:            c.LogLevel,
		LogFormat:           c.LogFormat,
		LogAddSource:        c.LogAddSource,
		CheckConfig:         c.CheckConfig,
	}
}

func LoadFromOS(args []string) (Config, error) {
	configPath, explicitConfigPath, err := resolveConfigPath(args)
	if err != nil {
		return Config{}, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}

	defaultDataDir := filepath.Join(home, ".clawsynapse")
	defaults := defaultConfigValues(defaultDataDir)
	loaded, err := loadConfigValues(configPath, explicitConfigPath)
	if err != nil {
		return Config{}, err
	}
	merged := mergeConfigValues(defaults, loaded)
	merged = mergeConfigValues(merged, loadDotEnvValues())
	merged = mergeConfigValues(merged, loadOSEnvValues())

	fs := flag.NewFlagSet("clawsynapsed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		natsServers         = fs.String("nats-servers", strings.Join(merged.NATSServers, ","), "comma separated nats servers")
		apiAddr             = fs.String("local-api-addr", merged.LocalAPIAddr, "http api address")
		dataDir             = fs.String("data-dir", merged.DataDir, "state directory")
		identityKeyPath     = fs.String("identity-key-path", merged.IdentityKeyPath, "private key file path")
		identityPubPath     = fs.String("identity-pub-path", merged.IdentityPubPath, "public key file path")
		heartbeat           = fs.Duration("heartbeat", merged.Heartbeat, "announce heartbeat interval")
		announceTTL         = fs.Duration("announce-ttl", merged.AnnounceTTL, "announce ttl")
		agentProduct        = fs.String("agent-product", merged.AgentProduct, "agent product advertised in discovery")
		agentVersion        = fs.String("agent-version", merged.AgentVersion, "agent version advertised in discovery; defaults to daemon version")
		capabilities        = fs.String("capabilities", strings.Join(merged.Capabilities, ","), "comma separated capabilities advertised in discovery")
		trustMode           = fs.String("trust-mode", merged.TrustMode, "trust mode: open|tofu|explicit")
		trustAutoApprove    = fs.Bool("trust-auto-approve", merged.TrustAutoApprove, "automatically approve valid inbound trust requests")
		agentAdapter        = fs.String("agent-adapter", merged.AgentAdapter, "agent adapter: default|openclaw|opencode|webhook")
		agentAdapterTimeout = fs.Duration("agent-adapter-timeout", merged.AgentAdapterTimeout, "timeout for delivering a message to the agent adapter")
		webhookURLFlag      = fs.String("webhook-url", merged.WebhookURL, "webhook url for webhook adapter")
		logLevel            = fs.String("log-level", merged.LogLevel, "log level: debug|info|warn|error")
		logFormat           = fs.String("log-format", merged.LogFormat, "log format: json|text")
		logFilePath         = fs.String("log-file-path", merged.LogFilePath, "log file path with rotation enabled when set")
		logRotateMaxSizeMB  = fs.Int("log-rotate-max-size-mb", merged.LogRotateMaxSizeMB, "max size in MB before rotating the log file")
		logRotateMaxBackups = fs.Int("log-rotate-max-backups", merged.LogRotateMaxBackups, "max number of old rotated log files to retain")
		logRotateMaxAgeDays = fs.Int("log-rotate-max-age-days", merged.LogRotateMaxAgeDays, "max age in days for old rotated log files")
		logRotateCompress   = fs.Bool("log-rotate-compress", merged.LogRotateCompress, "compress rotated log files")
		deliverablePrefixes = fs.String("deliverable-prefixes", strings.Join(merged.DeliverablePrefixes, ","), "comma separated message type prefixes that are deliverable to agent handlers")
		transferDir         = fs.String("transfer-dir", merged.TransferDir, "directory for received transfer files")
		transferMaxFileSize = fs.Int64("transfer-max-file-size", merged.TransferMaxFileSize, "max file size for transfer in bytes")
		transferTTL         = fs.Duration("transfer-ttl", merged.TransferTTL, "object store bucket TTL for transfers")
		logAddSource        = fs.Bool("log-add-source", merged.LogAddSource, "include source location in logs")
		_                   = fs.String("config", configPath, "config file path")
		checkConfig         = fs.Bool("check-config", false, "print config and exit")
	)

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	rawServers := splitCSV(*natsServers)
	if len(rawServers) == 0 {
		return Config{}, errors.New("nats servers is empty")
	}

	mode := strings.ToLower(strings.TrimSpace(*trustMode))
	if mode != "open" && mode != "tofu" && mode != "explicit" {
		return Config{}, errors.New("trust mode must be one of: open|tofu|explicit")
	}
	adapterName := strings.ToLower(strings.TrimSpace(*agentAdapter))
	if adapterName == "" {
		adapterName = defaultAgentAdapter
	}
	if adapterName != "default" && adapterName != "openclaw" && adapterName != "opencode" && adapterName != "webhook" {
		return Config{}, errors.New("agent adapter must be one of: default|openclaw|opencode|webhook")
	}

	webhookURL := strings.TrimSpace(*webhookURLFlag)
	if adapterName == "webhook" && webhookURL == "" {
		return Config{}, errors.New("webhook url is required when agent adapter is webhook (set --webhook-url or WEBHOOK_URL)")
	}
	level := strings.ToLower(strings.TrimSpace(*logLevel))
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return Config{}, errors.New("log level must be one of: debug|info|warn|error")
	}
	format := strings.ToLower(strings.TrimSpace(*logFormat))
	if format != "json" && format != "text" {
		return Config{}, errors.New("log format must be one of: json|text")
	}
	resolvedLogFilePath, err := expandPath(*logFilePath)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(resolvedLogFilePath) != "" {
		if *logRotateMaxSizeMB <= 0 {
			return Config{}, errors.New("log rotate max size mb must be greater than 0")
		}
		if *logRotateMaxBackups < 0 {
			return Config{}, errors.New("log rotate max backups must be greater than or equal to 0")
		}
		if *logRotateMaxAgeDays < 0 {
			return Config{}, errors.New("log rotate max age days must be greater than or equal to 0")
		}
	}

	resolvedDataDir, err := expandPath(*dataDir)
	if err != nil {
		return Config{}, err
	}
	resolvedKey, err := expandPath(*identityKeyPath)
	if err != nil {
		return Config{}, err
	}
	resolvedPub, err := expandPath(*identityPubPath)
	if err != nil {
		return Config{}, err
	}
	resolvedTransferDir, err := expandPath(*transferDir)
	if err != nil {
		return Config{}, err
	}

	return Config{
		NATSServers:         rawServers,
		LocalAPIAddr:        strings.TrimSpace(*apiAddr),
		DataDir:             resolvedDataDir,
		IdentityKeyPath:     resolvedKey,
		IdentityPubPath:     resolvedPub,
		HeartbeatInterval:   heartbeat.String(),
		AnnounceTTL:         announceTTL.String(),
		AgentProduct:        strings.TrimSpace(*agentProduct),
		AgentVersion:        strings.TrimSpace(*agentVersion),
		Capabilities:        splitCSV(*capabilities),
		TrustMode:           mode,
		TrustAutoApprove:    *trustAutoApprove,
		AgentAdapter:        adapterName,
		AgentAdapterTimeout: agentAdapterTimeout.String(),
		WebhookURL:          webhookURL,
		WebhookAuthToken:    merged.WebhookAuthToken,
		LogFilePath:         resolvedLogFilePath,
		LogRotateMaxSizeMB:  *logRotateMaxSizeMB,
		LogRotateMaxBackups: *logRotateMaxBackups,
		LogRotateMaxAgeDays: *logRotateMaxAgeDays,
		LogRotateCompress:   *logRotateCompress,
		DeliverablePrefixes: splitCSV(*deliverablePrefixes),
		TransferDir:         resolvedTransferDir,
		TransferMaxFileSize: *transferMaxFileSize,
		TransferTTL:         transferTTL.String(),
		LogLevel:            level,
		LogFormat:           format,
		LogAddSource:        *logAddSource,
		CheckConfig:         *checkConfig,
		ConfigPath:          configPath,
	}, nil
}

func defaultConfigValues(defaultDataDir string) configValues {
	ttl, _ := time.ParseDuration(defaultTransferTTL)
	return configValues{
		NATSServers:         []string{defaultNATSServers},
		LocalAPIAddr:        defaultLocalAPIAddr,
		DataDir:             defaultDataDir,
		IdentityKeyPath:     filepath.Join(defaultDataDir, "identity.key"),
		IdentityPubPath:     filepath.Join(defaultDataDir, "identity.pub"),
		Heartbeat:           defaultHeartbeatInterval,
		AnnounceTTL:         defaultAnnounceTTL,
		AgentProduct:        defaultAgentProduct,
		Capabilities:        splitCSV(defaultCapabilities),
		TrustMode:           defaultTrustMode,
		TrustAutoApprove:    false,
		TrustAutoApproveSet: true,
		AgentAdapter:        defaultAgentAdapter,
		AgentAdapterTimeout: defaultAgentAdapterTimeout,
		LogRotateMaxSizeMB:  defaultLogRotateMaxSizeMB,
		LogRotateMaxBackups: defaultLogRotateMaxBackups,
		LogRotateMaxAgeDays: defaultLogRotateMaxAgeDays,
		DeliverablePrefixes: splitCSV(defaultDeliverablePrefixes),
		TransferDir:         filepath.Join(defaultDataDir, "transfers"),
		TransferMaxFileSize: defaultTransferMaxFileSize,
		TransferTTL:         ttl,
		LogLevel:            defaultLogLevel,
		LogFormat:           defaultLogFormat,
	}
}

func mergeConfigValues(base, override configValues) configValues {
	if len(override.NATSServers) > 0 {
		base.NATSServers = append([]string(nil), override.NATSServers...)
	}
	if strings.TrimSpace(override.LocalAPIAddr) != "" {
		base.LocalAPIAddr = strings.TrimSpace(override.LocalAPIAddr)
	}
	if strings.TrimSpace(override.DataDir) != "" {
		base.DataDir = strings.TrimSpace(override.DataDir)
	}
	if strings.TrimSpace(override.IdentityKeyPath) != "" {
		base.IdentityKeyPath = strings.TrimSpace(override.IdentityKeyPath)
	}
	if strings.TrimSpace(override.IdentityPubPath) != "" {
		base.IdentityPubPath = strings.TrimSpace(override.IdentityPubPath)
	}
	if override.Heartbeat > 0 {
		base.Heartbeat = override.Heartbeat
	}
	if override.AnnounceTTL > 0 {
		base.AnnounceTTL = override.AnnounceTTL
	}
	if strings.TrimSpace(override.AgentProduct) != "" {
		base.AgentProduct = strings.TrimSpace(override.AgentProduct)
	}
	if strings.TrimSpace(override.AgentVersion) != "" {
		base.AgentVersion = strings.TrimSpace(override.AgentVersion)
	}
	if len(override.Capabilities) > 0 {
		base.Capabilities = cloneStrings(override.Capabilities)
	}
	if strings.TrimSpace(override.TrustMode) != "" {
		base.TrustMode = strings.TrimSpace(override.TrustMode)
	}
	if override.TrustAutoApproveSet {
		base.TrustAutoApprove = override.TrustAutoApprove
		base.TrustAutoApproveSet = true
	}
	if strings.TrimSpace(override.AgentAdapter) != "" {
		base.AgentAdapter = strings.TrimSpace(override.AgentAdapter)
	}
	if override.AgentAdapterTimeout > 0 {
		base.AgentAdapterTimeout = override.AgentAdapterTimeout
	}
	if strings.TrimSpace(override.WebhookURL) != "" {
		base.WebhookURL = strings.TrimSpace(override.WebhookURL)
	}
	if strings.TrimSpace(override.WebhookAuthToken) != "" {
		base.WebhookAuthToken = strings.TrimSpace(override.WebhookAuthToken)
	}
	if strings.TrimSpace(override.LogFilePath) != "" {
		base.LogFilePath = strings.TrimSpace(override.LogFilePath)
	}
	if override.LogRotateMaxSizeMB > 0 {
		base.LogRotateMaxSizeMB = override.LogRotateMaxSizeMB
	}
	if override.LogRotateMaxBackups > 0 {
		base.LogRotateMaxBackups = override.LogRotateMaxBackups
	}
	if override.LogRotateMaxAgeDays > 0 {
		base.LogRotateMaxAgeDays = override.LogRotateMaxAgeDays
	}
	if override.LogRotateCompress {
		base.LogRotateCompress = true
	}
	if len(override.DeliverablePrefixes) > 0 {
		base.DeliverablePrefixes = append([]string(nil), override.DeliverablePrefixes...)
	}
	if strings.TrimSpace(override.TransferDir) != "" {
		base.TransferDir = strings.TrimSpace(override.TransferDir)
	}
	if override.TransferMaxFileSize > 0 {
		base.TransferMaxFileSize = override.TransferMaxFileSize
	}
	if override.TransferTTL > 0 {
		base.TransferTTL = override.TransferTTL
	}
	if strings.TrimSpace(override.LogLevel) != "" {
		base.LogLevel = strings.TrimSpace(override.LogLevel)
	}
	if strings.TrimSpace(override.LogFormat) != "" {
		base.LogFormat = strings.TrimSpace(override.LogFormat)
	}
	if override.LogAddSource {
		base.LogAddSource = true
	}
	return base
}

func resolveConfigPath(args []string) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}

	defaultPath := filepath.Join(home, ".clawsynapse", "config.yaml")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--config" {
			if i+1 >= len(args) {
				return "", false, errors.New("missing value for --config")
			}
			return args[i+1], true, nil
		}
		if value, ok := strings.CutPrefix(arg, "--config="); ok {
			if strings.TrimSpace(value) == "" {
				return "", false, errors.New("missing value for --config")
			}
			return value, true, nil
		}
	}

	return defaultPath, false, nil
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
