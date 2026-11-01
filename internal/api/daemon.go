package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"clawsynapse/internal/config"
	"clawsynapse/pkg/types"
)

type BotInfo struct {
	BotID             string `json:"botId"`
	DID               string `json:"did,omitempty"`
	DisplayName       string `json:"displayName"`
	DesiredState      string `json:"desiredState"`
	State             string `json:"state"`
	Archived          bool   `json:"archived"`
	Revision          uint64 `json:"revision"`
	BindingRevision   string `json:"bindingRevision"`
	Error             string `json:"error,omitempty"`
	NetworkHealthy    bool   `json:"networkHealthy"`
	HarnessHealthy    bool   `json:"harnessHealthy"`
	HarnessApplicable bool   `json:"harnessApplicable"`
}

// BotManager is the authorized management seam. Acquire pins a runtime until release.
type BotManager interface {
	List() []BotInfo
	Info(string) (BotInfo, error)
	Config(string) (config.BotConfig, error)
	Create(config.BotConfig) (BotInfo, error)
	Update(string, config.BotConfig) (BotInfo, error)
	Transition(string, string) (BotInfo, error)
	Acquire(string, bool) (http.Handler, func(), error)
	Authenticate(string) (string, bool)
	BindWorkspace(string, string) (BotInfo, string, error)
}

type Error struct {
	Status int
	Code   string
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }
func Failure(status int, code, message string) error {
	return &Error{Status: status, Code: code, Err: errors.New(message)}
}

func respondError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "daemon.failed"
	var e *Error
	if errors.As(err, &e) {
		status, code = e.Status, e.Code
	}
	respondJSON(w, status, types.APIResult{OK: false, Code: code, Message: err.Error(), TS: time.Now().UnixMilli()})
}
func success(w http.ResponseWriter, status int, code string, data map[string]any) {
	respondJSON(w, status, types.APIResult{OK: true, Code: code, Data: data, TS: time.Now().UnixMilli()})
}

type DaemonServer struct {
	server  *http.Server
	cfg     config.DaemonConfig
	closing atomic.Bool
}

