package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"clawsynapse/internal/auth"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/trust"
)

type Server struct {
	httpServer *http.Server
	peers      *discovery.Registry
	auth       *auth.Service
	trust      *trust.Service
	messaging  *messaging.Service
	nats       *natsbus.Client
}

func NewServer(addr string, peers *discovery.Registry, authSvc *auth.Service, trustSvc *trust.Service, messagingSvc *messaging.Service, natsClient *natsbus.Client) *Server {
	s := &Server{peers: peers, auth: authSvc, trust: trustSvc, messaging: messagingSvc, nats: natsClient}

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
	mux.HandleFunc("GET /v1/health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	err := s.httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
