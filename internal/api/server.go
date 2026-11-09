package api

import (
	"net/http"
	"sync"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/auth"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/store"
	"clawsynapse/internal/transfer"
	"clawsynapse/internal/trust"
	"clawsynapse/pkg/types"
)

// Server is one Bot's business interface. The daemon owns listener and authorization.
type Server struct {
	peers       *discovery.Registry
	auth        *auth.Service
	trust       *trust.Service
	messaging   *messaging.Service
	transfer    *transfer.Service
	nats        *natsbus.Client
	adapter     adapter.AgentAdapter
	adapterName string
	self        SelfInfo
	attrs       *store.FSStore
	attrsMu     sync.Mutex
	version     string
}

type SelfInfo struct {
	BotID               string
	Role                string
	DID                 string
	IdentityFingerprint string
	TrustMode           string
	DisplayName         string
	Labels              types.Labels
}

func NewBotHandler(peers *discovery.Registry, authSvc *auth.Service, trustSvc *trust.Service, messages *messaging.Service, transfers *transfer.Service, bus *natsbus.Client, harness adapter.AgentAdapter, adapterName string, self SelfInfo, version string, fs *store.FSStore) *Server {
	return &Server{peers: peers, auth: authSvc, trust: trustSvc, messaging: messages, transfer: transfers, nats: bus, adapter: harness, adapterName: adapterName, self: self, attrs: fs, version: version}
}

// Handler uses relative paths: the authorized daemon dispatcher has already selected a Bot.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /peers", s.handlePeers)
	mux.HandleFunc("GET /peers/{peerID}/attrs", s.handlePeerAttrsGet)
	mux.HandleFunc("PUT /peers/{peerID}/attrs", s.handlePeerAttrsPut)
	mux.HandleFunc("DELETE /peers/{peerID}/attrs", s.handlePeerAttrsDelete)
	mux.HandleFunc("POST /auth/challenge", s.handleAuthChallenge)
	mux.HandleFunc("POST /trust/request", s.handleTrustRequest)
	mux.HandleFunc("POST /trust/approve", s.handleTrustApprove)
	mux.HandleFunc("POST /trust/reject", s.handleTrustReject)
	mux.HandleFunc("POST /trust/revoke", s.handleTrustRevoke)
	mux.HandleFunc("GET /trust/pending", s.handleTrustPending)
	mux.HandleFunc("GET /trust", s.handleTrustSnapshot)
	mux.HandleFunc("POST /publish", s.handlePublish)
	mux.HandleFunc("GET /messages", s.handleMessages)
	mux.HandleFunc("GET /requests/{messageId}", s.handleRequest)
	mux.HandleFunc("GET /requests/{messageId}/context", s.handleTopicContext)
	mux.HandleFunc("POST /transfer/send", s.handleTransferSend)
	mux.HandleFunc("POST /transfer/import", s.handleTransferImport)
	mux.HandleFunc("GET /transfers", s.handleTransfers)
	mux.HandleFunc("GET /transfer/{transferId}", s.handleTransfer)
	mux.HandleFunc("GET /transfer/{transferId}/file", s.handleTransferFile)
	mux.HandleFunc("POST /transfer/{transferId}/forward", s.handleTransferForward)
	mux.HandleFunc("DELETE /transfer/{transferId}", s.handleTransferDelete)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}
