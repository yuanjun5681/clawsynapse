package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/auth"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/transfer"
	"clawsynapse/internal/trust"
)

type Server struct {
	httpServer  *http.Server
	peers       *discovery.Registry
	auth        *auth.Service
	trust       *trust.Service
	messaging   *messaging.Service
	transfer    *transfer.Service
	nats        *natsbus.Client
	adapter     adapter.AgentAdapter
	adapterName string
	self        SelfInfo
	version     string
	cfg         config.Config
	configPath  string
}

type SelfInfo struct {
	NodeID              string
	DID                 string
	IdentityFingerprint string
	TrustMode           string
}

func NewServer(addr string, peers *discovery.Registry, authSvc *auth.Service, trustSvc *trust.Service, messagingSvc *messaging.Service, transferSvc *transfer.Service, natsClient *natsbus.Client, agentAdapter adapter.AgentAdapter, agentAdapterName string, self SelfInfo, version string, cfg config.Config) (*Server, error) {
	if err := config.ValidateAPIListener(addr, cfg.AllowRemoteAPI, cfg.APITLSCertFile, cfg.APITLSKeyFile); err != nil {
		return nil, err
	}
	if cfg.APITLSCertFile != "" {
		if _, err := tls.LoadX509KeyPair(cfg.APITLSCertFile, cfg.APITLSKeyFile); err != nil {
			return nil, fmt.Errorf("load API TLS certificate: %w", err)
		}
	}
	if cfg.APITokenPath == "" {
		if cfg.DataDir == "" {
			return nil, errors.New("API token path or data directory is required")
		}
		cfg.APITokenPath = filepath.Join(cfg.DataDir, "api.token")
	}
	token, err := ensureToken(cfg.APITokenPath)
	if err != nil {
		return nil, err
	}
	s := &Server{
		peers:       peers,
		auth:        authSvc,
		trust:       trustSvc,
		messaging:   messagingSvc,
		transfer:    transferSvc,
		nats:        natsClient,
		adapter:     agentAdapter,
		adapterName: agentAdapterName,
		self:        self,
		version:     version,
		cfg:         cfg,
		configPath:  cfg.ConfigPath,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/peers", s.handlePeers)
	mux.HandleFunc("POST /v1/auth/challenge", s.handleAuthChallenge)
	mux.HandleFunc("POST /v1/trust/request", s.handleTrustRequest)
	mux.HandleFunc("POST /v1/trust/approve", s.handleTrustApprove)
	mux.HandleFunc("POST /v1/trust/reject", s.handleTrustReject)
	mux.HandleFunc("POST /v1/trust/revoke", s.handleTrustRevoke)
	mux.HandleFunc("GET /v1/trust/pending", s.handleTrustPending)
	mux.HandleFunc("POST /v1/publish", s.handlePublish)
	mux.HandleFunc("GET /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /v1/transfer/send", s.handleTransferSend)
	mux.HandleFunc("GET /v1/transfers", s.handleTransfers)
	mux.HandleFunc("GET /v1/transfer/{transferId}", s.handleTransfer)
	mux.HandleFunc("DELETE /v1/transfer/{transferId}", s.handleTransferDelete)
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/config", s.handleConfigGet)
	mux.HandleFunc("PUT /v1/config", s.handleConfigSave)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           bearerAuth(token, mux),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	return s, nil
}

func (s *Server) Start() error {
	var err error
	if s.cfg.APITLSCertFile != "" {
		err = s.httpServer.ListenAndServeTLS(s.cfg.APITLSCertFile, s.cfg.APITLSKeyFile)
	} else {
		err = s.httpServer.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
