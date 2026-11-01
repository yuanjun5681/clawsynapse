package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"clawsynapse/internal/app"
	"clawsynapse/internal/config"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if isVersionCommand(args) {
		fmt.Fprintln(os.Stdout, version)
		return
	}

	cfg, err := config.LoadDaemon(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if cfg.CheckConfig {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(cfg)
		return
	}

	a, err := app.NewDaemon(cfg, version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap error: %v\n", err)
		os.Exit(1)
	}
	log := slog.Default()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		sig := <-sigCh
		log.Info("shutdown signal received", slog.String("signal", sig.String()))
		cancel()
	}()

	log.Info("daemon starting")
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("daemon stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	log.Info("daemon stopped")
}

func isVersionCommand(args []string) bool {
	if len(args) != 1 {
		return false
	}
	return args[0] == "version" || args[0] == "--version"
}
