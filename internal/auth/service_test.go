package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"clawsynapse/internal/discovery"
	"clawsynapse/internal/identity"
	"clawsynapse/pkg/types"
)

func TestChallengeValidationUsesBotTerminology(t *testing.T) {
	svc := &Service{}
	if err := svc.StartChallenge(context.Background(), ""); err == nil || err.Error() != "targetBot is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleChallengeResponseRejectsUnexpectedSender(t *testing.T) {
	peers := discovery.NewRegistry()
	self := mustIdentity(t, "self")
	target := mustIdentity(t, "target")
	attacker := mustIdentity(t, "attacker")

	peers.Upsert(types.Peer{
		BotID:       "bot-beta",
		AuthStatus:  types.AuthSeen,
		TrustStatus: types.TrustNone,
		Metadata: map[string]any{
			"publicKey": base64.RawURLEncoding.EncodeToString(target.PublicKey),
		},
	})
	peers.Upsert(types.Peer{
		BotID:       "bot-gamma",
		AuthStatus:  types.AuthSeen,
		TrustStatus: types.TrustNone,
		Metadata: map[string]any{
			"publicKey": base64.RawURLEncoding.EncodeToString(attacker.PublicKey),
		},
	})

	svc := NewService(slog.Default(), peers, nil, "bot-alpha", self, nil, "tofu")
	resultCh := make(chan error, 1)
	svc.pending["req-1"] = &pendingChallenge{
		requestID: "req-1",
		nonce:     "nonce-1",
		target:    "bot-beta",
		requestTs: time.Now().UnixMilli(),
		resultCh:  resultCh,
	}

	resp := map[string]any{
		"messageId":    "resp-1",
		"messageType":  "auth.challenge.response",
		"from":         "bot-gamma",
		"to":           "bot-alpha",
		"publicKey":    base64.RawURLEncoding.EncodeToString(attacker.PublicKey),
		"nonce":        "nonce-2",
		"challengeRef": "req-1",
		"proof":        "invalid-proof",
		"ts":           time.Now().UnixMilli(),
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	svc.handleChallengeResponse("clawsynapse.auth.bot-alpha.challenge.response", b)

	if _, ok := svc.pending["req-1"]; ok {
		t.Fatal("pending challenge should be cleared on sender mismatch")
	}

	select {
	case err := <-resultCh:
		if err == nil || err.Error() != "challenge response sender mismatch" {
			t.Fatalf("unexpected challenge result error: %v", err)
		}
	default:
		t.Fatal("expected challenge result error")
	}
}

func TestHandleChallengeAckRejectsInvalidProof(t *testing.T) {
	peers := discovery.NewRegistry()
	self := mustIdentity(t, "self")
	peer := mustIdentity(t, "peer")

	peers.Upsert(types.Peer{
		BotID:       "bot-beta",
		AuthStatus:  types.AuthSeen,
		TrustStatus: types.TrustNone,
		Metadata: map[string]any{
			"publicKey": base64.RawURLEncoding.EncodeToString(peer.PublicKey),
		},
	})

	svc := NewService(slog.Default(), peers, nil, "bot-alpha", self, nil, "tofu")
	svc.savePendingAck("resp-1", pendingAck{
		challengeRef: "resp-1",
		peer:         "bot-beta",
		nonce:        "nonce-2",
		responseTs:   time.Now().UnixMilli(),
		createdAt:    time.Now(),
	})

	ack := map[string]any{
		"messageId":    "ack-1",
		"messageType":  "auth.challenge.ack",
		"from":         "bot-beta",
		"to":           "bot-alpha",
		"challengeRef": "resp-1",
		"proof":        "invalid-proof",
		"ts":           time.Now().UnixMilli(),
	}
	b, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}

	svc.handleChallengeAck("", b)

	p, ok := peers.Get("bot-beta")
	if !ok {
		t.Fatal("peer should exist")
	}
	if p.AuthStatus == types.AuthAuthenticated {
		t.Fatal("invalid ack proof should not authenticate peer")
	}
}

func TestHandleChallengeAckAuthenticatesPeerWithValidProof(t *testing.T) {
	peers := discovery.NewRegistry()
	self := mustIdentity(t, "self")
	peer := mustIdentity(t, "peer")

	peers.Upsert(types.Peer{
		BotID:       "bot-beta",
		AuthStatus:  types.AuthSeen,
		TrustStatus: types.TrustNone,
		Metadata: map[string]any{
			"publicKey": base64.RawURLEncoding.EncodeToString(peer.PublicKey),
		},
	})

	svc := NewService(slog.Default(), peers, nil, "bot-alpha", self, nil, "tofu")
	responseTS := time.Now().UnixMilli()
	svc.savePendingAck("resp-1", pendingAck{
		challengeRef: "resp-1",
		peer:         "bot-beta",
		nonce:        "nonce-2",
		responseTs:   responseTS,
		createdAt:    time.Now(),
	})

	proof := identity.Sign(peer.PrivateKey, []byte("nonce-2|bot-alpha|bot-beta|"+fmt.Sprintf("%d", responseTS)))
	ack := map[string]any{
		"messageId":    "ack-1",
		"messageType":  "auth.challenge.ack",
		"from":         "bot-beta",
		"to":           "bot-alpha",
		"challengeRef": "resp-1",
		"proof":        proof,
		"ts":           time.Now().UnixMilli(),
	}
	b, err := json.Marshal(ack)
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}

	svc.handleChallengeAck("", b)

	p, ok := peers.Get("bot-beta")
	if !ok {
		t.Fatal("peer should exist")
	}
	if p.AuthStatus != types.AuthAuthenticated {
		t.Fatalf("expected authenticated status, got %s", p.AuthStatus)
	}
}

func mustIdentity(t *testing.T, name string) *identity.Identity {
	t.Helper()
	base := t.TempDir()
	id, err := identity.Create(filepath.Join(base, name+".key"), filepath.Join(base, name+".pub"))
	if err != nil {
		t.Fatalf("load identity %s: %v", name, err)
	}
	return id
}
