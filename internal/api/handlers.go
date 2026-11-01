package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"clawsynapse/internal/messaging"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/internal/transfer"
	"clawsynapse/pkg/types"
)

type challengeReq struct {
	TargetBot string `json:"targetBot"`
}

type trustRequestReq struct {
	TargetBot    string   `json:"targetBot"`
	Reason       string   `json:"reason,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type trustDecisionReq struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}

type trustRevokeReq struct {
	TargetBot string `json:"targetBot"`
	Reason    string `json:"reason,omitempty"`
}

type publishReq struct {
	TargetBot      string                   `json:"targetBot"`
	Type           string                   `json:"type,omitempty"`
	AgentID        string                   `json:"agentId,omitempty"`
	Message        *string                  `json:"message,omitempty"`
	Payload        *protocol.MessagePayload `json:"payload,omitempty"`
	ExpiresAt      int64                    `json:"expiresAt,omitempty"`
	SessionKey     string                   `json:"sessionKey,omitempty"`
	Metadata       map[string]any           `json:"metadata,omitempty"`
	IdempotencyKey string                   `json:"idempotencyKey,omitempty"`
}

type peerFilter struct {
	exact  [][2]string
	member [][2]string
	name   string
}

// parsePeerFilter parses the optional `label` (exact scalar), `labelHas`
// (set member) and `name` (substring) query filters for a Bot's peer view.
func parsePeerFilter(r *http.Request) (peerFilter, error) {
	var f peerFilter
	q := r.URL.Query()
	parse := func(raw string) ([2]string, error) {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return [2]string{}, fmt.Errorf("malformed label filter %q", raw)
		}
		return [2]string{strings.TrimSpace(key), value}, nil
	}
	for _, raw := range q["label"] {
		kv, err := parse(raw)
		if err != nil {
			return peerFilter{}, err
		}
		f.exact = append(f.exact, kv)
	}
	for _, raw := range q["labelHas"] {
		kv, err := parse(raw)
		if err != nil {
			return peerFilter{}, err
		}
		f.member = append(f.member, kv)
	}
	f.name = strings.ToLower(strings.TrimSpace(q.Get("name")))
	return f, nil
}

func (f peerFilter) matches(peer types.Peer) bool {
	for _, kv := range f.exact {
		if !peer.Labels.MatchExact(kv[0], kv[1]) {
			return false
		}
	}
	for _, kv := range f.member {
		if !peer.Labels.MatchMember(kv[0], kv[1]) {
			return false
		}
	}
	if f.name != "" {
		display := strings.ToLower(peer.DisplayName)
		alias := strings.ToLower(peer.LocalAlias)
		if !strings.Contains(display, f.name) && !strings.Contains(alias, f.name) {
			return false
		}
	}
	return true
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	filter, err := parsePeerFilter(r)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "peers.invalid_filter",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}
	peers := s.listRemotePeers()
	items := make([]types.Peer, 0, len(peers))
	for _, peer := range peers {
		if filter.matches(peer) {
			items = append(items, peer)
		}
	}
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "peers.ok",
		Message: "peers fetched",
		Data: map[string]any{
			"items": items,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) listRemotePeers() []types.Peer {
	peers := s.peers.List()
	out := make([]types.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.BotID == s.self.BotID {
			continue
		}
		out = append(out, peer)
	}
	return out
}

// peerAttrsPatch updates one remote Bot's local attributes. Pointer fields
// distinguish "key omitted" (leave untouched) from "explicit empty" (clear).
type peerAttrsPatch struct {
	Alias       *string       `json:"alias"`
	Description *string       `json:"description"`
	Labels      *types.Labels `json:"labels"`
}

// peerAttrsBotID validates the {botID} path value. It is a local lookup
// key only and is never joined into a filesystem path.
func peerAttrsBotID(w http.ResponseWriter, r *http.Request) (string, bool) {
	botID := strings.TrimSpace(r.PathValue("peerID"))
	if botID == "" || len(botID) > 256 {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "peer_attrs.invalid",
			Message: "invalid bot id",
			TS:      time.Now().UnixMilli(),
		})
		return "", false
	}
	return botID, true
}

func (s *Server) respondPeerAttrs(w http.ResponseWriter, botID string) {
	attrs := s.peers.LocalAttrsSnapshot()[botID]
	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "peer_attrs.ok",
		Message: "peer attrs fetched",
		Data: map[string]any{
			"botId": botID,
			"attrs": attrs,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handlePeerAttrsGet(w http.ResponseWriter, r *http.Request) {
	botID, ok := peerAttrsBotID(w, r)
	if !ok {
		return
	}
	s.respondPeerAttrs(w, botID)
}

func (s *Server) persistPeerAttrs(w http.ResponseWriter, botID string) bool {
	if s.attrs == nil {
		return true
	}
	st := store.PeerAttrsState{SchemaVersion: 1, Peers: s.peers.LocalAttrsSnapshot()}
	if err := s.attrs.SavePeerAttrsState(st); err != nil {
		slog.Error("persist peer attrs failed; in-memory change kept",
			slog.String("botId", botID),
			slog.String("error", err.Error()),
		)
		respondJSON(w, http.StatusInternalServerError, types.APIResult{
			OK:      false,
			Code:    "peer_attrs.persist_failed",
			Message: "failed to persist peer attrs",
			TS:      time.Now().UnixMilli(),
		})
		return false
	}
	return true
}

func (s *Server) handlePeerAttrsPut(w http.ResponseWriter, r *http.Request) {
	botID, ok := peerAttrsBotID(w, r)
	if !ok {
		return
	}
	var patch peerAttrsPatch
	if err := decodeRequest(r, &patch); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "peer_attrs.invalid",
			Message: "invalid json payload",
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	s.attrsMu.Lock()
	attrs := s.peers.LocalAttrsSnapshot()[botID]
	if patch.Alias != nil {
		attrs.Alias = *patch.Alias
	}
	if patch.Description != nil {
		attrs.Description = *patch.Description
	}
	if patch.Labels != nil {
		attrs.Labels = *patch.Labels
	}
	normalized, err := types.NormalizePeerLocalAttrs(attrs)
	if err != nil {
		s.attrsMu.Unlock()
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "peer_attrs.invalid",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}
	if err := types.ValidateDisplayName(normalized.Alias); err != nil {
		s.attrsMu.Unlock()
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "peer_attrs.invalid",
			Message: err.Error(),
			TS:      time.Now().UnixMilli(),
		})
		return
	}

	s.peers.SetLocalAttrs(botID, normalized)
	persisted := s.persistPeerAttrs(w, botID)
	s.attrsMu.Unlock()
	if !persisted {
		return
	}
	s.respondPeerAttrs(w, botID)
}

func (s *Server) handlePeerAttrsDelete(w http.ResponseWriter, r *http.Request) {
	botID, ok := peerAttrsBotID(w, r)
	if !ok {
		return
	}
	s.attrsMu.Lock()
	s.peers.SetLocalAttrs(botID, types.PeerLocalAttrs{})
	persisted := s.persistPeerAttrs(w, botID)
	s.attrsMu.Unlock()
	if !persisted {
		return
	}
	s.respondPeerAttrs(w, botID)
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

	if err := s.auth.StartChallenge(ctx, req.TargetBot); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "auth.challenge_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetBot": req.TargetBot,
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
			"targetBot": req.TargetBot,
			"status":    "authenticated",
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
				"botId":               s.self.BotID,
				"did":                 s.self.DID,
				"version":             s.version,
				"identityFingerprint": s.self.IdentityFingerprint,
				"trustMode":           s.self.TrustMode,
				"displayName":         s.self.DisplayName,
				"labels":              s.self.Labels,
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

	requestID, err := s.trust.Request(r.Context(), req.TargetBot, req.Reason, req.Capabilities)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "trust.request_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetBot": req.TargetBot,
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
			"targetBot": req.TargetBot,
			"requestId": requestID,
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

	if err := s.trust.Revoke(req.TargetBot, req.Reason); err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "trust.revoke_failed", Message: err.Error(), TS: time.Now().UnixMilli()})
		return
	}

	respondJSON(w, http.StatusOK, types.APIResult{
		OK:      true,
		Code:    "trust.revoked",
		Message: "trust revoked",
		Data: map[string]any{
			"targetBot": req.TargetBot,
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
	if strings.HasPrefix(req.Type, "run.") {
		respondJSON(w, http.StatusForbidden, types.APIResult{OK: false, Code: "control.unauthorized", Message: "execution events may only be published by the execution authority", TS: time.Now().UnixMilli()})
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
		TargetBot:      req.TargetBot,
		Type:           req.Type,
		AgentID:        req.AgentID,
		Message:        message,
		Payload:        payload,
		ExpiresAt:      req.ExpiresAt,
		SessionKey:     req.SessionKey,
		Metadata:       req.Metadata,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		status, code := http.StatusBadRequest, "msg.publish_failed"
		if errors.Is(err, messaging.ErrIdempotencyConflict) {
			status, code = http.StatusConflict, "msg.idempotency_conflict"
		}
		respondJSON(w, status, types.APIResult{
			OK:      false,
			Code:    code,
			Message: err.Error(),
			Data: map[string]any{
				"targetBot":  req.TargetBot,
				"messageId":  result.MessageID,
				"sessionKey": result.SessionKey,
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
			"targetBot":  req.TargetBot,
			"messageId":  result.MessageID,
			"sessionKey": result.SessionKey,
		},
		TS: time.Now().UnixMilli(),
	})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "limit must be between 1 and 1000", TS: time.Now().UnixMilli()})
			return
		}
		limit = value
	}
	if r.URL.Query().Has("after") {
		after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "after must be a nonnegative integer cursor", TS: time.Now().UnixMilli()})
			return
		}
		page := s.messaging.MessagesAfter(after, limit)
		respondJSON(w, http.StatusOK, types.APIResult{OK: true, Code: "msg.replay", Message: "durable messages fetched", Data: map[string]any{"items": page.Items, "cursor": page.Cursor}, TS: time.Now().UnixMilli()})
		return
	}
	page := s.messaging.RecentMessagesPage(limit)
	respondJSON(w, http.StatusOK, types.APIResult{OK: true, Code: "msg.recent", Message: "recent messages fetched", Data: map[string]any{"items": page.Items, "cursor": page.Cursor}, TS: time.Now().UnixMilli()})
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	request, ok := s.messaging.Request(r.PathValue("messageId"))
	if !ok {
		respondJSON(w, http.StatusNotFound, types.APIResult{OK: false, Code: "msg.not_found", Message: "publication not found", TS: time.Now().UnixMilli()})
		return
	}
	respondJSON(w, http.StatusOK, types.APIResult{OK: true, Code: "msg.request", Message: "persisted publication fetched", Data: map[string]any{"message": request.Message, "publication": request.Publication, "status": request.Status, "events": request.Events}, TS: time.Now().UnixMilli()})
}

type transferSendReq struct {
	TargetBot string         `json:"targetBot"`
	FilePath  string         `json:"filePath"`
	MimeType  string         `json:"mimeType,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
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
		TargetBot: req.TargetBot,
		FilePath:  req.FilePath,
		MimeType:  req.MimeType,
		Metadata:  req.Metadata,
	})
	if err != nil {
		respondJSON(w, http.StatusBadRequest, types.APIResult{
			OK:      false,
			Code:    "transfer.send_failed",
			Message: err.Error(),
			Data: map[string]any{
				"targetBot": req.TargetBot,
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
