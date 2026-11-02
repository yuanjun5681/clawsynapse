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

	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

const (
	defaultNATSServers         = "nats://220.168.146.21:9414"
	defaultLocalAPIAddr        = "127.0.0.1:18080"
	defaultHeartbeatInterval   = 15 * time.Second
	defaultAnnounceTTL         = 30 * time.Second
	defaultAgentProduct        = "clawsynapse"
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
	Role                string       `json:"role,omitempty"`
	APITokenPath        string       `json:"apiTokenPath"`
	AllowRemoteAPI      bool         `json:"allowRemoteAPI"`
	APITLSCertFile      string       `json:"apiTLSCertFile"`
	APITLSKeyFile       string       `json:"apiTLSKeyFile"`
	Production          bool         `json:"production"`
	NATSCredentialsFile string       `json:"natsCredentialsFile"`
	NATSTLSCAFile       string       `json:"natsTLSCAFile"`
	NATSTLSCertFile     string       `json:"natsTLSCertFile"`
	NATSTLSKeyFile      string       `json:"natsTLSKeyFile"`
	NATSTLSServerName   string       `json:"natsTLSServerName"`
	NATSServers         []string     `json:"natsServers"`
	LocalAPIAddr        string       `json:"localApiAddr"`
	DataDir             string       `json:"dataDir"`
	IdentityKeyPath     string       `json:"identityKeyPath"`
	IdentityPubPath     string       `json:"identityPubPath"`
	HeartbeatInterval   string       `json:"heartbeatInterval"`
	AnnounceTTL         string       `json:"announceTtl"`
	AgentProduct        string       `json:"agentProduct"`
	AgentVersion        string       `json:"agentVersion"`
	DisplayName         string       `json:"displayName"`
	Labels              types.Labels `json:"labels,omitempty"`
	TrustMode           string       `json:"trustMode"`
	TrustAutoApprove    bool         `json:"trustAutoApprove"`
	AgentAdapter        string       `json:"agentAdapter"`
	AgentAllowedIDs     []string     `json:"agentAllowedIds"`
	AgentAdapterTimeout string       `json:"agentAdapterTimeout"`
	WebhookURL          string       `json:"webhookUrl"`
	WebhookAuthToken    string       `json:"-"`
	LogLevel            string       `json:"logLevel"`
	LogFormat           string       `json:"logFormat"`
	LogFilePath         string       `json:"logFilePath"`
	LogRotateMaxSizeMB  int          `json:"logRotateMaxSizeMb"`
	LogRotateMaxBackups int          `json:"logRotateMaxBackups"`
	LogRotateMaxAgeDays int          `json:"logRotateMaxAgeDays"`
	LogRotateCompress   bool         `json:"logRotateCompress"`
	DeliverablePrefixes []string     `json:"deliverablePrefixes"`
	TransferDir         string       `json:"transferDir"`
	TransferMaxFileSize int64        `json:"transferMaxFileSize"`
	TransferTTL         string       `json:"transferTtl"`
	LogAddSource        bool         `json:"logAddSource"`
	CheckConfig         bool         `json:"checkConfig"`
	ConfigPath          string       `json:"-"`
}

