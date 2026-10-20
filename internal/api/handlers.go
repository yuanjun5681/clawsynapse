package api

import (
	"net/http"
	"time"

	"clawsynapse/internal/config"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/transfer"
	"clawsynapse/pkg/types"
)

type challengeReq struct {
	TargetNode string `json:"targetNode"`
}

type trustRequestReq struct {
	TargetNode   string   `json:"targetNode"`
	Reason       string   `json:"reason,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type trustDecisionReq struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}

type trustRevokeReq struct {
	TargetNode string `json:"targetNode"`
	Reason     string `json:"reason,omitempty"`
}

type publishReq struct {
	TargetNode string                   `json:"targetNode"`
	Type       string                   `json:"type,omitempty"`
	AgentID    string                   `json:"agentId,omitempty"`
	Message    *string                  `json:"message,omitempty"`
	Payload    *protocol.MessagePayload `json:"payload,omitempty"`
	ExpiresAt  int64                    `json:"expiresAt,omitempty"`
	SessionKey string                   `json:"sessionKey,omitempty"`
	Metadata   map[string]any           `json:"metadata,omitempty"`
}

func (s *Server) handlePeers(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "peers.ok",
		Message: "peers fetched",
		Data: map[string]any{
			"items": s.listRemotePeers(),
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) listRemotePeers() []types.Peer {
	peers := s.peers.List()
	out := make([]types.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.NodeID == s.self.NodeID {
			continue
		}
		out = append(out, peer)
	}
	return out
}

func (s *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var req challengeReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "invalid_argument",
			Message: "invalid json payload",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	ctx, cancel := contextWithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := s.auth.StartChallenge(ctx, req.TargetNode); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "auth.challenge_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetNode": req.TargetNode,
			},
			TS: time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "auth.challenge_accepted",
		Message: "challenge completed",
		Data: map[string]any{
			"targetNode": req.TargetNode,
			"status":     "authenticated",
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	natsStatus := map[string]any{"connected": false, "status": "unavailable"}
	if s.nats != nil {
		st := s.nats.Status()
		natsStatus = map[string]any{
			"name":             st.Name,
			"serverUrl":        st.ServerURL,
			"connected":        st.Connected,
			"status":           st.Status,
			"connectedAt":      st.ConnectedAt,
			"lastDisconnectAt": st.LastDisconnectAt,
			"lastReconnectAt":  st.LastReconnectAt,
			"disconnects":      st.Disconnects,
			"reconnects":       st.Reconnects,
			"lastError":        st.LastError,
			"inMsgs":           st.InMsgs,
			"outMsgs":          st.OutMsgs,
			"inBytes":          st.InBytes,
			"outBytes":         st.OutBytes,
		}
	}

	adapterStatus := map[string]any{
		"name":    s.adapterName,
		"healthy": false,
	}
	if s.adapter == nil {
		adapterStatus["error"] = "agent adapter unavailable"
	} else {
		ctx, cancel := contextWithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		st, err := s.adapter.GetStatus(ctx)
		if err != nil {
			adapterStatus["error"] = err.Error()
		}
		if st != nil {
			adapterStatus["healthy"] = st.Healthy
			adapterStatus["degraded"] = st.Degraded
			adapterStatus["detail"] = st.Detail
		}
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "health.ok",
		Message: "service healthy",
		Data: map[string]any{
			"self": map[string]any{
				"nodeId":              s.self.NodeID,
				"did":                 s.self.DID,
				"version":             s.version,
				"identityFingerprint": s.self.IdentityFingerprint,
				"trustMode":           s.self.TrustMode,
			},
			"peersCount": len(s.listRemotePeers()),
			"nats":       natsStatus,
			"adapter":    adapterStatus,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrustRequest(w http.ResponseWriter, r *http.Request) {
	var req trustRequestReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	requestID, err := s.trust.Request(r.Context(), req.TargetNode, req.Reason, req.Capabilities)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "trust.request_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetNode": req.TargetNode,
			},
			TS: time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "trust.requested",
		Message: "trust request sent",
		Data: map[string]any{
			"targetNode": req.TargetNode,
			"requestId":  requestID,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrustApprove(w http.ResponseWriter, r *http.Request) {
	s.handleTrustDecision(w, r, "approve")
}

func (s *Server) handleTrustReject(w http.ResponseWriter, r *http.Request) {
	s.handleTrustDecision(w, r, "reject")
}

func (s *Server) handleTrustDecision(w http.ResponseWriter, r *http.Request, decision string) {
	var req trustDecisionReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	var err error
	if decision == "approve" {
		err = s.trust.Approve(req.RequestID, req.Reason)
	} else {
		err = s.trust.Reject(req.RequestID, req.Reason)
	}
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "trust.response_failed", Message: err.Error(), TS: time.Now().UnixMilli()})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "trust.responded",
		Message: "trust decision sent",
		Data: map[string]any{
			"requestId": req.RequestID,
			"decision":  decision,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrustRevoke(w http.ResponseWriter, r *http.Request) {
	var req trustRevokeReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	if err := s.trust.Revoke(req.TargetNode, req.Reason); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "trust.revoke_failed", Message: err.Error(), TS: time.Now().UnixMilli()})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "trust.revoked",
		Message: "trust revoked",
		Data: map[string]any{
			"targetNode": req.TargetNode,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrustPending(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "trust.pending",
		Message: "pending trust requests fetched",
		Data: map[string]any{
			"items": s.trust.Pending(),
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}
	if req.Message != nil && req.Payload != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "message and payload are mutually exclusive", TS: time.Now().UnixMilli()})
		return
	}
	var message string
	var payload protocol.MessagePayload
	if req.Message != nil {
		message = *req.Message
	}
	if req.Payload != nil {
		payload = *req.Payload
	}

	result, err := s.messaging.Publish(messaging.PublishRequest{
		TargetNode: req.TargetNode,
		Type:       req.Type,
		AgentID:    req.AgentID,
		Message:    message,
		Payload:    payload,
		ExpiresAt:  req.ExpiresAt,
		SessionKey: req.SessionKey,
		Metadata:   req.Metadata,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "msg.publish_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetNode": req.TargetNode,
			},
			TS: time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "msg.published",
		Message: "message published",
		Data: map[string]any{
			"targetNode": req.TargetNode,
			"messageId":  result.MessageID,
			"sessionKey": result.SessionKey,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleMessages(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "msg.recent",
		Message: "recent messages fetched",
		Data: map[string]any{
			"items": s.messaging.RecentMessages(100),
		},
		TS: time.Now().UnixMilli(),
	})
}

type transferSendReq struct {
	TargetNode string         `json:"targetNode"`
	FilePath   string         `json:"filePath"`
	MimeType   string         `json:"mimeType,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func (s *Server) handleTransferSend(w http.ResponseWriter, r *http.Request) {
	if s.transfer == nil || !s.transfer.Enabled() {
		respondJSON(w, http.StatusServiceUnavailable, types.APIResult{
			OK:      false,
			Code:    "transfer.disabled",
			Message: "transfer service not available (jetstream required)",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	var req transferSendReq
	if err := decodeRequest(r, &req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	result, err := s.transfer.SendFile(transfer.SendFileRequest{
		TargetNode: req.TargetNode,
		FilePath:   req.FilePath,
		MimeType:   req.MimeType,
		Metadata:   req.Metadata,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "transfer.send_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetNode": req.TargetNode,
			},
			TS: time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "transfer.sent",
		Message: "file transfer initiated",
		Data: map[string]any{
			"transferId": result.TransferID,
			"bucket":     result.Bucket,
			"size":       result.Size,
			"checksum":   result.Checksum,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTransfers(w http.ResponseWriter, _ *http.Request) {
	if s.transfer == nil {
		respondJSON(w, http.StatusServiceUnavailable, types.APIResult{
			OK:      false,
			Code:    "transfer.disabled",
			Message: "transfer service not available",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "transfer.list",
		Message: "transfers fetched",
		Data: map[string]any{
			"items": s.transfer.ListTransfers(),
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if s.transfer == nil {
		respondJSON(w, http.StatusServiceUnavailable, types.APIResult{
			OK:      false,
			Code:    "transfer.disabled",
			Message: "transfer service not available",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	transferID := r.PathValue("transferId")
	info, ok := s.transfer.GetTransfer(transferID)
	if !ok {
		respondJSON(w, http.StatusNotFound, types.APIResult{
			OK:      false,
			Code:    "transfer.not_found",
			Message: "transfer not found",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "transfer.detail",
		Message: "transfer fetched",
		Data: map[string]any{
			"transfer": info,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTransferDelete(w http.ResponseWriter, r *http.Request) {
	if s.transfer == nil || !s.transfer.Enabled() {
		respondJSON(w, http.StatusServiceUnavailable, types.APIResult{
			OK:      false,
			Code:    "transfer.disabled",
			Message: "transfer service not available",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	transferID := r.PathValue("transferId")
	if err := s.transfer.DeleteTransfer(transferID); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "transfer.delete_failed",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "transfer.deleted",
		Message: "transfer deleted",
		Data: map[string]any{
			"transferId": transferID,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "config.ok",
		Message: "config fetched",
		Data: map[string]any{
			"config": s.cfg,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	if err := decodeRequest(r, &cfg); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "invalid_argument",
			Message: "invalid json payload",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	if err := config.Validate(cfg); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "config.invalid",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	if s.configPath == "" {
		respondJSON(w, http.StatusInternalServerError, types.APIResult{
			OK:      false,
			Code:    "config.no_path",
			Message: "config file path not available",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	if err := config.SaveToFile(s.configPath, cfg); err != nil {
		respondJSON(w, http.StatusInternalServerError, types.APIResult{
			OK:      false,
			Code:    "config.save_failed",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "config.saved",
		Message: "config saved, restart daemon to apply",
		TS:      time.Now().UnixMilli(),
	})
}
