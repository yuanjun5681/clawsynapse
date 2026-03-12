package api

import (
	"encoding/json"
	"net/http"
	"time"

	"clawsynapse/internal/messaging"
	"clawsynapse/internal/protocol"
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
	TargetNode string         `json:"targetNode"`
	Message    string         `json:"message"`
	SessionKey string         `json:"sessionKey,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type requestReq struct {
	TargetNode string         `json:"targetNode"`
	Message    string         `json:"message"`
	SessionKey string         `json:"sessionKey,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	TimeoutMs  int64          `json:"timeoutMs,omitempty"`
}

func (s *Server) handlePeers(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "peers.ok",
		Message: "peers fetched",
		Data: map[string]any{
			"items": s.peers.List(),
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	var req challengeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
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

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "health.ok",
		Message: "service healthy",
		Data: map[string]any{
			"peersCount": len(s.peers.List()),
			"nats":       natsStatus,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleTrustRequest(w http.ResponseWriter, r *http.Request) {
	var req trustRequestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	msgID, err := s.messaging.Publish(messaging.PublishRequest{
		TargetNode: req.TargetNode,
		Message:    req.Message,
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
			"messageId":  msgID,
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

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	var req requestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "invalid json payload", TS: time.Now().UnixMilli()})
		return
	}

	result, err := s.messaging.Request(messaging.RequestRequest{
		TargetNode: req.TargetNode,
		Message:    req.Message,
		SessionKey: req.SessionKey,
		Metadata:   req.Metadata,
		Timeout:    time.Duration(req.TimeoutMs) * time.Millisecond,
	})
	if err != nil {
		code := "msg.request_failed"
		if protocolErr, ok := err.(*protocol.Error); ok {
			code = protocolErr.Code
		}
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    code,
			Message: err.Error(),
			Data: map[string]any{
				"targetNode": req.TargetNode,
				"timeoutMs":  req.TimeoutMs,
			},
			TS: time.Now().UnixMilli(),
		})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "msg.replied",
		Message: "reply received",
		Data: map[string]any{
			"targetNode": req.TargetNode,
			"requestId":  result.RequestID,
			"messageId":  result.MessageID,
			"reply":      result.Reply,
			"from":       result.From,
			"runId":      result.RunID,
		},
		TS: time.Now().UnixMilli(),
	})
}
