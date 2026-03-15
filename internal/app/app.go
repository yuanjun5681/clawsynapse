package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/api"
	"clawsynapse/internal/auth"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/logging"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/store"
	"clawsynapse/internal/trust"
	"clawsynapse/pkg/types"
)

type App struct {
	log       *slog.Logger
	cfg       config.Config
	api       *api.Server
	discovery *discovery.Service
	auth      *auth.Service
	trust     *trust.Service
	messaging *messaging.Service
	bus       *natsbus.Client
	peers     *discovery.Registry
	identity  *identity.Identity
}

func New(cfg config.Config) (*App, error) {
	log := logging.New(logging.Options{
		Level:     cfg.LogLevel,
		Format:    cfg.LogFormat,
		AddSource: cfg.LogAddSource,
	}).With(
		slog.String("service", "clawsynapsed"),
		slog.String("nodeId", cfg.NodeID),
	)

	fs := store.NewFSStore(cfg.DataDir)
	if err := fs.EnsureLayout(); err != nil {
		return nil, fmt.Errorf("init fs store: %w", err)
	}

	id, err := identity.LoadOrCreate(cfg.IdentityKeyPath, cfg.IdentityPubPath)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}

	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{NodeID: cfg.NodeID, AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted, Inbox: "clawsynapse.msg." + cfg.NodeID + ".inbox"})

	hb, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil {
		return nil, fmt.Errorf("parse heartbeat interval: %w", err)
	}
	ttl, err := time.ParseDuration(cfg.AnnounceTTL)
	if err != nil {
		return nil, fmt.Errorf("parse announce ttl: %w", err)
	}

	bus, err := natsbus.Connect(context.Background(), cfg.NATSServers, "clawsynapsed-"+cfg.NodeID)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	replay, err := auth.NewReplayGuard(fs, 10000, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("init replay guard: %w", err)
	}

	discoverySvc := discovery.NewService(log.With(slog.String("component", "discovery")), bus, peers, fs, cfg.NodeID, base64.RawURLEncoding.EncodeToString(id.PublicKey), hb, ttl, cfg.TrustMode)
	authSvc := auth.NewService(log.With(slog.String("component", "auth")), peers, bus, cfg.NodeID, id, replay, cfg.TrustMode)
	discoverySvc.SetAutoAuthenticator(authSvc.StartChallenge)
	trustSvc, err := trust.NewService(log.With(slog.String("component", "trust")), peers, bus, fs, cfg.NodeID, id)
	if err != nil {
		return nil, fmt.Errorf("init trust service: %w", err)
	}
	messagingSvc := messaging.NewService(log.With(slog.String("component", "messaging")), peers, bus, cfg.NodeID, id, cfg.TrustMode)
	agentAdapter, err := newAgentAdapter(cfg, log)
	if err != nil {
		return nil, fmt.Errorf("init agent adapter: %w", err)
	}
	messagingSvc.SetMessageHandler(messaging.NewAdapterMessageHandler(agentAdapter, 30*time.Second))
	apiServer := api.NewServer(cfg.LocalAPIAddr, peers, authSvc, trustSvc, messagingSvc, bus)

	return &App{
		log:       log,
		cfg:       cfg,
		api:       apiServer,
		discovery: discoverySvc,
		auth:      authSvc,
		trust:     trustSvc,
		messaging: messagingSvc,
		bus:       bus,
		peers:     peers,
		identity:  id,
	}, nil
}

func newAgentAdapter(cfg config.Config, log *slog.Logger) (adapter.AgentAdapter, error) {
	switch cfg.AgentAdapter {
	case "", "default":
		return adapter.NewDefaultAdapter(cfg.NodeID), nil
	case "openclaw":
		return adapter.NewOpenClawAdapter(adapter.OpenClawConfig{
			NodeID:  cfg.NodeID,
			AgentID: cfg.OpenClawAgentID,
			Logger:  log.With(slog.String("component", "adapter"), slog.String("adapter", "openclaw")),
		})
	default:
		return nil, fmt.Errorf("unsupported agent adapter: %s", cfg.AgentAdapter)
	}
}

func (a *App) Run(ctx context.Context) error {
	a.log.Info("starting clawsynapsed",
		slog.String("nodeId", a.cfg.NodeID),
		slog.String("apiAddr", a.cfg.LocalAPIAddr),
		slog.String("trustMode", a.cfg.TrustMode),
		slog.String("identityFingerprint", identity.Fingerprint(a.identity.PublicKey)),
	)

	if err := a.auth.Start(); err != nil {
		return fmt.Errorf("start auth service: %w", err)
	}
	a.log.Info("auth subscriptions ready")
	if err := a.trust.Start(); err != nil {
		return fmt.Errorf("start trust service: %w", err)
	}
	a.log.Info("trust subscriptions ready")
	if err := a.messaging.Start(); err != nil {
		return fmt.Errorf("start messaging service: %w", err)
	}
	a.log.Info("messaging subscriptions ready")
	if err := a.bus.FlushTimeout(3 * time.Second); err != nil {
		a.log.Warn("nats flush timeout after control subscriptions", slog.String("error", err.Error()))
	}
	if err := a.discovery.Start(ctx); err != nil {
		return fmt.Errorf("start discovery service: %w", err)
	}
	a.log.Info("discovery subscriptions ready")
	if err := a.bus.FlushTimeout(3 * time.Second); err != nil {
		a.log.Warn("nats flush timeout after discovery start", slog.String("error", err.Error()))
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.api.Start()
	}()

	select {
	case <-ctx.Done():
		a.bus.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return a.api.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
