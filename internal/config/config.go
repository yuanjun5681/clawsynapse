package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultNATSServers       = "nats://127.0.0.1:4222"
	defaultLocalAPIAddr      = "127.0.0.1:18080"
	defaultHeartbeatInterval = 15 * time.Second
	defaultAnnounceTTL       = 30 * time.Second
	defaultTrustMode         = "tofu"
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
	CheckConfig     bool
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
		CheckConfig:     c.CheckConfig,
	}
}

func LoadFromOS(args []string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, err
	}

	defaultDataDir := filepath.Join(home, ".clawsynapse")

	fs := flag.NewFlagSet("clawsynapsed", flag.ContinueOnError)
	var (
		nodeID          = fs.String("node-id", envOr("NODE_ID", ""), "node id")
		natsServers     = fs.String("nats-servers", envOr("NATS_SERVERS", defaultNATSServers), "comma separated nats servers")
		apiAddr         = fs.String("local-api-addr", envOr("LOCAL_API_ADDR", defaultLocalAPIAddr), "http api address")
		dataDir         = fs.String("data-dir", envOr("DATA_DIR", defaultDataDir), "state directory")
		identityKeyPath = fs.String("identity-key-path", envOr("IDENTITY_KEY_PATH", filepath.Join(defaultDataDir, "identity.key")), "private key file path")
		identityPubPath = fs.String("identity-pub-path", envOr("IDENTITY_PUB_PATH", filepath.Join(defaultDataDir, "identity.pub")), "public key file path")
		heartbeat       = fs.Duration("heartbeat", envDuration("HEARTBEAT_INTERVAL_MS", defaultHeartbeatInterval), "announce heartbeat interval")
		announceTTL     = fs.Duration("announce-ttl", envDuration("ANNOUNCE_TTL_MS", defaultAnnounceTTL), "announce ttl")
		trustMode       = fs.String("trust-mode", envOr("TRUST_MODE", defaultTrustMode), "trust mode: open|tofu|explicit")
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
		CheckConfig:       *checkConfig,
	}, nil
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
