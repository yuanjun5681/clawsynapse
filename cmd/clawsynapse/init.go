package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	initDefaultNATSServers         = "nats://220.168.146.21:9414"
	initDefaultLocalAPIAddr        = "127.0.0.1:18080"
	initDefaultTrustMode           = "tofu"
	initDefaultAgentAdapter        = "default"
	initDefaultAgentAdapterTimeout = "10m"
	initDefaultDeliverablePrefixes = "chat,task"
	initDefaultHeartbeat           = "15s"
	initDefaultAnnounceTTL         = "30s"
	initDefaultTransferMaxFileSize = "104857600"
	initDefaultTransferTTL         = "24h"
	initDefaultLogLevel            = "info"
	initDefaultLogFormat           = "json"
)

type initConfig struct {
	ConfigPath          string
	NATSServers         string
	LocalAPIAddr        string
	TrustMode           string
	TrustAutoApprove    bool
	AgentAdapter        string
	AgentAdapterTimeout string
	WebhookURL          string
	DeliverablePrefixes string
	DataDir             string
	TransferDir         string
	HeartbeatInterval   string
	AnnounceTTL         string
	TransferMaxFileSize string
	TransferTTL         string
	LogLevel            string
	LogFormat           string
	Overwrite           bool
	transferDirExplicit bool
}

func runInit(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cfg, err := parseInitArgs(args, stderr)
	if err != nil {
		return err
	}

	interactive := isInteractiveInput(stdin)
	if interactive {
		if err := promptInitConfig(stdin, stdout, &cfg); err != nil {
			return err
		}
	}

	if err := finalizeInitConfig(&cfg); err != nil {
		return err
	}

	if err := maybeConfirmOverwrite(stdin, stdout, interactive, cfg); err != nil {
		return err
	}

	if err := writeInitConfig(cfg); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "config written: %s\n", cfg.ConfigPath)
	fmt.Fprintf(stdout, "natsServers: %s\n", cfg.NATSServers)
	fmt.Fprintf(stdout, "agentAdapter: %s\n", cfg.AgentAdapter)
	fmt.Fprintf(stdout, "next: run `clawsynapse service restart` to apply changes\n")
	return nil
}

func parseInitArgs(args []string, stderr io.Writer) (initConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return initConfig{}, err
	}

	defaultDataDir := filepath.Join(home, ".clawsynapse")
	defaultConfigPath := filepath.Join(defaultDataDir, "config.yaml")
	cfg := initConfig{
		ConfigPath:          defaultConfigPath,
		NATSServers:         initDefaultNATSServers,
		LocalAPIAddr:        initDefaultLocalAPIAddr,
		TrustMode:           initDefaultTrustMode,
		AgentAdapter:        initDefaultAgentAdapter,
		AgentAdapterTimeout: initDefaultAgentAdapterTimeout,
		DeliverablePrefixes: initDefaultDeliverablePrefixes,
		DataDir:             defaultDataDir,
		HeartbeatInterval:   initDefaultHeartbeat,
		AnnounceTTL:         initDefaultAnnounceTTL,
		TransferMaxFileSize: initDefaultTransferMaxFileSize,
		TransferTTL:         initDefaultTransferTTL,
		LogLevel:            initDefaultLogLevel,
		LogFormat:           initDefaultLogFormat,
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "config file path")
	fs.StringVar(&cfg.NATSServers, "nats-servers", cfg.NATSServers, "comma-separated NATS servers")
	fs.StringVar(&cfg.LocalAPIAddr, "local-api-addr", cfg.LocalAPIAddr, "local API address")
	fs.StringVar(&cfg.TrustMode, "trust-mode", cfg.TrustMode, "trust mode: open|tofu|explicit")
	fs.BoolVar(&cfg.TrustAutoApprove, "trust-auto-approve", false, "automatically approve valid inbound trust requests")
	fs.StringVar(&cfg.AgentAdapter, "agent-adapter", cfg.AgentAdapter, "agent adapter: default|openclaw|opencode|codex|webhook")
	fs.StringVar(&cfg.AgentAdapterTimeout, "agent-adapter-timeout", cfg.AgentAdapterTimeout, "timeout for delivering a message to the agent adapter")
	fs.StringVar(&cfg.WebhookURL, "webhook-url", cfg.WebhookURL, "webhook URL when using webhook adapter")
	fs.StringVar(&cfg.DeliverablePrefixes, "deliverable-prefixes", cfg.DeliverablePrefixes, "comma-separated deliverable prefixes")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "state directory")
	fs.StringVar(&cfg.TransferDir, "transfer-dir", cfg.TransferDir, "transfer directory")
	fs.StringVar(&cfg.HeartbeatInterval, "heartbeat", cfg.HeartbeatInterval, "heartbeat interval")
	fs.StringVar(&cfg.AnnounceTTL, "announce-ttl", cfg.AnnounceTTL, "announce ttl")
	fs.StringVar(&cfg.TransferMaxFileSize, "transfer-max-file-size", cfg.TransferMaxFileSize, "max transfer file size in bytes")
	fs.StringVar(&cfg.TransferTTL, "transfer-ttl", cfg.TransferTTL, "transfer object TTL")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level: debug|info|warn|error")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "log format: json|text")
	fs.BoolVar(&cfg.Overwrite, "overwrite", false, "overwrite an existing config file without prompting")

	if err := fs.Parse(args); err != nil {
		return initConfig{}, err
	}

	if len(fs.Args()) > 0 {
		return initConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	cfg.transferDirExplicit = hasFlag(args, "--transfer-dir")

	return cfg, nil
}

