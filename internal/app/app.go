package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/api"
	"clawsynapse/internal/auth"
	"clawsynapse/internal/blob"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/internal/transfer"
	"clawsynapse/internal/trust"
	"clawsynapse/pkg/types"
)

// Runtime owns every mutable network and execution dependency of one Bot.
// A closed Runtime is never started again.
type Runtime struct {
	log       *slog.Logger
	handler   http.Handler
	discovery *discovery.Service
	auth      *auth.Service
	trust     *trust.Service
	messaging *messaging.Service
	identity  *identity.Identity
	transfer  *transfer.Service
	bus       *natsbus.Client
	harness   adapter.AgentAdapter
	limiter   *adapter.BotLimiter
	lock      *store.FileLock
	ctx       context.Context
	cancel    context.CancelFunc
	busCancel context.CancelFunc
	once      sync.Once
}

func NewRuntime(d config.DaemonConfig, b config.BotConfig, botID, version string, blobs *blob.Store, scheduler *adapter.Scheduler) (_ *Runtime, err error) {
	if err = config.ValidateBot(b); err != nil {
		return nil, fmt.Errorf("validate Bot: %w", err)
	}
	cfg := config.RuntimeConfig(d, b, botID)
	lock, err := store.Lock(filepath.Join(cfg.DataDir, "runtime.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock Bot: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{log: slog.Default().With("botId", botID), lock: lock, ctx: ctx, cancel: cancel}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	fs, id, peers, err := loadBotState(cfg, botID)
	if err != nil {
		return nil, err
	}
	r.identity = id
	busCtx, busCancel := context.WithCancel(context.Background())
	r.busCancel = busCancel
	r.bus, err = natsbus.Connect(busCtx, cfg.NATSServers, "clawsynapsed-"+botID, natsbus.Options{CredentialsFile: cfg.NATSCredentialsFile, TLSCAFile: cfg.NATSTLSCAFile, TLSCertFile: cfg.NATSTLSCertFile, TLSKeyFile: cfg.NATSTLSKeyFile, TLSServerName: cfg.NATSTLSServerName})
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}
	replay, err := auth.NewReplayGuard(fs, 10000, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("load replay state: %w", err)
	}
	hb, _ := time.ParseDuration(cfg.HeartbeatInterval)
	ttl, _ := time.ParseDuration(cfg.AnnounceTTL)
	did := identity.DeriveBotDID(id.PublicKey)
	r.discovery = discovery.NewService(r.log, r.bus, peers, fs, botID, did, base64.RawURLEncoding.EncodeToString(id.PublicKey), hb, ttl, cfg.TrustMode, discoveryAnnounceConfig(b, id, version))
	r.auth = auth.NewService(r.log, peers, r.bus, botID, id, replay, cfg.TrustMode)
	r.discovery.SetAutoAuthenticator(r.auth.StartChallenge)
	r.trust, err = trust.NewService(r.log, peers, r.bus, fs, botID, id, cfg.TrustAutoApprove)
	if err != nil {
		return nil, fmt.Errorf("load trust: %w", err)
	}
	r.messaging = messaging.NewService(r.log, peers, r.bus, botID, id, cfg.TrustMode, cfg.DeliverablePrefixes)
	r.messaging.SetRole(b.Role)
	if err = r.messaging.SetStatePath(filepath.Join(cfg.DataDir, "state", "messages.jsonl")); err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	if b.Role == config.RoleAgent {
		r.limiter = scheduler.ForBot(botID, b.MaxConcurrent, b.MaxQueued)
		r.messaging.SetExecutionLimiter(r.limiter)
		sessionStore := store.NewFSStore(cfg.DataDir)
		sessionStore.SessionDir = filepath.Join(cfg.DataDir, "sessions", b.BindingRevision)
		r.harness, err = newAgentAdapter(cfg, b, botID, r.log, sessionStore)
		if err != nil {
			return nil, fmt.Errorf("create Harness: %w", err)
		}
		timeout, err := resolveAgentAdapterTimeout(cfg)
		if err != nil {
			return nil, err
		}
		apiAddr := "http://" + d.LocalAPIAddr
		if d.APITLSCertFile != "" {
			apiAddr = "https://" + d.LocalAPIAddr
		}
		opts := []messaging.HandlerOption{messaging.WithExecutionLimiter(r.limiter), messaging.WithPublishContext(apiAddr, adapter.ResolveCLIPath())}
		if cfg.AgentAdapter == "webhook" {
			opts = append(opts, messaging.WithFeedbackDelivery())
		}
		r.messaging.SetMessageHandler(messaging.NewAdapterMessageHandler(r.harness, timeout, opts...))
	}
	r.transfer = transfer.NewService(r.log, peers, r.bus, r.messaging, botID, id, cfg.TrustMode, transfer.TransferConfig{TransferDir: cfg.TransferDir, MaxFileSize: cfg.TransferMaxFileSize, TTL: cfg.TransferTTL, BotID: botID, Blobs: blobs, Workspace: filepath.Join(cfg.DataDir, "workspace", b.BindingRevision), QuotaBytes: b.FileQuotaBytes, InboxOnly: b.Role == config.RoleConsole})
	if err = r.transfer.ConfigurePersistence(); err != nil {
		return nil, fmt.Errorf("load transfers: %w", err)
	}
	r.messaging.SetTransferHandler(r.transfer.HandleTransferNotification)
	r.transfer.OnReceived(func(rec transfer.TransferRecord) {
		content, _ := json.Marshal(map[string]any{"transferId": rec.TransferID, "fileName": rec.FileName, "fileSize": rec.FileSize, "localPath": rec.LocalPath, "mimeType": rec.MimeType})
		err := r.messaging.HandleLocalNotification(messaging.IncomingMessage{Context: r.ctx, MessageID: "transfer.received:" + rec.TransferID, Type: "transfer.received", From: rec.PeerBot, To: botID, Payload: protocol.MessagePayload{Input: content}, Metadata: rec.Metadata})
		if err != nil {
			r.log.Warn("file execution notification failed", "transferId", rec.TransferID, "error", err)
		}
	})
	r.handler = api.NewBotHandler(peers, r.auth, r.trust, r.messaging, r.transfer, r.bus, r.harness, cfg.AgentAdapter, api.SelfInfo{BotID: botID, Role: b.Role, DID: did, IdentityFingerprint: identity.Fingerprint(id.PublicKey), TrustMode: cfg.TrustMode, DisplayName: cfg.DisplayName, Labels: cfg.Labels}, version, fs).Handler()
	return r, nil
}

// discoveryAnnounceConfig exposes only the public configured binding, never
// instance identifiers, paths, credentials, or execution health.
func discoveryAnnounceConfig(b config.BotConfig, id *identity.Identity, version string) discovery.AnnounceConfig {
	if b.AgentVersion != "" {
		version = b.AgentVersion
	}
	announce := discovery.AnnounceConfig{
		Identity:     id,
		AgentProduct: b.AgentProduct,
		AgentVersion: version,
		DisplayName:  b.DisplayName,
		Labels:       b.Labels,
	}
	if b.Role == config.RoleAgent && b.Harness != nil {
		announce.HarnessAdapter = b.Harness.Adapter
		announce.HarnessModel = b.Harness.Model
	}
	return announce
}

func loadBotState(cfg config.Config, botID string) (*store.FSStore, *identity.Identity, *discovery.Registry, error) {
	if err := store.EnsurePrivateDir(cfg.DataDir); err != nil {
		return nil, nil, nil, err
	}
	id, err := identity.Load(cfg.IdentityKeyPath, cfg.IdentityPubPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load identity: %w", err)
	}
	did := identity.DeriveBotDID(id.PublicKey)
	if identity.DeriveBotID(did) != botID {
		return nil, nil, nil, fmt.Errorf("identity does not match Bot directory")
	}
	fs := store.NewFSStore(cfg.DataDir)
	// Existing persisted state must parse before the Bot can run; never reset corrupt files.
	if _, err = fs.LoadTrustState(); err != nil {
		return nil, nil, nil, fmt.Errorf("load trust: %w", err)
	}
	if _, err = fs.LoadReplayState(); err != nil {
		return nil, nil, nil, fmt.Errorf("load replay state: %w", err)
	}
	attrs, err := fs.LoadPeerAttrsState()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load peer attributes: %w", err)
	}
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{BotID: botID, DID: did, AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted, Inbox: "clawsynapse.msg." + botID + ".inbox", DisplayName: cfg.DisplayName, Labels: cfg.Labels})
	peers.LoadLocalAttrs(attrs.Peers)
	return fs, id, peers, nil
}

