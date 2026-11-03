package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

const (
	DaemonSchemaVersion = 3
	RoleConsole         = "console"
	RoleAgent           = "agent"
)

type DaemonConfig struct {
	SchemaVersion       int      `json:"schemaVersion"`
	DataDir             string   `json:"dataDir"`
	LocalAPIAddr        string   `json:"localApiAddr"`
	APITLSCertFile      string   `json:"apiTLSCertFile,omitempty"`
	APITLSKeyFile       string   `json:"apiTLSKeyFile,omitempty"`
	NATSServers         []string `json:"natsServers"`
	NATSCredentialsFile string   `json:"natsCredentialsFile,omitempty"`
	NATSTLSCAFile       string   `json:"natsTLSCAFile,omitempty"`
	NATSTLSCertFile     string   `json:"natsTLSCertFile,omitempty"`
	NATSTLSKeyFile      string   `json:"natsTLSKeyFile,omitempty"`
	NATSTLSServerName   string   `json:"natsTLSServerName,omitempty"`
	Production          bool     `json:"production"`
	LogLevel            string   `json:"logLevel"`
	LogFormat           string   `json:"logFormat"`
	LogFilePath         string   `json:"logFilePath,omitempty"`
	LogAddSource        bool     `json:"logAddSource"`
	LogRotateMaxSizeMB  int      `json:"logRotateMaxSizeMb"`
	LogRotateMaxBackups int      `json:"logRotateMaxBackups"`
	LogRotateMaxAgeDays int      `json:"logRotateMaxAgeDays"`
	LogRotateCompress   bool     `json:"logRotateCompress"`
	MaxConcurrent       int      `json:"maxConcurrent"`
	BlobMaxBytes        int64    `json:"blobMaxBytes"`
	TempMaxBytes        int64    `json:"tempMaxBytes"`
	WorkspaceMaxBytes   int64    `json:"workspaceMaxBytes"`
	CheckConfig         bool     `json:"-"`
	ConfigPath          string   `json:"-"`
}

type HarnessBinding struct {
	Adapter         string   `json:"adapter"`
	Model           string   `json:"model,omitempty"`
	Timeout         string   `json:"timeout"`
	WebhookURL      string   `json:"webhookUrl,omitempty"`
	CredentialsFile string   `json:"credentialsFile,omitempty"`
	Instance        string   `json:"instance,omitempty"`
	AllowedAgentIDs []string `json:"allowedAgentIds,omitempty"`
}

type BotConfig struct {
	Revision            uint64          `json:"revision"`
	BindingRevision     string          `json:"bindingRevision"`
	DisplayName         string          `json:"displayName"`
	Role                string          `json:"role"`
	Labels              types.Labels    `json:"labels,omitempty"`
	TrustMode           string          `json:"trustMode"`
	TrustAutoApprove    bool            `json:"trustAutoApprove"`
	HeartbeatInterval   string          `json:"heartbeatInterval"`
	AnnounceTTL         string          `json:"announceTtl"`
	AgentProduct        string          `json:"agentProduct"`
	AgentVersion        string          `json:"agentVersion,omitempty"`
	Harness             *HarnessBinding `json:"harness,omitempty"`
	NATSCredentialsFile string          `json:"natsCredentialsFile,omitempty"`
	NATSTLSCAFile       string          `json:"natsTLSCAFile,omitempty"`
	NATSTLSCertFile     string          `json:"natsTLSCertFile,omitempty"`
	NATSTLSKeyFile      string          `json:"natsTLSKeyFile,omitempty"`
	NATSTLSServerName   string          `json:"natsTLSServerName,omitempty"`
	DeliverablePrefixes []string        `json:"deliverablePrefixes"`
	TransferMaxFileSize int64           `json:"transferMaxFileSize"`
	TransferTTL         string          `json:"transferTtl"`
	MaxConcurrent       int             `json:"maxConcurrent,omitempty"`
	MaxQueued           int             `json:"maxQueued,omitempty"`
	FileQuotaBytes      int64           `json:"fileQuotaBytes"`
}