func promptInitConfig(stdin io.Reader, stdout io.Writer, cfg *initConfig) error {
	reader := bufio.NewReader(stdin)
	fmt.Fprintln(stdout, "ClawSynapse daemon configuration")

	var err error
	if cfg.NATSServers, err = promptValue(reader, stdout, "NATS servers (comma-separated)", cfg.NATSServers); err != nil {
		return err
	}
	if cfg.AgentAdapter, err = promptChoice(reader, stdout, "Agent adapter", cfg.AgentAdapter, []string{"default", "openclaw", "opencode", "codex", "webhook"}); err != nil {
		return err
	}
	if cfg.AgentAdapter == "webhook" {
		if cfg.WebhookURL, err = promptRequired(reader, stdout, "Webhook URL", cfg.WebhookURL); err != nil {
			return err
		}
	} else {
		cfg.WebhookURL = ""
	}
	if cfg.TrustMode, err = promptChoice(reader, stdout, "Trust mode", cfg.TrustMode, []string{"open", "tofu", "explicit"}); err != nil {
		return err
	}
	if cfg.LocalAPIAddr, err = promptValue(reader, stdout, "Local API address", cfg.LocalAPIAddr); err != nil {
		return err
	}
	if cfg.DeliverablePrefixes, err = promptValue(reader, stdout, "Deliverable prefixes (comma-separated)", cfg.DeliverablePrefixes); err != nil {
		return err
	}
	if cfg.DataDir, err = promptValue(reader, stdout, "Data directory", cfg.DataDir); err != nil {
		return err
	}
	transferCurrent := cfg.TransferDir
	if !cfg.transferDirExplicit && strings.TrimSpace(transferCurrent) == "" {
		transferCurrent = filepath.Join(cfg.DataDir, "transfers")
	}
	if cfg.TransferDir, err = promptValue(reader, stdout, "Transfer directory", transferCurrent); err != nil {
		return err
	}
	if cfg.LogLevel, err = promptChoice(reader, stdout, "Log level", cfg.LogLevel, []string{"debug", "info", "warn", "error"}); err != nil {
		return err
	}
	if cfg.LogFormat, err = promptChoice(reader, stdout, "Log format", cfg.LogFormat, []string{"json", "text"}); err != nil {
		return err
	}
	return nil
}

