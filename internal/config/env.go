package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}

	if strings.HasSuffix(v, "ms") || strings.HasSuffix(v, "s") || strings.HasSuffix(v, "m") {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}

	ms, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}

	return filepath.Abs(path)
}