// MarshalJSON omits execution fields for Console while preserving an Agent's
// explicit zero queue budget across persistence and full-replacement updates.
func (b BotConfig) MarshalJSON() ([]byte, error) {
	type botConfig BotConfig
	if b.Role != RoleAgent {
		return json.Marshal(botConfig(b))
	}
	return json.Marshal(struct {
		botConfig
		MaxConcurrent int `json:"maxConcurrent"`
		MaxQueued     int `json:"maxQueued"`
	}{botConfig: botConfig(b), MaxConcurrent: b.MaxConcurrent, MaxQueued: b.MaxQueued})
}

func DefaultDaemonConfig() DaemonConfig {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".clawsynapse")
	return DaemonConfig{SchemaVersion: DaemonSchemaVersion, DataDir: root, LocalAPIAddr: defaultLocalAPIAddr, NATSServers: []string{defaultNATSServers}, LogLevel: defaultLogLevel, LogFormat: defaultLogFormat, LogRotateMaxSizeMB: defaultLogRotateMaxSizeMB, LogRotateMaxBackups: defaultLogRotateMaxBackups, LogRotateMaxAgeDays: defaultLogRotateMaxAgeDays, MaxConcurrent: 8, BlobMaxBytes: 10 << 30, TempMaxBytes: 1 << 30, WorkspaceMaxBytes: 10 << 30}
}

// DefaultBotConfig returns usable Agent defaults for in-process callers.
// JSON inputs must still explicitly declare their role and Harness adapter.
func DefaultBotConfig() BotConfig {
	return DefaultBotConfigForRole(RoleAgent)
}

// DefaultBotConfigForRole leaves unsupported roles invalid for ValidateBot.
func DefaultBotConfigForRole(role string) BotConfig {
	b := BotConfig{Revision: 1, Role: role, TrustMode: defaultTrustMode, HeartbeatInterval: defaultHeartbeatInterval.String(), AnnounceTTL: defaultAnnounceTTL.String(), AgentProduct: defaultAgentProduct, Labels: types.Labels{}.WithCaps([]string{"chat", "tools"}), DeliverablePrefixes: splitCSV(defaultDeliverablePrefixes), TransferMaxFileSize: defaultTransferMaxFileSize, TransferTTL: defaultTransferTTL, FileQuotaBytes: 1 << 30}
	if role == RoleAgent {
		b.Harness = &HarnessBinding{Adapter: defaultAgentAdapter, Timeout: defaultAgentAdapterTimeout.String()}
		b.MaxConcurrent = 2
		b.MaxQueued = 64
	} else if role == RoleConsole {
		b.Labels = types.Labels{}.WithCaps([]string{"chat"})
	}
	return b
}

// UnmarshalJSON keeps nested management requests and ordinary JSON clients on
// the same presence-aware contract as persisted configuration.
func (b *BotConfig) UnmarshalJSON(data []byte) error {
	return DecodeStrict(data, b)
}