func finalizeInitConfig(cfg *initConfig) error {
	cfg.ConfigPath = strings.TrimSpace(cfg.ConfigPath)
	cfg.NATSServers = strings.TrimSpace(cfg.NATSServers)
	cfg.LocalAPIAddr = strings.TrimSpace(cfg.LocalAPIAddr)
	cfg.TrustMode = strings.ToLower(strings.TrimSpace(cfg.TrustMode))
	cfg.AgentAdapter = strings.ToLower(strings.TrimSpace(cfg.AgentAdapter))
	cfg.AgentAdapterTimeout = strings.TrimSpace(cfg.AgentAdapterTimeout)
	cfg.WebhookURL = strings.TrimSpace(cfg.WebhookURL)
	cfg.DeliverablePrefixes = strings.TrimSpace(cfg.DeliverablePrefixes)
	cfg.DataDir = strings.TrimSpace(cfg.DataDir)
	cfg.TransferDir = strings.TrimSpace(cfg.TransferDir)
	cfg.HeartbeatInterval = strings.TrimSpace(cfg.HeartbeatInterval)
	cfg.AnnounceTTL = strings.TrimSpace(cfg.AnnounceTTL)
	cfg.TransferMaxFileSize = strings.TrimSpace(cfg.TransferMaxFileSize)
	cfg.TransferTTL = strings.TrimSpace(cfg.TransferTTL)
	cfg.LogLevel = strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	cfg.LogFormat = strings.ToLower(strings.TrimSpace(cfg.LogFormat))

	if cfg.ConfigPath == "" {
		return errors.New("config path is required")
	}
	if cfg.NATSServers == "" {
		return errors.New("nats servers is required")
	}
	if cfg.LocalAPIAddr == "" {
		return errors.New("local API address is required")
	}
	if cfg.DataDir == "" {
		return errors.New("data directory is required")
	}
	if cfg.AgentAdapterTimeout == "" {
		cfg.AgentAdapterTimeout = initDefaultAgentAdapterTimeout
	}
	if d, err := time.ParseDuration(cfg.AgentAdapterTimeout); err != nil || d <= 0 {
		return fmt.Errorf("invalid agent adapter timeout: %s", cfg.AgentAdapterTimeout)
	}
	if !cfg.transferDirExplicit || cfg.TransferDir == "" {
		cfg.TransferDir = filepath.Join(cfg.DataDir, "transfers")
	}
	switch cfg.TrustMode {
	case "open", "tofu", "explicit":
	default:
		return fmt.Errorf("invalid trust mode: %s", cfg.TrustMode)
	}
	switch cfg.AgentAdapter {
	case "default", "openclaw", "opencode", "codex", "webhook":
	default:
		return fmt.Errorf("invalid agent adapter: %s", cfg.AgentAdapter)
	}
	if cfg.AgentAdapter == "webhook" && cfg.WebhookURL == "" {
		return errors.New("webhook url is required when agent adapter is webhook")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level: %s", cfg.LogLevel)
	}
	switch cfg.LogFormat {
	case "json", "text":
	default:
		return fmt.Errorf("invalid log format: %s", cfg.LogFormat)
	}

	var err error
	cfg.ConfigPath, err = expandHomePath(cfg.ConfigPath)
	if err != nil {
		return err
	}
	cfg.DataDir, err = expandHomePath(cfg.DataDir)
	if err != nil {
		return err
	}
	cfg.TransferDir, err = expandHomePath(cfg.TransferDir)
	if err != nil {
		return err
	}
	return nil
}

func maybeConfirmOverwrite(stdin io.Reader, stdout io.Writer, interactive bool, cfg initConfig) error {
	if cfg.Overwrite {
		return nil
	}
	if _, err := os.Stat(cfg.ConfigPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !interactive {
		return fmt.Errorf("config already exists: %s (use --overwrite to replace it)", cfg.ConfigPath)
	}

	reader := bufio.NewReader(stdin)
	ok, err := promptYesNo(reader, stdout, fmt.Sprintf("Overwrite existing config %s", cfg.ConfigPath), false)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("aborted")
	}
	return nil
}

