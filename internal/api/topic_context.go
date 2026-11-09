package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

// handleTopicContext reads only the immutable public facts authorized for this
// active native invocation. Bot selection/authentication is enforced by daemon.
func (s *Server) handleTopicContext(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, runID := query.Get("from"), query.Get("runId")
	view := query.Get("view")
	if view != "" && view != "required" && view != "history" {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "view must be required or history", TS: time.Now().UnixMilli()})
		return
	}
	if from == "" || runID == "" {
		respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "from and runId are required", TS: time.Now().UnixMilli()})
		return
	}
	var after uint64
	if query.Has("after") {
		value, err := strconv.ParseUint(query.Get("after"), 10, 64)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: "after must be a nonnegative revision", TS: time.Now().UnixMilli()})
			return
		}
		after = value
	}
	limit := 32
	if query.Has("limit") {
		value, err := strconv.Atoi(query.Get("limit"))
		if err != nil || value < 1 || value > protocol.TopicReaderMaxEvents {
			respondJSON(w, http.StatusBadRequest, types.APIResult{OK: false, Code: "invalid_argument", Message: fmt.Sprintf("limit must be between 1 and %d", protocol.TopicReaderMaxEvents), TS: time.Now().UnixMilli()})
			return
		}
		limit = value
	}
	if s.messaging == nil {
		respondJSON(w, http.StatusServiceUnavailable, types.APIResult{OK: false, Code: "context.unavailable", Message: "context reader is unavailable", TS: time.Now().UnixMilli()})
		return
	}
	reader := s.messaging.ReadTopicContext
	if view == "history" {
		reader = s.messaging.ReadTopicHistory
	}
	page, err := reader(from, r.PathValue("messageId"), runID, r.Header.Get("X-ClawSynapse-Context-Token"), after, limit)
	if err != nil {
		// Do not disclose whether another sender/request/run owns any context.
		respondJSON(w, http.StatusForbidden, types.APIResult{OK: false, Code: "context.denied", Message: "context is not available for this active request", TS: time.Now().UnixMilli()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, types.APIResult{OK: true, Code: "context.page", Message: "fixed public context fetched", Data: map[string]any{"page": page}, TS: time.Now().UnixMilli()})
}
