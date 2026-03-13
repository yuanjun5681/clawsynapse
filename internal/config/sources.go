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
	NodeID            string   `yaml:"nodeId"`
	NATSServers       []string `yaml:"natsServers"`
	LocalAPIAddr      string   `yaml:"localApiAddr"`
	DataDir           string   `yaml:"dataDir"`
	IdentityKeyPath   string   `yaml:"identityKeyPath"`
	IdentityPubPath   string   `yaml:"identityPubPath"`
	HeartbeatInterval string   `yaml:"heartbeatInterval"`
	AnnounceTTL       string   `yaml:"announceTtl"`
	TrustMode         string   `yaml:"trustMode"`
	AgentAdapter      string   `yaml:"agentAdapter"`
	OpenClawAgentID   string   `yaml:"openclawAgentId"`
	LogLevel          string   `yaml:"logLevel"`
	LogFormat         string   `yaml:"logFormat"`
	LogAddSource      *bool    `yaml:"logAddSource"`
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
		NodeID:          strings.TrimSpace(cfg.NodeID),
		NATSServers:     cloneStrings(cfg.NATSServers),
		LocalAPIAddr:    strings.TrimSpace(cfg.LocalAPIAddr),
		DataDir:         strings.TrimSpace(cfg.DataDir),
		IdentityKeyPath: strings.TrimSpace(cfg.IdentityKeyPath),
		IdentityPubPath: strings.TrimSpace(cfg.IdentityPubPath),
		TrustMode:       strings.TrimSpace(cfg.TrustMode),
		AgentAdapter:    strings.TrimSpace(cfg.AgentAdapter),
		OpenClawAgentID: strings.TrimSpace(cfg.OpenClawAgentID),
		LogLevel:        strings.TrimSpace(cfg.LogLevel),
		LogFormat:       strings.TrimSpace(cfg.LogFormat),
	}
	if cfg.LogAddSource != nil {
		values.LogAddSource = *cfg.LogAddSource
	}

	if cfg.HeartbeatInterval != "" {
		values.Heartbeat = parseDurationValue(cfg.HeartbeatInterval, 0)
	}
	if cfg.AnnounceTTL != "" {
		values.AnnounceTTL = parseDurationValue(cfg.AnnounceTTL, 0)
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
	return configValues{
		NodeID:          strings.TrimSpace(values["NODE_ID"]),
		NATSServers:     splitCSV(values["NATS_SERVERS"]),
		LocalAPIAddr:    strings.TrimSpace(values["LOCAL_API_ADDR"]),
		DataDir:         strings.TrimSpace(values["DATA_DIR"]),
		IdentityKeyPath: strings.TrimSpace(values["IDENTITY_KEY_PATH"]),
		IdentityPubPath: strings.TrimSpace(values["IDENTITY_PUB_PATH"]),
		Heartbeat:       parseDurationValue(values["HEARTBEAT_INTERVAL_MS"], 0),
		AnnounceTTL:     parseDurationValue(values["ANNOUNCE_TTL_MS"], 0),
		TrustMode:       strings.TrimSpace(values["TRUST_MODE"]),
		AgentAdapter:    strings.TrimSpace(values["AGENT_ADAPTER"]),
		OpenClawAgentID: strings.TrimSpace(values["OPENCLAW_AGENT_ID"]),
		LogLevel:        strings.TrimSpace(values["LOG_LEVEL"]),
		LogFormat:       strings.TrimSpace(values["LOG_FORMAT"]),
		LogAddSource:    parseBoolValue(values["LOG_ADD_SOURCE"]),
	}
}

func parseDurationValue(v string, fallback time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}

	if strings.HasSuffix(v, "ms") || strings.HasSuffix(v, "s") || strings.HasSuffix(v, "m") {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fallback
		}
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