func Validate(cfg Config) error {
	if err := ValidateSecurity(cfg); err != nil {
		return err
	}
	if len(cfg.NATSServers) == 0 {
		return errors.New("nats servers is empty")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.TrustMode))
	if mode != "open" && mode != "tofu" && mode != "explicit" {
		return errors.New("trust mode must be one of: open|tofu|explicit")
	}
	if err := types.ValidateDisplayName(cfg.DisplayName); err != nil {
		return fmt.Errorf("display name: %w", err)
	}
	if _, err := types.NormalizeLabels(cfg.Labels); err != nil {
		return fmt.Errorf("labels: %w", err)
	}
	// An empty role is accepted only by the legacy single-Bot configuration
	// model used by offline migration. New Bot configs validate role explicitly.
	adapterName := strings.ToLower(strings.TrimSpace(cfg.AgentAdapter))
	switch cfg.Role {
	case RoleConsole:
		if cfg.AgentAdapter != "" || cfg.AgentAdapterTimeout != "" || cfg.WebhookURL != "" || len(cfg.AgentAllowedIDs) != 0 {
			return errors.New("console has no execution Harness configuration")
		}
	case "", RoleAgent:
		if cfg.Role == "" && adapterName == "" {
			adapterName = defaultAgentAdapter
		}
		if !supportedAgentAdapter(adapterName) && !(cfg.Role == "" && adapterName == "console") {
			return errors.New("unsupported agent adapter")
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
	default:
		return errors.New("role must be console or agent")
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
	return store.WriteAtomic(path, data, 0o600)
}

type runtimeConfig struct {
	NATSServers []string
	SecurityValues
	LocalAPIAddr        string
	DataDir             string
	IdentityKeyPath     string
	IdentityPubPath     string
	Heartbeat           time.Duration
	AnnounceTTL         time.Duration
	AgentProduct        string
	AgentVersion        string
	DisplayName         string
	Labels              types.Labels
	TrustMode           string
	TrustAutoApprove    bool
	AgentAdapter        string
	AgentAllowedIDs     []string
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
	NATSServers []string
	SecurityValues
	LocalAPIAddr        string
	DataDir             string
	IdentityKeyPath     string
	IdentityPubPath     string
	Heartbeat           time.Duration
	AnnounceTTL         time.Duration
	AgentProduct        string
	AgentVersion        string
	DisplayName         string
	Labels              types.Labels
	TrustMode           string
	TrustAutoApprove    bool
	TrustAutoApproveSet bool
	AgentAdapter        string
	AgentAllowedIDs     []string
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
		SecurityValues:      securityValuesFromConfig(c),
		NATSServers:         c.NATSServers,
		LocalAPIAddr:        c.LocalAPIAddr,
		DataDir:             c.DataDir,
		IdentityKeyPath:     c.IdentityKeyPath,
		IdentityPubPath:     c.IdentityPubPath,
		Heartbeat:           h,
		AnnounceTTL:         t,
		AgentProduct:        c.AgentProduct,
		AgentVersion:        c.AgentVersion,
		DisplayName:         c.DisplayName,
		Labels:              cloneLabels(c.Labels),
		TrustMode:           c.TrustMode,
		TrustAutoApprove:    c.TrustAutoApprove,
		AgentAdapter:        c.AgentAdapter,
		AgentAllowedIDs:     cloneStrings(c.AgentAllowedIDs),
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
	dotenv, err := loadDotEnvValues()
	if err != nil {
		return Config{}, err
	}
	merged = mergeConfigValues(merged, dotenv)
	envValues, err := loadOSEnvValues()
	if err != nil {
		return Config{}, err
	}
	merged = mergeConfigValues(merged, envValues)

	fs := flag.NewFlagSet("clawsynapsed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		apiTokenPath        = fs.String("api-token-path", merged.APITokenPath, "local API bearer token file")
		allowRemoteAPI      = fs.Bool("allow-remote-api", merged.AllowRemoteAPI, "explicitly allow non-loopback HTTPS API")
		apiTLSCertFile      = fs.String("api-tls-cert-file", merged.APITLSCertFile, "API TLS certificate file")
		apiTLSKeyFile       = fs.String("api-tls-key-file", merged.APITLSKeyFile, "API TLS private key file")
		production          = fs.Bool("production", merged.Production, "require authenticated TLS NATS and non-open trust")
		natsCredentialsFile = fs.String("nats-credentials-file", merged.NATSCredentialsFile, "NATS credentials file")
		natsTLSCAFile       = fs.String("nats-tls-ca-file", merged.NATSTLSCAFile, "NATS trusted CA file")
		natsTLSCertFile     = fs.String("nats-tls-cert-file", merged.NATSTLSCertFile, "NATS TLS client certificate")
		natsTLSKeyFile      = fs.String("nats-tls-key-file", merged.NATSTLSKeyFile, "NATS TLS client private key")
		natsTLSServerName   = fs.String("nats-tls-server-name", merged.NATSTLSServerName, "NATS TLS server name to verify")
		natsServers         = fs.String("nats-servers", strings.Join(merged.NATSServers, ","), "comma separated nats servers")
		apiAddr             = fs.String("local-api-addr", merged.LocalAPIAddr, "http api address")
		dataDir             = fs.String("data-dir", merged.DataDir, "state directory")
		identityKeyPath     = fs.String("identity-key-path", merged.IdentityKeyPath, "private key file path")
		identityPubPath     = fs.String("identity-pub-path", merged.IdentityPubPath, "public key file path")
		heartbeat           = fs.Duration("heartbeat", merged.Heartbeat, "announce heartbeat interval")
		announceTTL         = fs.Duration("announce-ttl", merged.AnnounceTTL, "announce ttl")
		agentProduct        = fs.String("agent-product", merged.AgentProduct, "agent product advertised in discovery")
		agentVersion        = fs.String("agent-version", merged.AgentVersion, "agent version advertised in discovery; defaults to daemon version")
		displayName         = fs.String("display-name", merged.DisplayName, "human readable Bot name advertised in discovery")
		labelFlags          = newLabelFlags()
		capabilities        = fs.String("capabilities", strings.Join(merged.Labels.Caps(), ","), "deprecated: comma separated capabilities folded into labels.caps (use --label caps=<value>)")
		trustMode           = fs.String("trust-mode", merged.TrustMode, "trust mode: open|tofu|explicit")
		trustAutoApprove    = fs.Bool("trust-auto-approve", merged.TrustAutoApprove, "automatically approve valid inbound trust requests")
		agentAdapter        = fs.String("agent-adapter", merged.AgentAdapter, "agent adapter: console|default|openclaw|opencode|codex|webhook")
		agentAllowedIDs     = fs.String("agent-allowed-ids", strings.Join(merged.AgentAllowedIDs, ","), "comma separated authorized local agent routing IDs")
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

	fs.Var(labelFlags, "label", "Bot label key=value; repeat a key to make it a set (reserved key: caps)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { flagsSet[f.Name] = true })

	labels := cloneLabels(merged.Labels)
	if flagsSet["label"] {
		labels = labelFlags.Labels()
	}
	if flagsSet["capabilities"] {
		if _, capsSetByLabel := labels[types.LabelKeyCaps]; !(flagsSet["label"] && capsSetByLabel) {
			labels = labels.WithCaps(splitCSV(*capabilities))
		}
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
	if adapterName != "console" && adapterName != "default" && adapterName != "openclaw" && adapterName != "opencode" && adapterName != "codex" && adapterName != "webhook" {
		return Config{}, errors.New("agent adapter must be one of: console|default|openclaw|opencode|codex|webhook")
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

	cfg := Config{
		APITokenPath:        *apiTokenPath,
		AllowRemoteAPI:      *allowRemoteAPI,
		APITLSCertFile:      *apiTLSCertFile,
		APITLSKeyFile:       *apiTLSKeyFile,
		Production:          *production,
		NATSCredentialsFile: *natsCredentialsFile,
		NATSTLSCAFile:       *natsTLSCAFile,
		NATSTLSCertFile:     *natsTLSCertFile,
		NATSTLSKeyFile:      *natsTLSKeyFile,
		NATSTLSServerName:   *natsTLSServerName,
		NATSServers:         rawServers,
		LocalAPIAddr:        strings.TrimSpace(*apiAddr),
		DataDir:             resolvedDataDir,
		IdentityKeyPath:     resolvedKey,
		IdentityPubPath:     resolvedPub,
		HeartbeatInterval:   heartbeat.String(),
		AnnounceTTL:         announceTTL.String(),
		AgentProduct:        strings.TrimSpace(*agentProduct),
		AgentVersion:        strings.TrimSpace(*agentVersion),
		DisplayName:         strings.TrimSpace(*displayName),
		Labels:              labels,
		TrustMode:           mode,
		TrustAutoApprove:    *trustAutoApprove,
		AgentAdapter:        adapterName,
		AgentAllowedIDs:     splitCSV(*agentAllowedIDs),
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
	}
	if err := resolveSecurityPaths(&cfg); err != nil {
		return Config{}, err
	}
	if err := ValidateSecurity(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
		Labels:              types.Labels{}.WithCaps([]string{"chat", "tools"}),
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
	base.SecurityValues = mergeSecurityValues(base.SecurityValues, override.SecurityValues)
	if len(override.AgentAllowedIDs) > 0 {
		base.AgentAllowedIDs = append([]string(nil), override.AgentAllowedIDs...)
	}
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
	if strings.TrimSpace(override.DisplayName) != "" {
		base.DisplayName = strings.TrimSpace(override.DisplayName)
	}
	if len(override.Labels) > 0 {
		base.Labels = cloneLabels(override.Labels)
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

// labelFlags collects repeated --label key=value occurrences. A key given
// once stays scalar; a repeated key becomes a set of all given values.
type labelFlags struct {
	order  []string
	values map[string][]string
}

func newLabelFlags() *labelFlags {
	return &labelFlags{values: make(map[string][]string)}
}

func (f *labelFlags) String() string { return "" }

func (f *labelFlags) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return fmt.Errorf("label %q must be key=value", raw)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("label %q has empty key", raw)
	}
	if _, seen := f.values[key]; !seen {
		f.order = append(f.order, key)
	}
	f.values[key] = append(f.values[key], strings.TrimSpace(value))
	return nil
}

func (f *labelFlags) Labels() types.Labels {
	if len(f.values) == 0 {
		return nil
	}
	out := make(types.Labels, len(f.values))
	for _, key := range f.order {
		vals := f.values[key]
		if len(vals) == 1 && key != types.LabelKeyCaps {
			out[key] = types.LabelString(vals[0])
			continue
		}
		out[key] = types.LabelSet(vals)
	}
	return out
}

func cloneLabels(in types.Labels) types.Labels {
	if len(in) == 0 {
		return nil
	}
	out := make(types.Labels, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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