func NewDaemonServer(cfg config.DaemonConfig, manager BotManager) (*DaemonServer, error) {
	if err := config.ValidateAPIListener(cfg.LocalAPIAddr, cfg.AllowRemoteAPI, cfg.APITLSCertFile, cfg.APITLSKeyFile); err != nil {
		return nil, err
	}
	if cfg.APITLSCertFile != "" {
		if _, err := tls.LoadX509KeyPair(cfg.APITLSCertFile, cfg.APITLSKeyFile); err != nil {
			return nil, fmt.Errorf("load API TLS certificate: %w", err)
		}
	}
	token, err := ensureToken(cfg.AdminTokenPath)
	if err != nil {
		return nil, err
	}
	expected := sha256.Sum256([]byte(token))
	s := &DaemonServer{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/health", func(w http.ResponseWriter, r *http.Request) {
		items := manager.List()
		active, failed := 0, 0
		for _, b := range items {
			if b.State == "running" {
				active++
			}
			if b.State == "failed" {
				failed++
			}
		}
		data := map[string]any{"activeBots": active, "failedBots": failed, "botsCount": len(items), "capabilities": []string{"console", "workspace_binding"}}
		if source, ok := manager.(interface{ Version() string }); ok {
			data["version"] = source.Version()
		}
		if source, ok := manager.(interface{ RecoveryIssues() []string }); ok {
			data["recoveryIssues"] = source.RecoveryIssues()
		}
		success(w, 200, "daemon.healthy", data)
	})
	mux.HandleFunc("POST /v2/workspaces/{workspaceID}/binding", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DisplayName string `json:"displayName"`
		}
		if err := decodeRequest(r, &body); err != nil {
			respondError(w, Failure(400, "invalid_argument", err.Error()))
			return
		}
		bot, token, err := manager.BindWorkspace(r.PathValue("workspaceID"), body.DisplayName)
		if err != nil {
			respondError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		success(w, 200, "workspace.bound", map[string]any{"bot": bot, "token": token})
	})
	mux.HandleFunc("GET /v2/bots", func(w http.ResponseWriter, r *http.Request) {
		success(w, 200, "bots.list", map[string]any{"items": manager.List()})
	})
	mux.HandleFunc("POST /v2/bots", func(w http.ResponseWriter, r *http.Request) {
		cfg := config.DefaultBotConfig()
		if err := decodeRequest(r, &cfg); err != nil {
			respondError(w, Failure(400, "invalid_argument", err.Error()))
			return
		}
		b, err := manager.Create(cfg)
		if err != nil {
			respondError(w, err)
			return
		}
		success(w, 201, "bot.created", map[string]any{"bot": b})
	})
	mux.HandleFunc("GET /v2/bots/{botID}", func(w http.ResponseWriter, r *http.Request) {
		b, err := manager.Info(r.PathValue("botID"))
		if err != nil {
			respondError(w, err)
			return
		}
		success(w, 200, "bot.info", map[string]any{"bot": b})
	})
	mux.HandleFunc("GET /v2/bots/{botID}/config", func(w http.ResponseWriter, r *http.Request) {
		cfg, err := manager.Config(r.PathValue("botID"))
		if err != nil {
			respondError(w, err)
			return
		}
		success(w, 200, "bot.config", map[string]any{"config": cfg})
	})
	mux.HandleFunc("PUT /v2/bots/{botID}/config", func(w http.ResponseWriter, r *http.Request) {
		var cfg config.BotConfig
		if err := decodeRequest(r, &cfg); err != nil {
			respondError(w, Failure(400, "invalid_argument", err.Error()))
			return
		}
		b, err := manager.Update(r.PathValue("botID"), cfg)
		if err != nil {
			respondError(w, err)
			return
		}
		success(w, 200, "bot.updated", map[string]any{"bot": b})
	})
	for _, op := range []string{"start", "stop", "archive", "restore"} {
		mux.HandleFunc("POST /v2/bots/{botID}/"+op, func(w http.ResponseWriter, r *http.Request) {
			b, err := manager.Transition(r.PathValue("botID"), op)
			if err != nil {
				respondError(w, err)
				return
			}
			status := 200
			if op == "start" || op == "stop" {
				status = 202
			}
			success(w, status, "bot."+op, map[string]any{"bot": b})
		})
	}
	mux.HandleFunc("/v2/bots/{botID}/{operation...}", func(w http.ResponseWriter, r *http.Request) {
		operation := r.PathValue("operation")
		handler, release, err := manager.Acquire(r.PathValue("botID"), r.Method != http.MethodGet)
		if err != nil {
			respondError(w, err)
			return
		}
		defer release()
		request := r.Clone(r.Context())
		u := *r.URL
		u.Path = "/" + operation
		u.RawPath = ""
		request.URL = &u
		handler.ServeHTTP(w, request)
	})
	s.server = &http.Server{Addr: cfg.LocalAPIAddr, ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		provided := strings.TrimPrefix(header, "Bearer ")
		actual := sha256.Sum256([]byte(provided))
		admin := strings.HasPrefix(header, "Bearer ") && subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
		scope, valid := "", false
		if !admin && strings.HasPrefix(header, "Bearer ") {
			scope, valid = manager.Authenticate(provided)
		}
		if !admin && !valid {
			w.Header().Set("WWW-Authenticate", "Bearer")
			respondError(w, Failure(401, "unauthorized", "bearer authentication required"))
			return
		}
		if s.closing.Load() && r.Method != http.MethodGet {
			respondError(w, Failure(503, "daemon.stopping", "daemon is stopping"))
			return
		}
		if !admin {
			// Scoped credentials may inspect their identity and business state, never configuration or lifecycle.
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) < 3 || parts[0] != "v2" || parts[1] != "bots" || parts[2] != scope {
				respondError(w, Failure(403, "forbidden", "credential does not authorize this Bot or daemon management"))
				return
			}
			if len(parts) == 3 && r.Method != http.MethodGet {
				respondError(w, Failure(403, "forbidden", "Bot credentials cannot manage Bots"))
				return
			}
			if len(parts) > 3 {
				switch parts[3] {
				case "config", "start", "stop", "archive", "restore":
					respondError(w, Failure(403, "forbidden", "Bot credentials cannot change configuration or lifecycle"))
					return
				}
			}
			if len(parts) > 4 && parts[3] == "transfer" && parts[4] == "import" {
				respondError(w, Failure(403, "forbidden", "Bot credentials cannot import host files"))
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		mux.ServeHTTP(w, r)
	})
	return s, nil
}
func (s *DaemonServer) Handler() http.Handler { return s.server.Handler }
func (s *DaemonServer) Start() error {
	var err error
	if s.cfg.APITLSCertFile != "" {
		err = s.server.ListenAndServeTLS(s.cfg.APITLSCertFile, s.cfg.APITLSKeyFile)
	} else {
		err = s.server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func (s *DaemonServer) Shutdown(ctx context.Context) error {
	s.closing.Store(true)
	return s.server.Shutdown(ctx)
}
func (s *DaemonServer) StopWrites() { s.closing.Store(true) }
