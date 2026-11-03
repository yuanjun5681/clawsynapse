package main

import (
	"clawsynapse/internal/config"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultLogLines        = 80
	defaultLogFollowPeriod = 1 * time.Second
)

type logProvider interface {
	ReadLogs(ctx context.Context, lines int) (string, error)
}

type defaultLogProvider struct {
	runner serviceRunner
}

func runLogs(args []string, stdout, stderr io.Writer) error {
	return runLogsWithProvider(args, stdout, stderr, defaultLogProvider{runner: execServiceRunner{}})
}

func runLogsWithProvider(args []string, stdout, stderr io.Writer, provider logProvider) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	lines := fs.Int("lines", defaultLogLines, "number of log lines to display")
	follow := fs.Bool("follow", false, "follow logs continuously")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *lines <= 0 {
		return errors.New("lines must be greater than 0")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logs, err := provider.ReadLogs(ctx, *lines)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, logs)
	if logs != "" && !strings.HasSuffix(logs, "\n") {
		fmt.Fprintln(stdout)
	}
	if !*follow {
		return nil
	}

	followCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return followLogs(followCtx, provider, stdout, stderr, *lines, defaultLogFollowPeriod, logs)
}

func followLogs(ctx context.Context, provider logProvider, stdout, stderr io.Writer, lines int, interval time.Duration, initial string) error {
	previous := initial
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return nil
		case <-ticker.C:
			readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			current, err := provider.ReadLogs(readCtx, lines)
			cancel()
			if err != nil {
				fmt.Fprintf(stderr, "log follow error: %v\n", err)
				continue
			}
			delta := newLogContent(previous, current)
			if delta != "" {
				fmt.Fprint(stdout, delta)
				if !strings.HasSuffix(delta, "\n") {
					fmt.Fprintln(stdout)
				}
			}
			previous = current
		}
	}
}

func newLogContent(previous, current string) string {
	if current == previous {
		return ""
	}
	if previous == "" {
		return current
	}
	if strings.HasPrefix(current, previous) {
		return strings.TrimPrefix(current, previous)
	}
	return current
}

func (p defaultLogProvider) ReadLogs(ctx context.Context, lines int) (string, error) {
	home, err := serviceUserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".clawsynapse")
	if value := os.Getenv("CLAWSYNAPSE_DATA_DIR"); value != "" {
		root = value
	}
	cfg, err := config.LoadDaemon([]string{"--data-dir", root})
	if err != nil {
		return "", err
	}
	if cfg.LogFilePath != "" {
		text, err := readLastLines(cfg.LogFilePath, lines)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "no logs available\n", nil
		}
		return text, nil
	}
	switch serviceGOOS {
	case "linux":
		return p.readLinuxLogs(ctx, lines)
	case "darwin":
		return p.readLaunchdLogs(lines)
	default:
		return "", fmt.Errorf("logs are not supported on %s", serviceGOOS)
	}
}

func (p defaultLogProvider) readLinuxLogs(ctx context.Context, lines int) (string, error) {
	home, err := serviceUserHomeDir()
	if err != nil {
		return "", err
	}
	logPath := filepath.Join(home, ".clawsynapse", "log", "clawsynapsed.log")
	text, fileErr := readLastLines(logPath, lines)
	if fileErr == nil {
		if strings.TrimSpace(text) == "" {
			return "no logs available\n", nil
		}
		return text, nil
	}
	return p.readSystemdLogs(ctx, lines)
}

func (p defaultLogProvider) readSystemdLogs(ctx context.Context, lines int) (string, error) {
	args := []string{"-u", serviceUnitName, "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso"}
	out, err := p.runner.Run(ctx, "journalctl", args...)
	if err == nil {
		return string(out), nil
	}

	sudoArgs := append([]string{"-n", "journalctl"}, args...)
	out2, err2 := p.runner.Run(ctx, "sudo", sudoArgs...)
	if err2 == nil {
		return string(out2), nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = strings.TrimSpace(string(out2))
	}
	if msg != "" {
		return "", fmt.Errorf("read systemd logs: %s", msg)
	}
	return "", fmt.Errorf("read systemd logs: %w", err)
}

func (p defaultLogProvider) readLaunchdLogs(lines int) (string, error) {
	home, err := serviceUserHomeDir()
	if err != nil {
		return "", err
	}
	logDir := filepath.Join(home, ".clawsynapse", "log")
	combinedPath := filepath.Join(logDir, "clawsynapsed.log")
	stdoutPath := filepath.Join(logDir, "clawsynapsed.stdout.log")
	stderrPath := filepath.Join(logDir, "clawsynapsed.stderr.log")

	combinedText, combinedErr := readLastLines(combinedPath, lines)
	if combinedErr == nil {
		if strings.TrimSpace(combinedText) == "" {
			return "no logs available\n", nil
		}
		return combinedText, nil
	}

	stdoutText, stdoutErr := readLastLines(stdoutPath, lines)
	stderrText, stderrErr := readLastLines(stderrPath, lines)
	if stdoutErr != nil && stderrErr != nil {
		return "", fmt.Errorf("read launchd logs: %v; %v; %v", combinedErr, stdoutErr, stderrErr)
	}

	parts := make([]string, 0, 2)
	if strings.TrimSpace(stdoutText) != "" {
		parts = append(parts, "== stdout ==\n"+stdoutText)
	}
	if strings.TrimSpace(stderrText) != "" {
		parts = append(parts, "== stderr ==\n"+stderrText)
	}
	if len(parts) == 0 {
		return "no logs available\n", nil
	}
	return strings.Join(parts, "\n\n"), nil
}

func readLastLines(path string, lines int) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return "", nil
	}
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}