func newAgentAdapter(cfg config.Config, b config.BotConfig, botID string, log *slog.Logger, fs *store.FSStore) (adapter.AgentAdapter, error) {
	workspace := filepath.Join(cfg.DataDir, "workspace", b.BindingRevision)
	if err := store.EnsurePrivateDir(workspace); err != nil {
		return nil, err
	}
	switch cfg.AgentAdapter {
	case "default":
		return adapter.NewDefaultAdapter(botID), nil
	case "opencode":
		return adapter.NewOpenCodeAdapter(adapter.OpenCodeConfig{BotID: botID, BindingRevision: b.BindingRevision, Workspace: workspace, Model: b.Harness.Model, Logger: log, SessionStore: fs, AllowAgentIDs: cfg.AgentAllowedIDs})
	case "codex":
		return adapter.NewCodexAdapter(adapter.CodexConfig{BotID: botID, BindingRevision: b.BindingRevision, Workspace: workspace, Model: b.Harness.Model, Logger: log, SessionStore: fs})
	case "pi":
		return adapter.NewPiAdapter(adapter.PiConfig{BotID: botID, BindingRevision: b.BindingRevision, Workspace: workspace, Model: b.Harness.Model, Logger: log, SessionStore: fs})
	case "omp":
		return adapter.NewOMPAdapter(adapter.OMPConfig{BotID: botID, BindingRevision: b.BindingRevision, Workspace: workspace, Model: b.Harness.Model, Logger: log, SessionStore: fs})
	case "openclaw":
		return adapter.NewOpenClawAdapter(adapter.OpenClawConfig{BotID: botID, BindingRevision: b.BindingRevision, Workspace: workspace, Model: b.Harness.Model, Logger: log, AllowAgentIDs: cfg.AgentAllowedIDs})
	case "webhook":
		token := ""
		if b.Harness.CredentialsFile != "" {
			data, err := os.ReadFile(b.Harness.CredentialsFile)
			if err != nil {
				return nil, err
			}
			token = strings.TrimSpace(string(data))
		}
		return adapter.NewWebhookAdapter(adapter.WebhookConfig{BotID: botID, BindingRevision: b.BindingRevision, Instance: b.Harness.Instance, URL: cfg.WebhookURL, AuthToken: token, Logger: log})
	default:
		return nil, fmt.Errorf("unsupported Harness adapter: %s", cfg.AgentAdapter)
	}
}
func resolveAgentAdapterTimeout(cfg config.Config) (time.Duration, error) {
	if strings.TrimSpace(cfg.AgentAdapterTimeout) == "" {
		return 10 * time.Minute, nil
	}
	d, err := time.ParseDuration(cfg.AgentAdapterTimeout)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid Harness timeout")
	}
	return d, nil
}
func (r *Runtime) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.auth.Start(); err != nil {
		return fmt.Errorf("start authentication: %w", err)
	}
	if err := r.trust.Start(); err != nil {
		return fmt.Errorf("start trust: %w", err)
	}
	if err := r.messaging.Start(); err != nil {
		return fmt.Errorf("start messaging: %w", err)
	}
	if err := r.transfer.Start(r.ctx); err != nil {
		return fmt.Errorf("start transfers: %w", err)
	}
	if err := r.bus.FlushTimeout(3 * time.Second); err != nil {
		return err
	}
	if err := r.discovery.Start(r.ctx); err != nil {
		return fmt.Errorf("start discovery: %w", err)
	}
	return r.bus.FlushTimeout(3 * time.Second)
}
func (r *Runtime) Handler() http.Handler { return r.handler }
func (r *Runtime) Close() {
	r.once.Do(func() {
		r.cancel()
		if r.discovery != nil {
			r.discovery.Wait()
		}
		if r.bus != nil {
			r.bus.Quiesce()
		}
		if r.messaging != nil {
			r.messaging.Close()
		}
		if r.transfer != nil {
			_ = r.transfer.Close()
		}
		if r.limiter != nil {
			r.limiter.Close()
		}
		if r.bus != nil {
			_ = r.bus.FlushTimeout(time.Second)
			r.bus.Close()
		}
		if r.busCancel != nil {
			r.busCancel()
		}
		if r.lock != nil {
			_ = r.lock.Close()
		}
	})
}
