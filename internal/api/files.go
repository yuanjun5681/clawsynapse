package api

import (
	"io"
	"net/http"
	"time"

	"clawsynapse/internal/transfer"
	"clawsynapse/pkg/types"
)

func (s *Server) handleTrustSnapshot(w http.ResponseWriter, r *http.Request) {
	st, err := s.attrs.LoadTrustState()
	if err != nil {
		respondError(w, err)
		return
	}
	type peer struct {
		BotID  string `json:"botId"`
		AtMs   int64  `json:"atMs"`
		Reason string `json:"reason,omitempty"`
	}
	trusted := make([]peer, 0, len(st.Trusted))
	rejected := make([]peer, 0, len(st.Rejected))
	revoked := make([]peer, 0, len(st.Revoked))
	for _, p := range st.Trusted {
		trusted = append(trusted, peer{p.BotID, p.AtMs, p.Reason})
	}
	for _, p := range st.Rejected {
		rejected = append(rejected, peer{p.BotID, p.AtMs, p.Reason})
	}
	for _, p := range st.Revoked {
		revoked = append(revoked, peer{p.BotID, p.AtMs, p.Reason})
	}
	success(w, 200, "trust.snapshot", map[string]any{"trusted": trusted, "rejected": rejected, "revoked": revoked, "pending": st.Pending})
}

func (s *Server) handleTransferFile(w http.ResponseWriter, r *http.Request) {
	if s.transfer == nil {
		respondError(w, Failure(404, "transfer.not_found", "transfer not found"))
		return
	}
	id := r.PathValue("transferId")
	if _, ok := s.transfer.GetTransfer(id); !ok {
		respondError(w, Failure(404, "transfer.not_found", "transfer not found"))
		return
	}
	f, err := s.transfer.OpenFile(id)
	if err != nil {
		respondError(w, Failure(500, "transfer.file_unavailable", "authorized file is missing or corrupt"))
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}

func (s *Server) handleTransferForward(w http.ResponseWriter, r *http.Request) {
	if s.transfer == nil || !s.transfer.Enabled() {
		respondError(w, Failure(409, "bot.must_start", "Bot must be running"))
		return
	}
	var req struct {
		TargetBot string         `json:"targetBot"`
		MimeType  string         `json:"mimeType,omitempty"`
		Metadata  map[string]any `json:"metadata,omitempty"`
	}
	if err := decodeRequest(r, &req); err != nil {
		respondError(w, Failure(400, "invalid_argument", err.Error()))
		return
	}
	result, err := s.transfer.ForwardFile(r.PathValue("transferId"), transfer.SendFileRequest{TargetBot: req.TargetBot, MimeType: req.MimeType, Metadata: req.Metadata})
	if err != nil {
		respondError(w, Failure(400, "transfer.forward_failed", err.Error()))
		return
	}
	respondJSON(w, 200, types.APIResult{OK: true, Code: "transfer.sent", Data: map[string]any{"transferId": result.TransferID, "size": result.Size, "checksum": result.Checksum, "bucket": result.Bucket}, TS: time.Now().UnixMilli()})
}

// Import is routed only after daemon-admin authorization, never a Bot credential.
func (s *Server) handleTransferImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FilePath string `json:"filePath"`
	}
	if err := decodeRequest(r, &req); err != nil {
		respondError(w, Failure(400, "invalid_argument", err.Error()))
		return
	}
	info, err := s.transfer.ImportFile(req.FilePath)
	if err != nil {
		respondError(w, Failure(400, "transfer.import_failed", err.Error()))
		return
	}
	success(w, 201, "transfer.imported", map[string]any{"transfer": info})
}