// DecodeStrict is shared by on-disk configuration and local management.
// Bot configuration is a complete replacement: validate field applicability
// before choosing defaults, and never inherit values from the destination.
func DecodeStrict(data []byte, v any) error {
	b, ok := v.(*BotConfig)
	if !ok {
		return protocol.DecodeStrict(data, v)
	}
	if b == nil {
		return errors.New("Bot config destination is nil")
	}
	var fields map[string]json.RawMessage
	if err := protocol.DecodeStrict(data, &fields); err != nil {
		return err
	}
	for name := range fields {
		for _, canonical := range []string{"role", "harness", "maxConcurrent", "maxQueued"} {
			if name != canonical && strings.EqualFold(name, canonical) {
				return fmt.Errorf("use canonical field name %s", canonical)
			}
		}
	}
	var role string
	if err := json.Unmarshal(fields["role"], &role); err != nil || (role != RoleConsole && role != RoleAgent) {
		return errors.New("role must explicitly be console or agent")
	}
	// Scalar nulls must not leave default values intact. Optional collections
	// may be null and are validated by their normal field constraints.
	for name, raw := range fields {
		if string(raw) == "null" && name != "labels" && name != "deliverablePrefixes" {
			return fmt.Errorf("%s must not be null", name)
		}
	}
	if role == RoleConsole {
		for _, name := range []string{"harness", "maxConcurrent", "maxQueued"} {
			if _, present := fields[name]; present {
				return fmt.Errorf("%s is not applicable to console", name)
			}
		}
	} else {
		var harness map[string]json.RawMessage
		if err := json.Unmarshal(fields["harness"], &harness); err != nil || len(harness) == 0 {
			return errors.New("agent requires a non-empty harness object")
		}
		var adapter string
		if err := json.Unmarshal(harness["adapter"], &adapter); err != nil || !supportedAgentAdapter(adapter) {
			return errors.New("agent requires an explicit supported harness.adapter")
		}
		for name, raw := range harness {
			if name != "adapter" && strings.EqualFold(name, "adapter") {
				return errors.New("use canonical field name harness.adapter")
			}
			if string(raw) == "null" && name != "allowedAgentIds" {
				return fmt.Errorf("harness.%s must not be null", name)
			}
		}
	}
	type botConfig BotConfig
	decoded := botConfig(DefaultBotConfigForRole(role))
	if err := protocol.DecodeStrict(data, &decoded); err != nil {
		return err
	}
	*b = BotConfig(decoded)
	return nil
}

func supportedAgentAdapter(adapter string) bool {
	switch adapter {
	case "default", "openclaw", "opencode", "codex", "pi", "omp", "webhook":
		return true
	default:
		return false
	}
}

// DecodePersistedDaemon imports daemon JSON written before local management
// stopped using administrator tokens and remote-listener opt-ins. Only those
// obsolete fields are discarded; strict decoding still rejects other mistakes.
// Loading does not rewrite files or alter the configured listener address.
func DecodePersistedDaemon(data []byte, d *DaemonConfig) error {
	var fields map[string]json.RawMessage
	if err := protocol.DecodeStrict(data, &fields); err != nil {
		return err
	}
	_, hadToken := fields["adminTokenPath"]
	_, hadRemote := fields["allowRemoteAPI"]
	if !hadToken && !hadRemote {
		return protocol.DecodeStrict(data, d)
	}
	delete(fields, "adminTokenPath")
	delete(fields, "allowRemoteAPI")
	cleaned, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return protocol.DecodeStrict(cleaned, d)
}

