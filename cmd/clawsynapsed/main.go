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
	"clawsynapse/internal/logging"
)

func main() {
	cfg, err := config.LoadFromOS(os.Args[1:])
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

	log := logging.New(logging.Options{
		Level:     cfg.LogLevel,
		Format:    cfg.LogFormat,
		AddSource: cfg.LogAddSource,
	}).With(
		slog.String("service", "clawsynapsed"),
		slog.String("nodeId", cfg.NodeID),
	)

	a, err := app.New(cfg)
	if err != nil {
		log.Error("bootstrap failed", slog.String("error", err.Error()))
		os.Exit(1)
	}

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
