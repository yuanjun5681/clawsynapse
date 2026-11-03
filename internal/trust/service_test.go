package trust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
	"clawsynapse/pkg/types"
)

type publishedMessage struct {
	subject string
	payload any
}

func TestTrustValidationUsesBotTerminology(t *testing.T) {
	svc := &Service{}
	if _, err := svc.Request(context.Background(), "", "", nil); err == nil || err.Error() != "targetBot is required" {
		t.Fatalf("request error = %v", err)
	}
	if err := svc.Revoke("", ""); err == nil || err.Error() != "targetBot is required" {
		t.Fatalf("revoke error = %v", err)
	}
}

func TestHandleTrustRequestKeepsPendingWhenAutoApproveDisabled(t *testing.T) {
	svc, peers, peerID := newTrustTestService(t, false)
	var published []publishedMessage
	svc.publishJSON = func(subject string, payload any) error {
		published = append(published, publishedMessage{subject: subject, payload: payload})
		return nil
	}

	req := signedTrustRequest(t, svc, peerID, "bot-alpha", "req-1")
	svc.handleTrustRequest("clawsynapse.trust.bot-alpha.request", mustJSON(t, req))

	if len(published) != 0 {
		t.Fatalf("expected no trust response to be published, got %d", len(published))
	}
	if len(svc.state.Pending) != 1 {
		t.Fatalf("expected one pending request, got %+v", svc.state.Pending)
	}
	if len(svc.state.Trusted) != 0 {
		t.Fatalf("expected no trusted peers, got %+v", svc.state.Trusted)
	}
	peer, ok := peers.Get(peerID.botID)
	if !ok {
		t.Fatal("expected peer to exist")
	}
	if peer.TrustStatus != types.TrustPending {
		t.Fatalf("expected trust pending, got %s", peer.TrustStatus)
	}
}

func TestHandleTrustRequestAutoApprovesValidRequest(t *testing.T) {
	svc, peers, peerID := newTrustTestService(t, true)
	var published []publishedMessage
	svc.publishJSON = func(subject string, payload any) error {
		published = append(published, publishedMessage{subject: subject, payload: payload})
		return nil
	}

	req := signedTrustRequest(t, svc, peerID, "bot-alpha", "req-1")
	svc.handleTrustRequest("clawsynapse.trust.bot-alpha.request", mustJSON(t, req))

	if len(svc.state.Pending) != 0 {
		t.Fatalf("expected pending request to be cleared, got %+v", svc.state.Pending)
	}
	if len(svc.state.Trusted) != 1 || svc.state.Trusted[0].BotID != peerID.botID {
		t.Fatalf("expected peer to be trusted, got %+v", svc.state.Trusted)
	}
	if svc.state.Trusted[0].Reason != "auto-approved trust request" {
		t.Fatalf("unexpected trust reason: %q", svc.state.Trusted[0].Reason)
	}

	peer, ok := peers.Get(peerID.botID)
	if !ok {
		t.Fatal("expected peer to exist")
	}
	if peer.TrustStatus != types.TrustTrusted {
		t.Fatalf("expected trust trusted, got %s", peer.TrustStatus)
	}

	if len(published) != 1 {
		t.Fatalf("expected one trust response to be published, got %d", len(published))
	}
	if published[0].subject != "clawsynapse.trust."+peerID.botID+".response" {
		t.Fatalf("unexpected publish subject: %s", published[0].subject)
	}
	resp, ok := published[0].payload.(protocol.TrustResponse)
	if !ok {
		t.Fatalf("expected trust response payload, got %T", published[0].payload)
	}
	if resp.Decision != "approve" || resp.RequestID != "req-1" || resp.To != peerID.botID {
		t.Fatalf("unexpected trust response: %+v", resp)
	}
	if !identity.Verify(svc.identity.PublicKey, []byte(svc.trustResponseSignatureInput(resp)), resp.Signature) {
		t.Fatal("expected trust response signature to verify")
	}
}

type trustTestIdentity struct {
	botID string
	id    *identity.Identity
}

func newTrustTestService(t *testing.T, trustAutoApprove bool) (*Service, *discovery.Registry, trustTestIdentity) {
	t.Helper()

	self := newIdentity(t)
	peerID := trustTestIdentity{botID: "bot-beta", id: newIdentity(t)}
	peers := discovery.NewRegistry()
	peers.Upsert(types.Peer{
		BotID:       peerID.botID,
		AuthStatus:  types.AuthAuthenticated,
		TrustStatus: types.TrustNone,
		Metadata: map[string]any{
			"publicKey": base64.RawURLEncoding.EncodeToString(peerID.id.PublicKey),
		},
	})

	fs := store.NewFSStore(t.TempDir())
	if err := fs.EnsureLayout(); err != nil {
		t.Fatalf("ensure store layout: %v", err)
	}
	svc, err := NewService(slog.Default(), peers, nil, fs, "bot-alpha", self, trustAutoApprove)
	if err != nil {
		t.Fatalf("new trust service: %v", err)
	}
	return svc, peers, peerID
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	return &identity.Identity{PrivateKey: priv, PublicKey: pub}
}

func signedTrustRequest(t *testing.T, svc *Service, peerID trustTestIdentity, to, requestID string) protocol.TrustRequest {
	t.Helper()
	req := protocol.TrustRequest{
		MessageID:   "msg-" + requestID,
		MessageType: "trust.request",
		From:        peerID.botID,
		To:          to,
		RequestID:   requestID,
		Reason:      "test trust",
		Ts:          time.Now().UnixMilli(),
	}
	req.Signature = identity.Sign(peerID.id.PrivateKey, []byte(svc.trustRequestSignatureInput(req)))
	return req
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}