func ValidateDaemon(d DaemonConfig) error {
	if d.SchemaVersion != DaemonSchemaVersion {
		return errors.New("unsupported daemon schemaVersion; explicit legacy migration is required")
	}
	if !filepath.IsAbs(d.DataDir) || filepath.Clean(d.DataDir) == string(filepath.Separator) {
		return errors.New("dataDir must be an absolute non-root directory")
	}
	if err := store.CheckPath(d.DataDir); err != nil {
		return err
	}
	if err := ValidateAPIListener(d.LocalAPIAddr, d.APITLSCertFile, d.APITLSKeyFile); err != nil {
		return err
	}
	if len(d.NATSServers) == 0 {
		return errors.New("natsServers is empty")
	}
	for _, server := range d.NATSServers {
		u, err := url.Parse(server)
		if err != nil || u.Host == "" || (u.Scheme != "nats" && u.Scheme != "tls" && u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil {
			return errors.New("NATS URLs must use nats, tls, ws or wss with no embedded credentials")
		}
	}
	if d.MaxConcurrent <= 0 || d.BlobMaxBytes <= 0 || d.TempMaxBytes <= 0 || d.WorkspaceMaxBytes <= 0 {
		return errors.New("daemon concurrency and storage budgets must be positive")
	}
	if d.LogLevel != "debug" && d.LogLevel != "info" && d.LogLevel != "warn" && d.LogLevel != "error" {
		return errors.New("invalid logLevel")
	}
	if d.LogFormat != "json" && d.LogFormat != "text" {
		return errors.New("invalid logFormat")
	}
	if d.LogRotateMaxSizeMB <= 0 || d.LogRotateMaxBackups < 0 || d.LogRotateMaxAgeDays < 0 {
		return errors.New("invalid log rotation limits")
	}
	c := RuntimeConfig(d, DefaultBotConfig(), "validation")
	if err := ValidateSecurity(c); err != nil {
		return err
	}
	for _, p := range []string{d.APITLSCertFile, d.APITLSKeyFile, d.NATSCredentialsFile, d.NATSTLSCAFile, d.NATSTLSCertFile, d.NATSTLSKeyFile, d.LogFilePath} {
		if p != "" {
			if !filepath.IsAbs(p) {
				return errors.New("daemon file paths must be absolute")
			}
			if err := store.CheckPath(p); err != nil {
				return err
			}
		}
	}
	for _, p := range []string{d.APITLSKeyFile, d.NATSCredentialsFile, d.NATSTLSKeyFile} {
		if err := validatePrivateFile(p); err != nil {
			return err
		}
	}
	return nil
}

var bindingName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func ValidateBot(b BotConfig) error {
	if b.Revision == 0 {
		return errors.New("bot revision must be positive")
	}
	if b.BindingRevision != "" && !bindingName.MatchString(b.BindingRevision) {
		return errors.New("invalid bindingRevision")
	}
	if b.TrustMode != "open" && b.TrustMode != "tofu" && b.TrustMode != "explicit" {
		return errors.New("trustMode must be open, tofu or explicit")
	}
	switch b.Role {
	case RoleConsole:
		if b.Harness != nil || b.MaxConcurrent != 0 || b.MaxQueued != 0 {
			return errors.New("console must omit harness, maxConcurrent and maxQueued")
		}
	case RoleAgent:
		if b.Harness == nil || !supportedAgentAdapter(b.Harness.Adapter) {
			return errors.New("agent requires a supported Harness adapter")
		}
		if (b.Harness.Adapter == "default" || b.Harness.Adapter == "openclaw") && b.Harness.Model != "" {
			return errors.New("OpenClaw does not support a per-call model override")
		}
		if b.MaxConcurrent <= 0 || b.MaxQueued < 0 {
			return errors.New("invalid Bot execution limits")
		}
		if duration, err := time.ParseDuration(b.Harness.Timeout); err != nil || duration <= 0 {
			return errors.New("harness.timeout must be a positive duration")
		}
	default:
		return errors.New("role must be console or agent")
	}
	if b.FileQuotaBytes <= 0 || b.TransferMaxFileSize <= 0 {
		return errors.New("invalid Bot storage limits")
	}
	if b.TransferMaxFileSize > b.FileQuotaBytes {
		return errors.New("transferMaxFileSize exceeds fileQuotaBytes")
	}
	for name, raw := range map[string]string{"heartbeatInterval": b.HeartbeatInterval, "announceTtl": b.AnnounceTTL, "transferTtl": b.TransferTTL} {
		dur, err := time.ParseDuration(raw)
		if err != nil || dur <= 0 {
			return fmt.Errorf("%s must be a positive duration", name)
		}
	}
	heartbeat, _ := time.ParseDuration(b.HeartbeatInterval)
	ttl, _ := time.ParseDuration(b.AnnounceTTL)
	if ttl < heartbeat {
		return errors.New("announceTtl must not be shorter than heartbeatInterval")
	}
	if len(b.DeliverablePrefixes) == 0 {
		return errors.New("deliverablePrefixes is empty")
	}
	for _, p := range b.DeliverablePrefixes {
		if strings.TrimSpace(p) == "" {
			return errors.New("deliverable prefix is empty")
		}
	}
	if b.Harness != nil && b.Harness.Adapter == "webhook" {
		u, err := url.Parse(b.Harness.WebhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return errors.New("webhookUrl must be an HTTP(S) URL without credentials")
		}
	}
	credentialsFile := ""
	if b.Harness != nil {
		credentialsFile = b.Harness.CredentialsFile
	}
	for _, p := range []string{credentialsFile, b.NATSCredentialsFile, b.NATSTLSCAFile, b.NATSTLSCertFile, b.NATSTLSKeyFile} {
		if p != "" {
			if !filepath.IsAbs(p) {
				return errors.New("Bot credential paths must be absolute")
			}
			if err := store.CheckPath(p); err != nil {
				return err
			}
		}
	}
	for _, p := range []string{credentialsFile, b.NATSCredentialsFile, b.NATSTLSKeyFile} {
		if err := validatePrivateFile(p); err != nil {
			return err
		}
	}
	return Validate(RuntimeConfig(DefaultDaemonConfig(), b, "validation"))
}

func validatePrivateFile(path string) error {
	if path == "" {
		return nil
	}
	if err := store.CheckPath(path); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	// Publication validates the staged file; runtime reads enforce existence.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("credential must be a private regular file (0600): %s", path)
	}
	return nil
}

