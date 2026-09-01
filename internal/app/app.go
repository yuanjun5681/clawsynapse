package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
	"clawsynapse/internal/transfer"
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
	transfer  *transfer.Service
	bus       *natsbus.Client
	peers     *discovery.Registry
	identity  *identity.Identity
}

func New(cfg config.Config, version string) (*App, error) {
	fs := store.NewFSStore(cfg.DataDir)
	if err := fs.EnsureLayout(); err != nil {
		return nil, fmt.Errorf("init fs store: %w", err)
	}

	id, err := identity.LoadOrCreate(cfg.IdentityKeyPath, cfg.IdentityPubPath)
	if err != nil {
		return nil, fmt.Errorf("load identity: %w", err)
	}

	// derive DID and subject-safe node ID from public key
	nodeDID := identity.DeriveNodeDID(id.PublicKey)
	nodeID := identity.DeriveNodeID(nodeDID)

	log, err := logging.New(logging.Options{
		Level:     cfg.LogLevel,
		Format:    cfg.LogFormat,
		AddSource: cfg.LogAddSource,
		FilePath:  cfg.LogFilePath,
		Rotate: logging.RotateOptions{
			MaxSizeMB:  cfg.LogRotateMaxSizeMB,
			MaxBackups: cfg.LogRotateMaxBackups,
			MaxAgeDays: cfg.LogRotateMaxAgeDays,
			Compress:   cfg.LogRotateCompress,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}
	log = log.With(
		slog.String("service", "clawsynapsed"),
		slog.String("nodeId", nodeID),
		slog.String("did", nodeDID),
	)

	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{NodeID: nodeID, DID: nodeDID, AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted, Inbox: "clawsynapse.msg." + nodeID + ".inbox"})

	hb, err := time.ParseDuration(cfg.HeartbeatInterval)
	if err != nil {
		return nil, fmt.Errorf("parse heartbeat interval: %w", err)
	}
	ttl, err := time.ParseDuration(cfg.AnnounceTTL)
	if err != nil {
		return nil, fmt.Errorf("parse announce ttl: %w", err)
	}

	bus, err := natsbus.Connect(context.Background(), cfg.NATSServers, "clawsynapsed-"+nodeID)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	replay, err := auth.NewReplayGuard(fs, 10000, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("init replay guard: %w", err)
	}

	agentVersion := strings.TrimSpace(cfg.AgentVersion)
	if agentVersion == "" {
		agentVersion = version
	}
	discoverySvc := discovery.NewService(
		log.With(slog.String("component", "discovery")),
		bus,
		peers,
		fs,
		nodeID,
		nodeDID,
		base64.RawURLEncoding.EncodeToString(id.PublicKey),
		hb,
		ttl,
		cfg.TrustMode,
		discovery.AnnounceConfig{
			AgentProduct: cfg.AgentProduct,
			AgentVersion: agentVersion,
			Capabilities: cfg.Capabilities,
		},
	)
	authSvc := auth.NewService(log.With(slog.String("component", "auth")), peers, bus, nodeID, id, replay, cfg.TrustMode)
	discoverySvc.SetAutoAuthenticator(authSvc.StartChallenge)
	trustSvc, err := trust.NewService(log.With(slog.String("component", "trust")), peers, bus, fs, nodeID, id, cfg.TrustAutoApprove)
	if err != nil {
		return nil, fmt.Errorf("init trust service: %w", err)
	}
	messagingSvc := messaging.NewService(log.With(slog.String("component", "messaging")), peers, bus, nodeID, id, cfg.TrustMode, cfg.DeliverablePrefixes)
	agentAdapter, err := newAgentAdapter(cfg, nodeID, log, fs)
	if err != nil {
		return nil, fmt.Errorf("init agent adapter: %w", err)
	}
	agentAdapterTimeout, err := resolveAgentAdapterTimeout(cfg)
	if err != nil {
		return nil, err
	}
	var handlerOpts []messaging.HandlerOption
	if cfg.AgentAdapter == "webhook" {
		handlerOpts = append(handlerOpts, messaging.WithFeedbackDelivery())
	}
	adapterHandler := messaging.NewAdapterMessageHandler(agentAdapter, agentAdapterTimeout, handlerOpts...)
	messagingSvc.SetMessageHandler(adapterHandler)

	transferSvc := transfer.NewService(
		log.With(slog.String("component", "transfer")),
		peers, bus, messagingSvc, nodeID, id, cfg.TrustMode,
		transfer.TransferConfig{
			TransferDir: cfg.TransferDir,
			MaxFileSize: cfg.TransferMaxFileSize,
			TTL:         cfg.TransferTTL,
		},
	)
	messagingSvc.SetTransferHandler(transferSvc.HandleTransferNotification)

	transferSvc.OnReceived(func(rec transfer.TransferRecord) {
		content, _ := json.Marshal(map[string]any{
			"transferId": rec.TransferID,
			"fileName":   rec.FileName,
			"fileSize":   rec.FileSize,
			"localPath":  rec.LocalPath,
			"mimeType":   rec.MimeType,
		})
		msg := messaging.IncomingMessage{
			MessageID: "transfer.received:" + rec.TransferID,
			Type:      "transfer.received",
			From:      rec.PeerNode,
			To:        nodeID,
			Message:   string(content),
			Metadata:  rec.Metadata,
		}
		if _, err := adapterHandler.HandleMessage(msg); err != nil {
			log.Warn("deliver transfer.received to agent failed",
				slog.String("transferId", rec.TransferID),
				slog.String("error", err.Error()),
			)
		}
	})

	apiServer := api.NewServer(cfg.LocalAPIAddr, peers, authSvc, trustSvc, messagingSvc, transferSvc, bus, agentAdapter, cfg.AgentAdapter, api.SelfInfo{
		NodeID:              nodeID,
		DID:                 nodeDID,
		IdentityFingerprint: identity.Fingerprint(id.PublicKey),
		TrustMode:           cfg.TrustMode,
	}, version, cfg)

	return &App{
		log:       log,
		cfg:       cfg,
		api:       apiServer,
		discovery: discoverySvc,
		auth:      authSvc,
		trust:     trustSvc,
		messaging: messagingSvc,
		transfer:  transferSvc,
		bus:       bus,
		peers:     peers,
		identity:  id,
	}, nil
}

func newAgentAdapter(cfg config.Config, nodeID string, log *slog.Logger, fs *store.FSStore) (adapter.AgentAdapter, error) {
	switch cfg.AgentAdapter {
	case "", "default":
		return adapter.NewDefaultAdapter(nodeID), nil
	case "openclaw":
		return adapter.NewOpenClawAdapter(adapter.OpenClawConfig{
			NodeID: nodeID,
			Logger: log.With(slog.String("component", "adapter"), slog.String("adapter", "openclaw")),
		})
	case "opencode":
		return adapter.NewOpenCodeAdapter(adapter.OpenCodeConfig{
			NodeID:       nodeID,
			Logger:       log.With(slog.String("component", "adapter"), slog.String("adapter", "opencode")),
			SessionStore: fs,
		})
	case "codex":
		return adapter.NewCodexAdapter(adapter.CodexConfig{
			NodeID:       nodeID,
			Logger:       log.With(slog.String("component", "adapter"), slog.String("adapter", "codex")),
			SessionStore: fs,
		})
	case "webhook":
		return adapter.NewWebhookAdapter(adapter.WebhookConfig{
			NodeID:    nodeID,
			URL:       cfg.WebhookURL,
			AuthToken: cfg.WebhookAuthToken,
			Logger:    log.With(slog.String("component", "adapter"), slog.String("adapter", "webhook")),
		})
	default:
		return nil, fmt.Errorf("unsupported agent adapter: %s", cfg.AgentAdapter)
	}
}

func resolveAgentAdapterTimeout(cfg config.Config) (time.Duration, error) {
	timeout := strings.TrimSpace(cfg.AgentAdapterTimeout)
	if timeout == "" {
		return 10 * time.Minute, nil
	}
	d, err := time.ParseDuration(timeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("parse agent adapter timeout: %s", timeout)
	}
	return d, nil
}

func (a *App) Run(ctx context.Context) error {
	a.log.Info("starting clawsynapsed",
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
		a.log.Warn("nats not connected within timeout, transfer may be disabled", slog.String("error", err.Error()))
	}
	if err := a.transfer.Start(ctx); err != nil {
		return fmt.Errorf("start transfer service: %w", err)
	}
	if a.transfer.Enabled() {
		a.log.Info("transfer service ready")
	}
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