func writeInitConfig(cfg initConfig) error {
	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.TransferDir, 0o700); err != nil {
		return fmt.Errorf("create transfer directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(cfg.ConfigPath), ".clawsynapse-config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	content := renderInitConfig(cfg)
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, cfg.ConfigPath); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func renderInitConfig(cfg initConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "natsServers:\n")
	for _, item := range splitCSVList(cfg.NATSServers) {
		fmt.Fprintf(&b, "  - %s\n", item)
	}
	fmt.Fprintf(&b, "localApiAddr: %s\n", cfg.LocalAPIAddr)
	fmt.Fprintf(&b, "trustMode: %s\n", cfg.TrustMode)
	fmt.Fprintf(&b, "trustAutoApprove: %t\n", cfg.TrustAutoApprove)
	fmt.Fprintf(&b, "agentAdapter: %s\n", cfg.AgentAdapter)
	fmt.Fprintf(&b, "agentAdapterTimeout: %s\n", cfg.AgentAdapterTimeout)
	if cfg.WebhookURL != "" {
		fmt.Fprintf(&b, "webhookUrl: %s\n", cfg.WebhookURL)
	}
	fmt.Fprintf(&b, "heartbeatInterval: %s\n", cfg.HeartbeatInterval)
	fmt.Fprintf(&b, "announceTtl: %s\n", cfg.AnnounceTTL)
	fmt.Fprintf(&b, "dataDir: %s\n", cfg.DataDir)
	fmt.Fprintf(&b, "identityKeyPath: %s\n", filepath.Join(cfg.DataDir, "identity.key"))
	fmt.Fprintf(&b, "identityPubPath: %s\n", filepath.Join(cfg.DataDir, "identity.pub"))
	fmt.Fprintf(&b, "deliverablePrefixes:\n")
	for _, item := range splitCSVList(cfg.DeliverablePrefixes) {
		fmt.Fprintf(&b, "  - %s\n", item)
	}
	fmt.Fprintf(&b, "transferDir: %s\n", cfg.TransferDir)
	fmt.Fprintf(&b, "transferMaxFileSize: %s\n", cfg.TransferMaxFileSize)
	fmt.Fprintf(&b, "transferTtl: %s\n", cfg.TransferTTL)
	fmt.Fprintf(&b, "logLevel: %s\n", cfg.LogLevel)
	fmt.Fprintf(&b, "logFormat: %s\n", cfg.LogFormat)
	return b.String()
}

func splitCSVList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func expandHomePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func isInteractiveInput(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func promptValue(reader *bufio.Reader, stdout io.Writer, label, current string) (string, error) {
	if current != "" {
		fmt.Fprintf(stdout, "%s [%s]: ", label, current)
	} else {
		fmt.Fprintf(stdout, "%s: ", label)
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	return line, nil
}

func promptRequired(reader *bufio.Reader, stdout io.Writer, label, current string) (string, error) {
	for {
		value, err := promptValue(reader, stdout, label, current)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
		fmt.Fprintln(stdout, "value is required")
	}
}

func promptChoice(reader *bufio.Reader, stdout io.Writer, label, current string, allowed []string) (string, error) {
	for {
		value, err := promptValue(reader, stdout, fmt.Sprintf("%s (%s)", label, strings.Join(allowed, "/")), current)
		if err != nil {
			return "", err
		}
		value = strings.ToLower(strings.TrimSpace(value))
		for _, item := range allowed {
			if value == item {
				return value, nil
			}
		}
		fmt.Fprintf(stdout, "choose one of: %s\n", strings.Join(allowed, ", "))
	}
}

func promptYesNo(reader *bufio.Reader, stdout io.Writer, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	for {
		fmt.Fprintf(stdout, "%s %s: ", label, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		line = strings.ToLower(strings.TrimSpace(line))
		if line == "" {
			return defaultYes, nil
		}
		if line == "y" || line == "yes" {
			return true, nil
		}
		if line == "n" || line == "no" {
			return false, nil
		}
		fmt.Fprintln(stdout, "enter yes or no")
	}
}