// RuntimeConfig is only an internal assembly mapping; no paths are accepted from a Bot ID.
func RuntimeConfig(d DaemonConfig, b BotConfig, id string) Config {
	root := filepath.Join(d.DataDir, "bots", id)
	prefer := func(own, def string) string {
		if own != "" {
			return own
		}
		return def
	}
	var h HarnessBinding
	if b.Harness != nil {
		h = *b.Harness
	}
	return Config{Role: b.Role, APITokenPath: filepath.Join(root, "credentials", "api.token"), APITLSCertFile: d.APITLSCertFile, APITLSKeyFile: d.APITLSKeyFile, Production: d.Production, NATSCredentialsFile: prefer(b.NATSCredentialsFile, d.NATSCredentialsFile), NATSTLSCAFile: prefer(b.NATSTLSCAFile, d.NATSTLSCAFile), NATSTLSCertFile: prefer(b.NATSTLSCertFile, d.NATSTLSCertFile), NATSTLSKeyFile: prefer(b.NATSTLSKeyFile, d.NATSTLSKeyFile), NATSTLSServerName: prefer(b.NATSTLSServerName, d.NATSTLSServerName), NATSServers: cloneStrings(d.NATSServers), LocalAPIAddr: d.LocalAPIAddr, DataDir: root, IdentityKeyPath: filepath.Join(root, "credentials", "identity.key"), IdentityPubPath: filepath.Join(root, "credentials", "identity.pub"), HeartbeatInterval: b.HeartbeatInterval, AnnounceTTL: b.AnnounceTTL, AgentProduct: b.AgentProduct, AgentVersion: b.AgentVersion, DisplayName: b.DisplayName, Labels: cloneLabels(b.Labels), TrustMode: b.TrustMode, TrustAutoApprove: b.TrustAutoApprove, AgentAdapter: h.Adapter, AgentAllowedIDs: cloneStrings(h.AllowedAgentIDs), AgentAdapterTimeout: h.Timeout, WebhookURL: h.WebhookURL, LogLevel: d.LogLevel, LogFormat: d.LogFormat, LogFilePath: d.LogFilePath, LogRotateMaxSizeMB: d.LogRotateMaxSizeMB, LogRotateMaxBackups: d.LogRotateMaxBackups, LogRotateMaxAgeDays: d.LogRotateMaxAgeDays, LogRotateCompress: d.LogRotateCompress, LogAddSource: d.LogAddSource, DeliverablePrefixes: cloneStrings(b.DeliverablePrefixes), TransferDir: filepath.Join(root, "transfers"), TransferMaxFileSize: b.TransferMaxFileSize, TransferTTL: b.TransferTTL}
}

