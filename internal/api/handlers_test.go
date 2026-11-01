package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

type stubAgentAdapter struct {
	status *adapter.AgentStatus
	err    error
}

func (s stubAgentAdapter) DeliverMessage(_ context.Context, _ adapter.DeliverMessageRequest) (*adapter.DeliverMessageResult, error) {
	return nil, nil
}

func (s stubAgentAdapter) GetStatus(_ context.Context) (*adapter.AgentStatus, error) {
	return s.status, s.err
}

func TestHandleHealthIncludesAdapterStatus(t *testing.T) {
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{BotID: "n1-localbotid0000000000000000000000"})
	peers.Upsert(types.Peer{BotID: "n1-remotepeer0000000000000000000000"})

	srv := &Server{
		peers:       peers,
		adapter:     stubAgentAdapter{status: &adapter.AgentStatus{Healthy: true}},
		adapterName: "openclaw",
		version:     "v0.3.2",
		self: SelfInfo{
			BotID:               "n1-localbotid0000000000000000000000",
			DID:                 "did:key:z6MkexampleLocalDid",
			IdentityFingerprint: "sha256:1234abcd5678ef90",
			TrustMode:           "tofu",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var result types.APIResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	adapterData, ok := result.Data["adapter"].(map[string]any)
	if !ok {
		t.Fatalf("expected adapter data map, got %#v", result.Data["adapter"])
	}
	selfData, ok := result.Data["self"].(map[string]any)
	if !ok {
		t.Fatalf("expected self data map, got %#v", result.Data["self"])
	}
	if selfData["botId"] != "n1-localbotid0000000000000000000000" {
		t.Fatalf("expected self botId, got %#v", selfData["botId"])
	}
	if selfData["did"] != "did:key:z6MkexampleLocalDid" {
		t.Fatalf("expected self did, got %#v", selfData["did"])
	}
	if selfData["version"] != "v0.3.2" {
		t.Fatalf("expected self version, got %#v", selfData["version"])
	}
	if selfData["identityFingerprint"] != "sha256:1234abcd5678ef90" {
		t.Fatalf("expected self identityFingerprint, got %#v", selfData["identityFingerprint"])
	}
	if selfData["trustMode"] != "tofu" {
		t.Fatalf("expected self trustMode tofu, got %#v", selfData["trustMode"])
	}
	if result.Data["peersCount"] != float64(1) {
		t.Fatalf("expected peersCount 1, got %#v", result.Data["peersCount"])
	}
	if adapterData["name"] != "openclaw" {
		t.Fatalf("expected adapter name openclaw, got %#v", adapterData["name"])
	}
	if adapterData["healthy"] != true {
		t.Fatalf("expected adapter healthy true, got %#v", adapterData["healthy"])
	}
	if _, exists := adapterData["error"]; exists {
		t.Fatalf("expected no adapter error, got %#v", adapterData["error"])
	}
}

func TestHandlePeersExcludesSelfBot(t *testing.T) {
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{BotID: "n1-localbotid0000000000000000000000"})
	peers.Upsert(types.Peer{BotID: "n1-remotepeer0000000000000000000000", AuthStatus: types.AuthAuthenticated, TrustStatus: types.TrustTrusted})

	srv := &Server{
		peers: peers,
		self: SelfInfo{
			BotID: "n1-localbotid0000000000000000000000",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/peers", nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var result types.APIResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	items, ok := result.Data["items"].([]any)
	if !ok {
		t.Fatalf("expected items slice, got %#v", result.Data["items"])
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 peer, got %#v", result.Data["items"])
	}
	peer, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected peer item map, got %#v", items[0])
	}
	if peer["botId"] != "n1-remotepeer0000000000000000000000" {
		t.Fatalf("expected remote peer only, got %#v", peer["botId"])
	}
}

func TestHandleHealthIncludesAdapterError(t *testing.T) {
	srv := &Server{
		peers:       discovery.NewRegistry(),
		adapter:     stubAgentAdapter{status: &adapter.AgentStatus{Healthy: false}, err: errors.New("openclaw unavailable")},
		adapterName: "openclaw",
		version:     "v0.3.2",
		self: SelfInfo{
			BotID:               "n1-localbotid0000000000000000000000",
			DID:                 "did:key:z6MkexampleLocalDid",
			IdentityFingerprint: "sha256:1234abcd5678ef90",
			TrustMode:           "explicit",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.handleHealth(rec, req)

	var result types.APIResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	adapterData, ok := result.Data["adapter"].(map[string]any)
	if !ok {
		t.Fatalf("expected adapter data map, got %#v", result.Data["adapter"])
	}
	if adapterData["healthy"] != false {
		t.Fatalf("expected adapter healthy false, got %#v", adapterData["healthy"])
	}
	if adapterData["error"] != "openclaw unavailable" {
		t.Fatalf("expected adapter error, got %#v", adapterData["error"])
	}
}

func peersFilterServer(t *testing.T) *Server {
	t.Helper()
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{BotID: "n1-localbotid0000000000000000000000"})
	peers.Upsert(types.Peer{
		BotID:       "n1-scalarpeer0000000000000000000000000",
		DisplayName: "Scalar Bot",
		Labels:      types.Labels{"env": types.LabelString("production")},
	})
	peers.Upsert(types.Peer{
		BotID:       "n1-setpeer0000000000000000000000000000",
		DisplayName: "Set Bot",
		Labels:      types.Labels{"caps": types.LabelSet([]string{"production", "chat"})},
	})
	return &Server{
		peers: peers,
		self:  SelfInfo{BotID: "n1-localbotid0000000000000000000000"},
	}
}

func filteredPeerIDs(t *testing.T, srv *Server, target string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.handlePeers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	var result types.APIResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, ok := result.Data["items"].([]any)
	if !ok {
		t.Fatalf("expected items slice, got %#v", result.Data["items"])
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.(map[string]any)["botId"].(string))
	}
	return ids
}

func TestHandlePeersLabelExactDoesNotMatchSet(t *testing.T) {
	srv := peersFilterServer(t)
	ids := filteredPeerIDs(t, srv, "/peers?label=env%3Dproduction")
	if len(ids) != 1 || ids[0] != "n1-scalarpeer0000000000000000000000000" {
		t.Fatalf("expected scalar peer only, got %v", ids)
	}
	// exact match against a set member must not hit
	ids = filteredPeerIDs(t, srv, "/peers?label=caps%3Dproduction")
	if len(ids) != 0 {
		t.Fatalf("expected no peers for exact match on set, got %v", ids)
	}
}

func TestHandlePeersLabelHasDoesNotMatchScalar(t *testing.T) {
	srv := peersFilterServer(t)
	ids := filteredPeerIDs(t, srv, "/peers?labelHas=caps%3Dproduction")
	if len(ids) != 1 || ids[0] != "n1-setpeer0000000000000000000000000000" {
		t.Fatalf("expected set peer only, got %v", ids)
	}
	// member match against a scalar must not hit
	ids = filteredPeerIDs(t, srv, "/peers?labelHas=env%3Dproduction")
	if len(ids) != 0 {
		t.Fatalf("expected no peers for member match on scalar, got %v", ids)
	}
}

func TestHandlePeersNameFilterMatchesDisplayNameAndAlias(t *testing.T) {
	srv := peersFilterServer(t)
	srv.peers.SetLocalAttrs("n1-setpeer0000000000000000000000000000", types.PeerLocalAttrs{Alias: "Build Runner"})
	ids := filteredPeerIDs(t, srv, "/peers?name=scalar")
	if len(ids) != 1 || ids[0] != "n1-scalarpeer0000000000000000000000000" {
		t.Fatalf("expected display name match, got %v", ids)
	}
	ids = filteredPeerIDs(t, srv, "/peers?name=build")
	if len(ids) != 1 || ids[0] != "n1-setpeer0000000000000000000000000000" {
		t.Fatalf("expected alias match, got %v", ids)
	}
}

func TestHandlePeersMalformedFilterRejected(t *testing.T) {
	srv := peersFilterServer(t)
	for _, target := range []string{"/peers?label=noequals", "/peers?labelHas=%3Dvalue", "/peers?label="} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		srv.handlePeers(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected status 400, got %d", target, rec.Code)
		}
		var result types.APIResult
		if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if result.Code != "peers.invalid_filter" {
			t.Fatalf("%s: expected peers.invalid_filter, got %q", target, result.Code)
		}
	}
}

