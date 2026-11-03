package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"clawsynapse/internal/api"
	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/logging"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/migration"
	"clawsynapse/internal/store"
	"clawsynapse/internal/transfer"
	"clawsynapse/internal/trust"
)

type App struct {
	manager *Manager
	api     *api.DaemonServer
	lock    *store.FileLock
	blobs   *blob.Store
}

func NewDaemon(cfg config.DaemonConfig, version string) (_ *App, err error) {
	if err = migration.CheckStartup(cfg.DataDir); err != nil {
		return nil, err
	}
	if err = config.ValidateDaemon(cfg); err != nil {
		return nil, err
	}
	if err = config.ValidateDaemonLayout(cfg.DataDir); err != nil {
		return nil, err
	}
	if err = store.EnsurePrivateDir(cfg.DataDir); err != nil {
		return nil, err
	}
	lock, err := store.Lock(filepath.Join(cfg.DataDir, "daemon.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock daemon root: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = lock.Close()
		}
	}()
	if err = migration.CheckStartup(cfg.DataDir); err != nil {
		return nil, err
	}
	for _, path := range []string{filepath.Join(cfg.DataDir, "bots"), filepath.Join(cfg.DataDir, "blobs")} {
		if err = store.EnsurePrivateDir(path); err != nil {
			return nil, err
		}
	}
	log, err := logging.New(logging.Options{Level: cfg.LogLevel, Format: cfg.LogFormat, FilePath: cfg.LogFilePath, AddSource: cfg.LogAddSource, Rotate: logging.RotateOptions{MaxSizeMB: cfg.LogRotateMaxSizeMB, MaxBackups: cfg.LogRotateMaxBackups, MaxAgeDays: cfg.LogRotateMaxAgeDays, Compress: cfg.LogRotateCompress}})
	if err != nil {
		return nil, err
	}
	slog.SetDefault(log.With("service", "clawsynapsed"))
	blobs, err := blob.New(cfg.DataDir, blob.Limits{MaxBytes: cfg.BlobMaxBytes, TempBytes: cfg.TempMaxBytes, WorkspaceBytes: cfg.WorkspaceMaxBytes})
	if err != nil {
		return nil, fmt.Errorf("open content store: %w", err)
	}
	manager, err := newManager(cfg, version, blobs)
	if err != nil {
		return nil, err
	}
	server, err := api.NewDaemonServer(cfg, manager)
	if err != nil {
		return nil, err
	}
	committed = true
	return &App{manager: manager, api: server, lock: lock, blobs: blobs}, nil
}
func (a *App) Run(ctx context.Context) error {
	gcCtx, gcCancel := context.WithCancel(ctx)
	gcDone := make(chan struct{})
	go func() {
		defer close(gcDone)
		collect := func() {
			if err := a.blobs.GC(); err != nil {
				slog.Warn("content collection paused", "error", err)
			}
		}
		collect()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-gcCtx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
	defer func() { gcCancel(); <-gcDone }()
	a.manager.Resume()
	errors := make(chan error, 1)
	go func() { errors <- a.api.Start() }()
	var result error
	select {
	case <-ctx.Done():
	case result = <-errors:
	}
	a.api.StopWrites()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpErr := a.api.Shutdown(shutdown)
	stopErr := a.manager.Shutdown(shutdown)
	gcCancel()
	<-gcDone
	// Never release exclusive ownership while a timed-out runtime may still be alive.
	if stopErr == nil {
		_ = a.lock.Close()
	}
	if result != nil {
		return result
	}
	if stopErr != nil {
		return stopErr
	}
	return httpErr
}

func (m *Manager) offline(b *managedBot) (http.Handler, func(), error) {
	cfg := config.RuntimeConfig(m.cfg, b.cfg, b.info.BotID)
	lock, err := store.Lock(filepath.Join(cfg.DataDir, "runtime.lock"))
	if err != nil {
		return nil, nil, err
	}
	fs, id, peers, err := loadBotState(cfg, b.info.BotID)
	if err != nil {
		_ = lock.Close()
		return nil, nil, err
	}
	messages := messaging.NewService(slog.Default(), peers, nil, b.info.BotID, id, cfg.TrustMode, cfg.DeliverablePrefixes)
	messages.SetRole(b.cfg.Role)
	close := func() { messages.Close(); _ = lock.Close() }
	if err = messages.SetStatePath(filepath.Join(cfg.DataDir, "state", "messages.jsonl")); err != nil {
		close()
		return nil, nil, err
	}
	trusts, err := trust.NewService(slog.Default(), peers, nil, fs, b.info.BotID, id, cfg.TrustAutoApprove)
	if err != nil {
		close()
		return nil, nil, err
	}
	transfers := transfer.NewService(slog.Default(), peers, nil, messages, b.info.BotID, id, cfg.TrustMode, transfer.TransferConfig{TransferDir: cfg.TransferDir, MaxFileSize: cfg.TransferMaxFileSize, TTL: cfg.TransferTTL, BotID: b.info.BotID, Blobs: m.blobs, Workspace: filepath.Join(cfg.DataDir, "workspace", b.cfg.BindingRevision), QuotaBytes: b.cfg.FileQuotaBytes, InboxOnly: b.cfg.Role == config.RoleConsole})
	if err = transfers.ConfigurePersistence(); err != nil {
		close()
		return nil, nil, err
	}
	handler := api.NewBotHandler(peers, nil, trusts, messages, transfers, nil, nil, cfg.AgentAdapter, api.SelfInfo{BotID: b.info.BotID, Role: b.cfg.Role, DID: b.info.DID, TrustMode: b.cfg.TrustMode, DisplayName: b.cfg.DisplayName, Labels: b.cfg.Labels}, m.version, fs)
	return handler.Handler(), func() { _ = transfers.Close(); close() }, nil
}
