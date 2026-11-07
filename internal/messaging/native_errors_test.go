package messaging

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/protocol"
)

func TestRunFailureSafeDiagnosticProjection(t *testing.T) {
	for _, tc := range []struct {
		name, code  string
		partial     bool
		wantMessage bool
	}{
		{"invalid model", "invalid_model", false, true},
		{"quota", "provider_quota", false, true},
		{"unknown code", "unclassified", false, false},
		{"output preserves uncertainty", "invalid_model", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peers := discovery.NewRegistry()
			sender, senderID := durableIdentity(t, peers)
			receiver, receiverID := durableIdentity(t, peers)
			path := filepath.Join(t.TempDir(), "messages.jsonl")
			svc := durableBot(t, nil, peers, receiver, receiverID, path)
			svc.SetRole(config.RoleAgent)
			calls := 0
			svc.SetMessageHandler(MessageHandlerFunc(func(msg IncomingMessage) (HandlerResult, error) {
				calls++
				if tc.partial {
					if err := msg.Emit(adapter.OutputEvent{Text: "partial"}); err != nil {
						return HandlerResult{}, err
					}
				}
				return HandlerResult{}, &adapter.ExecutionError{Code: tc.code, Stage: "validation", Err: errors.New("PRIVATE_KEY PRIVATE_PROMPT")}
			}))
			now := time.Now().UnixMilli()
			env := protocol.MessageEnvelope{ID: "request", Type: "task.request", From: sender, To: receiver, SessionKey: "topic", Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
			encoded := signedConsoleDelivery(t, svc, senderID, env)
			if !svc.deliver(*svc.journal.work[messageKey(env)]) {
				t.Fatal("failure was not committed")
			}
			var terminal protocol.MessageEnvelope
			for _, publication := range svc.journal.publications {
				if publication.Message.Type == "run.failed" {
					terminal = publication.Message
				}
			}
			if terminal.Payload.Code != tc.code || terminal.Payload.Stage != "validation" || terminal.Payload.Indeterminate != tc.partial || (terminal.Payload.Message != "") != tc.wantMessage || terminal.RequestID != env.ID || terminal.RunID == "" {
				t.Fatalf("incorrect failure projection: %#v", terminal)
			}
			data, err := json.Marshal(terminal)
			if err != nil || strings.Contains(string(data), "PRIVATE_") || terminal.Payload.Text != "" {
				t.Fatalf("raw diagnostic leaked: %s / %v", data, err)
			}
			if !svc.handleInbox(inboxSubject(receiver), encoded) || calls != 1 || svc.journal.work[messageKey(env)].State != "done" {
				t.Fatal("failed execution was retried")
			}
			console := durableBot(t, nil, peers, sender, senderID, filepath.Join(t.TempDir(), "console.jsonl"))
			console.SetRole(config.RoleConsole)
			if !console.handleInbox(inboxSubject(sender), data) {
				t.Fatal("failure feedback was not acknowledged")
			}
			stored := console.RecentMessages(1)
			if len(stored) != 1 || stored[0].Payload.Message != terminal.Payload.Message {
				t.Fatalf("signed failure feedback lost its message: %#v", stored)
			}
			svc.Close()
			restored := durableBot(t, nil, peers, receiver, receiverID, path)
			pub := restored.journal.publications[terminal.ID]
			if pub == nil || pub.Message.Payload.Message != terminal.Payload.Message || pub.Message.Payload.Indeterminate != tc.partial {
				t.Fatal("durable outbox lost diagnostic or certainty")
			}
		})
	}
}

func TestFailureMessageValidation(t *testing.T) {
	now := time.Now().UnixMilli()
	base := protocol.MessageEnvelope{ID: "event", Type: "run.failed", From: "sender", To: "receiver", RequestID: "request", RunID: "run", Seq: 1, Ts: now, ExpiresAt: now + 60000, ProtocolVersion: protocol.WireVersion}
	for _, tc := range []struct {
		name, kind, message string
		valid               bool
	}{
		{"old event", "run.failed", "", true},
		{"safe message", "run.failed", "The selected model is invalid.", true},
		{"wrong event", "run.completed", "failure", false},
		{"oversized", "run.failed", strings.Repeat("x", 513), false},
		{"invalid UTF-8", "run.failed", string([]byte{0xff}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := base
			env.Type, env.Payload.Message = tc.kind, tc.message
			if err := validateEnvelope(env, now); (err == nil) != tc.valid {
				t.Fatalf("message validity = %v, want %v", err, tc.valid)
			}
		})
	}
}