func peerAttrsServer(t *testing.T) *Server {
	t.Helper()
	peers := discovery.NewRegistry()
	fs := store.NewFSStore(t.TempDir())
	return &Server{
		peers: peers,
		attrs: fs,
		self:  SelfInfo{BotID: "n1-localbotid0000000000000000000000"},
	}
}

func peerAttrsResult(t *testing.T, rec *httptest.ResponseRecorder) types.APIResult {
	t.Helper()
	var result types.APIResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func TestPeerAttrsValidationUsesBotTerminology(t *testing.T) {
	rec := httptest.NewRecorder()
	peerAttrsBotID(rec, httptest.NewRequest(http.MethodGet, "/peers//attrs", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if result := peerAttrsResult(t, rec); result.Message != "invalid bot id" {
		t.Fatalf("message = %q", result.Message)
	}
}

func peerAttrsData(t *testing.T, result types.APIResult) map[string]any {
	t.Helper()
	attrs, ok := result.Data["attrs"].(map[string]any)
	if !ok {
		t.Fatalf("expected attrs map, got %#v", result.Data["attrs"])
	}
	return attrs
}

func TestHandlePeerAttrsPutGetDeleteRoundTrip(t *testing.T) {
	srv := peerAttrsServer(t)
	botID := "n1-remotepeer0000000000000000000000"

	// GET on unknown Bot returns empty object.
	req := httptest.NewRequest(http.MethodGet, "/peers/"+botID+"/attrs", nil)
	req.SetPathValue("peerID", botID)
	rec := httptest.NewRecorder()
	srv.handlePeerAttrsGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if result := peerAttrsResult(t, rec); result.Code != "peer_attrs.ok" || len(peerAttrsData(t, result)) != 0 {
		t.Fatalf("expected empty attrs, got %#v", result)
	}

	// PUT sets alias + labels.
	put := httptest.NewRequest(http.MethodPut, "/peers/"+botID+"/attrs", strings.NewReader(`{"alias":"Buddy","labels":{"env":"production","caps":["chat"]}}`))
	put.SetPathValue("peerID", botID)
	rec = httptest.NewRecorder()
	srv.handlePeerAttrsPut(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	attrs := peerAttrsData(t, peerAttrsResult(t, rec))
	if attrs["alias"] != "Buddy" {
		t.Fatalf("expected alias Buddy, got %#v", attrs)
	}

	// PUT with omitted keys leaves existing values untouched.
	put = httptest.NewRequest(http.MethodPut, "/peers/"+botID+"/attrs", strings.NewReader(`{"description":"note"}`))
	put.SetPathValue("peerID", botID)
	rec = httptest.NewRecorder()
	srv.handlePeerAttrsPut(rec, put)
	attrs = peerAttrsData(t, peerAttrsResult(t, rec))
	if attrs["alias"] != "Buddy" || attrs["description"] != "note" {
		t.Fatalf("expected alias preserved and description set, got %#v", attrs)
	}

	// Explicit empty clears only that key.
	put = httptest.NewRequest(http.MethodPut, "/peers/"+botID+"/attrs", strings.NewReader(`{"description":""}`))
	put.SetPathValue("peerID", botID)
	rec = httptest.NewRecorder()
	srv.handlePeerAttrsPut(rec, put)
	attrs = peerAttrsData(t, peerAttrsResult(t, rec))
	if attrs["alias"] != "Buddy" {
		t.Fatalf("expected alias preserved, got %#v", attrs)
	}
	if _, exists := attrs["description"]; exists {
		t.Fatalf("expected description cleared, got %#v", attrs)
	}

	// Persisted state survives into a fresh registry.
	st, err := srv.attrs.LoadPeerAttrsState()
	if err != nil {
		t.Fatalf("load persisted attrs: %v", err)
	}
	if st.Peers[botID].Alias != "Buddy" {
		t.Fatalf("expected persisted alias Buddy, got %#v", st.Peers[botID])
	}

	// DELETE clears.
	del := httptest.NewRequest(http.MethodDelete, "/peers/"+botID+"/attrs", nil)
	del.SetPathValue("peerID", botID)
	rec = httptest.NewRecorder()
	srv.handlePeerAttrsDelete(rec, del)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if result := peerAttrsResult(t, rec); len(peerAttrsData(t, result)) != 0 {
		t.Fatalf("expected empty attrs after delete, got %#v", result)
	}
	if got := srv.peers.LocalAttrsSnapshot()[botID]; !got.IsZero() {
		t.Fatalf("expected registry attrs cleared, got %#v", got)
	}
}

func TestHandlePeerAttrsPutValidationFailure(t *testing.T) {
	srv := peerAttrsServer(t)
	botID := "n1-remotepeer0000000000000000000000"
	for _, body := range []string{
		`{"alias":"bad
name"}`,
		`{"labels":{"caps":"not-a-set"}}`,
	} {
		put := httptest.NewRequest(http.MethodPut, "/peers/"+botID+"/attrs", strings.NewReader(body))
		put.SetPathValue("peerID", botID)
		rec := httptest.NewRecorder()
		srv.handlePeerAttrsPut(rec, put)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected status 400, got %d", body, rec.Code)
		}
		if result := peerAttrsResult(t, rec); result.Code != "peer_attrs.invalid" {
			t.Fatalf("%s: expected peer_attrs.invalid, got %q", body, result.Code)
		}
	}
}
