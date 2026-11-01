package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/messaging"
	"clawsynapse/pkg/types"
)

func TestMessagingRejectsMalformedCursorAndUnauthorisedRunPublication(t *testing.T) {
	srv := &Server{}
	for _, query := range []string{"?after=-1", "?after=wrong", "?after=", "?limit=0", "?limit=1001", "?limit=wrong"} {
		rec := httptest.NewRecorder()
		srv.handleMessages(rec, httptest.NewRequest(http.MethodGet, "/messages"+query, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed cursor accepted: %q -> %d", query, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	srv.handlePublish(rec, httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(`{"targetBot":"remote","type":"run.completed","message":"forged"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("caller published execution authority event: %d", rec.Code)
	}
}

func TestMessagingConflictAndReconciliationExposeOriginalDurableIntent(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Create(filepath.Join(dir, "identity.key"), filepath.Join(dir, "identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{BotID: "remote", AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted})
	svc := messaging.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), peers, nil, "local", id, "explicit", nil)
	if err := svc.SetStatePath(filepath.Join(dir, "messages.jsonl")); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	srv := &Server{messaging: svc}
	publish := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handlePublish(rec, httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(body)))
		return rec
	}
	// Without a broker the call must not claim durable network acceptance, but
	// its persistent outbox intent remains reconcilable under the same key.
	first := publish(`{"targetBot":"remote","message":"first","idempotencyKey":"stable-key"}`)
	if first.Code == http.StatusOK {
		t.Fatal("Core/no-broker publication claimed durable acceptance")
	}
	var result types.APIResult
	if err := json.Unmarshal(first.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	messageID, _ := result.Data["messageId"].(string)
	if messageID == "" {
		t.Fatal("durable pending publication cannot be reconciled")
	}
	conflict := publish(`{"targetBot":"remote","message":"different","idempotencyKey":"stable-key"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict HTTP status = %d", conflict.Code)
	}
	if err := json.Unmarshal(conflict.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "msg.idempotency_conflict" {
		t.Fatalf("conflict code = %q", result.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/requests/"+messageID, nil)
	request.SetPathValue("messageId", messageID)
	rec := httptest.NewRecorder()
	srv.handleRequest(rec, request)
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || result.Data["publication"] != "pending" || result.Data["status"] != "sending" {
		t.Fatalf("wrong reconciliation state: %s", rec.Body.String())
	}
	request.SetPathValue("messageId", "unknown")
	rec = httptest.NewRecorder()
	srv.handleRequest(rec, request)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown request = %d", rec.Code)
	}
}