// LoadDaemon never reads a legacy config or Bot environment settings.
func LoadDaemon(args []string) (DaemonConfig, error) {
	d := DefaultDaemonConfig()
	configPath := ""
	explicit := false
	dataExplicit := false
	// Resolve source before parsing overrides, allowing --data-dir to select config.json.
	for i := range args {
		a := args[i]
		for _, name := range []string{"config", "data-dir"} {
			prefix := "--" + name
			value := ""
			matched := false
			if a == prefix {
				if i+1 >= len(args) {
					return d, fmt.Errorf("%s requires a value", prefix)
				}
				value = args[i+1]
				matched = true
			} else if strings.HasPrefix(a, prefix+"=") {
				value = strings.TrimPrefix(a, prefix+"=")
				matched = true
			}
			if matched {
				if name == "config" {
					configPath = value
					explicit = true
				} else {
					d.DataDir = value
					dataExplicit = true
				}
				break
			}
		}
	}
	if v := os.Getenv("CLAWSYNAPSE_DATA_DIR"); v != "" && !dataExplicit {
		d.DataDir = v
	}
	var err error
	d.DataDir, err = expandPath(d.DataDir)
	if err != nil {
		return d, err
	}
	d.DataDir, err = filepath.Abs(d.DataDir)
	if err != nil {
		return d, err
	}
	if configPath == "" {
		configPath = filepath.Join(d.DataDir, "config.json")
	}
	configPath, err = expandPath(configPath)
	if err != nil {
		return d, err
	}
	data, err := store.ReadFile(configPath)
	if err == nil {
		var header struct {
			SchemaVersion int `json:"schemaVersion"`
		}
		if json.Unmarshal(data, &header) != nil || header.SchemaVersion != DaemonSchemaVersion {
			return d, errors.New("legacy or incompatible config: run explicit migration")
		}
		if err := DecodePersistedDaemon(data, &d); err != nil {
			return d, fmt.Errorf("daemon config: %w", err)
		}
	} else if explicit || !errors.Is(err, os.ErrNotExist) {
		return d, err
	}
	// Only explicitly daemon-scoped environment variables are read.
	stringsEnv := map[string]*string{"DATA_DIR": &d.DataDir, "LOCAL_API_ADDR": &d.LocalAPIAddr, "API_TLS_CERT_FILE": &d.APITLSCertFile, "API_TLS_KEY_FILE": &d.APITLSKeyFile, "NATS_CREDENTIALS_FILE": &d.NATSCredentialsFile, "NATS_TLS_CA_FILE": &d.NATSTLSCAFile, "NATS_TLS_CERT_FILE": &d.NATSTLSCertFile, "NATS_TLS_KEY_FILE": &d.NATSTLSKeyFile, "NATS_TLS_SERVER_NAME": &d.NATSTLSServerName, "LOG_LEVEL": &d.LogLevel, "LOG_FORMAT": &d.LogFormat, "LOG_FILE_PATH": &d.LogFilePath}
	for name, dst := range stringsEnv {
		if v, ok := os.LookupEnv("CLAWSYNAPSE_" + name); ok {
			*dst = v
		}
	}
	if v, ok := os.LookupEnv("CLAWSYNAPSE_NATS_SERVERS"); ok {
		d.NATSServers = splitCSV(v)
	}
	for name, dst := range map[string]*bool{"PRODUCTION": &d.Production} {
		if v, ok := os.LookupEnv("CLAWSYNAPSE_" + name); ok {
			parsed, e := strconv.ParseBool(v)
			if e != nil {
				return d, e
			}
			*dst = parsed
		}
	}
	fs := flag.NewFlagSet("clawsynapsed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&configPath, "config", configPath, "v3 daemon JSON config")
	for name, dst := range map[string]*string{"data-dir": &d.DataDir, "local-api-addr": &d.LocalAPIAddr, "api-tls-cert-file": &d.APITLSCertFile, "api-tls-key-file": &d.APITLSKeyFile, "nats-credentials-file": &d.NATSCredentialsFile, "nats-tls-ca-file": &d.NATSTLSCAFile, "nats-tls-cert-file": &d.NATSTLSCertFile, "nats-tls-key-file": &d.NATSTLSKeyFile, "nats-tls-server-name": &d.NATSTLSServerName, "log-level": &d.LogLevel, "log-format": &d.LogFormat, "log-file-path": &d.LogFilePath} {
		fs.StringVar(dst, name, *dst, name)
	}
	servers := strings.Join(d.NATSServers, ",")
	fs.StringVar(&servers, "nats-servers", servers, "NATS URLs")
	fs.BoolVar(&d.Production, "production", d.Production, "require secure NATS")
	fs.BoolVar(&d.CheckConfig, "check-config", false, "print validated configuration without writing files")
	fs.BoolVar(&d.LogAddSource, "log-add-source", d.LogAddSource, "include source")
	fs.IntVar(&d.MaxConcurrent, "max-concurrent", d.MaxConcurrent, "global execution budget")
	fs.Int64Var(&d.BlobMaxBytes, "blob-max-bytes", d.BlobMaxBytes, "blob budget")
	fs.Int64Var(&d.TempMaxBytes, "temp-max-bytes", d.TempMaxBytes, "temporary budget")
	fs.Int64Var(&d.WorkspaceMaxBytes, "workspace-max-bytes", d.WorkspaceMaxBytes, "workspace budget")
	fs.IntVar(&d.LogRotateMaxSizeMB, "log-rotate-max-size-mb", d.LogRotateMaxSizeMB, "log size")
	fs.IntVar(&d.LogRotateMaxBackups, "log-rotate-max-backups", d.LogRotateMaxBackups, "log backups")
	fs.IntVar(&d.LogRotateMaxAgeDays, "log-rotate-max-age-days", d.LogRotateMaxAgeDays, "log retention")
	fs.BoolVar(&d.LogRotateCompress, "log-rotate-compress", d.LogRotateCompress, "compress logs")
	if err := fs.Parse(args); err != nil {
		return d, err
	}
	if fs.NArg() != 0 {
		return d, fmt.Errorf("unexpected daemon arguments: %v", fs.Args())
	}
	d.NATSServers = splitCSV(servers)
	d.ConfigPath = configPath
	d.DataDir, err = expandPath(d.DataDir)
	if err != nil {
		return d, err
	}
	d.DataDir, err = filepath.Abs(d.DataDir)
	if err != nil {
		return d, err
	}
	for _, p := range []*string{&d.APITLSCertFile, &d.APITLSKeyFile, &d.NATSCredentialsFile, &d.NATSTLSCAFile, &d.NATSTLSCertFile, &d.NATSTLSKeyFile, &d.LogFilePath} {
		if *p != "" {
			*p, err = expandPath(*p)
			if err != nil {
				return d, err
			}
			if !filepath.IsAbs(*p) {
				*p = filepath.Join(d.DataDir, *p)
			}
		}
	}
	if err := ValidateDaemon(d); err != nil {
		return d, err
	}
	if err := ValidateDaemonLayout(d.DataDir); err != nil {
		return d, err
	}
	return d, nil
}

func ValidateDaemonLayout(root string) error {
	for _, name := range []string{"identity.key", "identity.pub", "trust.json", "replay-cache.json", "peer-attrs.json", "config.yaml", "config.yml"} {
		_, err := os.Lstat(filepath.Join(root, name))
		if err == nil {
			return fmt.Errorf("legacy layout (%s): explicit stopped migration required", name)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	data, err := store.ReadFile(filepath.Join(root, "migration.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := protocol.DecodeStrictLimit(data, &fields, 64<<20); err != nil {
		return err
	}
	var version int
	var status string
	if err := json.Unmarshal(fields["schemaVersion"], &version); err != nil {
		return err
	}
	if err := json.Unmarshal(fields["status"], &status); err != nil {
		return err
	}
	if version != 2 || status != "complete" {
		return errors.New("migration is not committed; refuse startup")
	}
	return nil
}
