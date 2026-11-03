package main

import (
	"bufio"
	"clawsynapse/internal/config"
	"clawsynapse/internal/store"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	initDefaultNATSServers  = "nats://220.168.146.21:9414"
	initDefaultLocalAPIAddr = "127.0.0.1:18080"
	initDefaultLogLevel     = "info"
	initDefaultLogFormat    = "json"
)

type initConfig struct {
	ConfigPath   string
	NATSServers  string
	LocalAPIAddr string
	DataDir      string
	LogLevel     string
	LogFormat    string
	Overwrite    bool
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
	fmt.Fprintf(stdout, "next: run `clawsynapse service restart` to apply changes\n")
	return nil
}

func parseInitArgs(args []string, stderr io.Writer) (initConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return initConfig{}, err
	}

	defaultDataDir := filepath.Join(home, ".clawsynapse")
	defaultConfigPath := filepath.Join(defaultDataDir, "config.json")
	cfg := initConfig{
		ConfigPath:   defaultConfigPath,
		NATSServers:  initDefaultNATSServers,
		LocalAPIAddr: initDefaultLocalAPIAddr,
		DataDir:      defaultDataDir,
		LogLevel:     initDefaultLogLevel,
		LogFormat:    initDefaultLogFormat,
	}

	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.ConfigPath, "config", cfg.ConfigPath, "config file path")
	fs.StringVar(&cfg.NATSServers, "nats-servers", cfg.NATSServers, "comma-separated NATS servers")
	fs.StringVar(&cfg.LocalAPIAddr, "local-api-addr", cfg.LocalAPIAddr, "local API address")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "state directory")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level: debug|info|warn|error")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "log format: json|text")
	fs.BoolVar(&cfg.Overwrite, "overwrite", false, "overwrite an existing config file without prompting")

	if err := fs.Parse(args); err != nil {
		return initConfig{}, err
	}

	if len(fs.Args()) > 0 {
		return initConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	return cfg, nil
}

func promptInitConfig(stdin io.Reader, stdout io.Writer, cfg *initConfig) error {
	reader := bufio.NewReader(stdin)
	fmt.Fprintln(stdout, "ClawSynapse daemon configuration")

	var err error
	if cfg.NATSServers, err = promptValue(reader, stdout, "NATS servers (comma-separated)", cfg.NATSServers); err != nil {
		return err
	}
	if cfg.LocalAPIAddr, err = promptValue(reader, stdout, "Local API address", cfg.LocalAPIAddr); err != nil {
		return err
	}
	if cfg.DataDir, err = promptValue(reader, stdout, "Data directory", cfg.DataDir); err != nil {
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
	cfg.DataDir = strings.TrimSpace(cfg.DataDir)
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
	return config.ValidateDaemon(daemonInitConfig(*cfg))
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

	return store.WriteJSONAtomic(cfg.ConfigPath, daemonInitConfig(cfg), 0o600)
}

func daemonInitConfig(cfg initConfig) config.DaemonConfig {
	d := config.DefaultDaemonConfig()
	d.DataDir = cfg.DataDir
	d.NATSServers = splitCSVList(cfg.NATSServers)
	d.LocalAPIAddr = cfg.LocalAPIAddr
	d.LogLevel = cfg.LogLevel
	d.LogFormat = cfg.LogFormat
	d.LogFilePath = filepath.Join(cfg.DataDir, "log", "clawsynapsed.log")
	return d
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
