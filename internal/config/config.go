package config

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultNATSServers       = "nats://220.168.146.21:9414"
	defaultLocalAPIAddr      = "127.0.0.1:18080"
	defaultHeartbeatInterval = 15 * time.Second
	defaultAnnounceTTL       = 30 * time.Second
	defaultTrustMode         = "tofu"
	defaultAgentAdapter      = "default"
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
)

type Config struct {
	NodeID            string   `json:"nodeId"`
	NATSServers       []string `json:"natsServers"`
	LocalAPIAddr      string   `json:"localApiAddr"`
	DataDir           string   `json:"dataDir"`
	IdentityKeyPath   string   `json:"identityKeyPath"`
	IdentityPubPath   string   `json:"identityPubPath"`
	HeartbeatInterval string   `json:"heartbeatInterval"`
	AnnounceTTL       string   `json:"announceTtl"`
	TrustMode         string   `json:"trustMode"`
	AgentAdapter      string   `json:"agentAdapter"`
	LogLevel          string   `json:"logLevel"`
	LogFormat         string   `json:"logFormat"`
	LogAddSource      bool     `json:"logAddSource"`
	CheckConfig       bool     `json:"checkConfig"`
}

type runtimeConfig struct {
	NodeID          string
	NATSServers     []string
	LocalAPIAddr    string
	DataDir         string
	IdentityKeyPath string
	IdentityPubPath string
	Heartbeat       time.Duration
	AnnounceTTL     time.Duration
	TrustMode       string
	AgentAdapter    string
	LogLevel        string
	LogFormat       string
	LogAddSource    bool
	CheckConfig     bool
}

type configValues struct {
	NodeID          string
	NATSServers     []string
	LocalAPIAddr    string
	DataDir         string
	IdentityKeyPath string
	IdentityPubPath string
	Heartbeat       time.Duration
	AnnounceTTL     time.Duration
	TrustMode       string
	AgentAdapter    string
	LogLevel        string
	LogFormat       string
	LogAddSource    bool
}

func (c Config) Runtime() runtimeConfig {
	h, _ := time.ParseDuration(c.HeartbeatInterval)
	t, _ := time.ParseDuration(c.AnnounceTTL)
	return runtimeConfig{
		NodeID:          c.NodeID,
		NATSServers:     c.NATSServers,
		LocalAPIAddr:    c.LocalAPIAddr,
		DataDir:         c.DataDir,
		IdentityKeyPath: c.IdentityKeyPath,
		IdentityPubPath: c.IdentityPubPath,
		Heartbeat:       h,
		AnnounceTTL:     t,
		TrustMode:       c.TrustMode,
		AgentAdapter:    c.AgentAdapter,
		LogLevel:        c.LogLevel,
		LogFormat:       c.LogFormat,
		LogAddSource:    c.LogAddSource,
		CheckConfig:     c.CheckConfig,
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
		nodeID          = fs.String("node-id", merged.NodeID, "node id")
		natsServers     = fs.String("nats-servers", strings.Join(merged.NATSServers, ","), "comma separated nats servers")
		apiAddr         = fs.String("local-api-addr", merged.LocalAPIAddr, "http api address")
		dataDir         = fs.String("data-dir", merged.DataDir, "state directory")
		identityKeyPath = fs.String("identity-key-path", merged.IdentityKeyPath, "private key file path")
		identityPubPath = fs.String("identity-pub-path", merged.IdentityPubPath, "public key file path")
		heartbeat       = fs.Duration("heartbeat", merged.Heartbeat, "announce heartbeat interval")
		announceTTL     = fs.Duration("announce-ttl", merged.AnnounceTTL, "announce ttl")
		trustMode       = fs.String("trust-mode", merged.TrustMode, "trust mode: open|tofu|explicit")
		agentAdapter    = fs.String("agent-adapter", merged.AgentAdapter, "agent adapter: default|openclaw")
		logLevel        = fs.String("log-level", merged.LogLevel, "log level: debug|info|warn|error")
		logFormat       = fs.String("log-format", merged.LogFormat, "log format: json|text")
		logAddSource    = fs.Bool("log-add-source", merged.LogAddSource, "include source location in logs")
		_               = fs.String("config", configPath, "config file path")
		checkConfig     = fs.Bool("check-config", false, "print config and exit")
	)

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	rawServers := splitCSV(*natsServers)
	if len(rawServers) == 0 {
		return Config{}, errors.New("nats servers is empty")
	}

	if strings.TrimSpace(*nodeID) == "" && !*checkConfig {
		return Config{}, errors.New("node id is required (set --node-id or NODE_ID)")
	}

	mode := strings.ToLower(strings.TrimSpace(*trustMode))
	if mode != "open" && mode != "tofu" && mode != "explicit" {
		return Config{}, errors.New("trust mode must be one of: open|tofu|explicit")
	}
	adapterName := strings.ToLower(strings.TrimSpace(*agentAdapter))
	if adapterName == "" {
		adapterName = defaultAgentAdapter
	}
	if adapterName != "default" && adapterName != "openclaw" {
		return Config{}, errors.New("agent adapter must be one of: default|openclaw")
	}
	level := strings.ToLower(strings.TrimSpace(*logLevel))
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return Config{}, errors.New("log level must be one of: debug|info|warn|error")
	}
	format := strings.ToLower(strings.TrimSpace(*logFormat))
	if format != "json" && format != "text" {
		return Config{}, errors.New("log format must be one of: json|text")
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

	return Config{
		NodeID:            strings.TrimSpace(*nodeID),
		NATSServers:       rawServers,
		LocalAPIAddr:      strings.TrimSpace(*apiAddr),
		DataDir:           resolvedDataDir,
		IdentityKeyPath:   resolvedKey,
		IdentityPubPath:   resolvedPub,
		HeartbeatInterval: heartbeat.String(),
		AnnounceTTL:       announceTTL.String(),
		TrustMode:         mode,
		AgentAdapter:      adapterName,
		LogLevel:          level,
		LogFormat:         format,
		LogAddSource:      *logAddSource,
		CheckConfig:       *checkConfig,
	}, nil
}

func defaultConfigValues(defaultDataDir string) configValues {
	return configValues{
		NATSServers:     []string{defaultNATSServers},
		LocalAPIAddr:    defaultLocalAPIAddr,
		DataDir:         defaultDataDir,
		IdentityKeyPath: filepath.Join(defaultDataDir, "identity.key"),
		IdentityPubPath: filepath.Join(defaultDataDir, "identity.pub"),
		Heartbeat:       defaultHeartbeatInterval,
		AnnounceTTL:     defaultAnnounceTTL,
		TrustMode:       defaultTrustMode,
		AgentAdapter:    defaultAgentAdapter,
		LogLevel:        defaultLogLevel,
		LogFormat:       defaultLogFormat,
	}
}

func mergeConfigValues(base, override configValues) configValues {
	if strings.TrimSpace(override.NodeID) != "" {
		base.NodeID = strings.TrimSpace(override.NodeID)
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
	if strings.TrimSpace(override.TrustMode) != "" {
		base.TrustMode = strings.TrimSpace(override.TrustMode)
	}
	if strings.TrimSpace(override.AgentAdapter) != "" {
		base.AgentAdapter = strings.TrimSpace(override.AgentAdapter)
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
