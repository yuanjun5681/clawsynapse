package adapter

import (
	"context"
	"strings"
	"testing"

	"clawsynapse/internal/store"
)

func TestOpenCodeConfirmsCurrentPersistedTurnWithoutStreamTerminal(t *testing.T) {
	for _, mode := range []string{"confirmed-turn", "stale-turn", "unfinished-turn", "failed-turn"} {
		t.Run(mode, func(t *testing.T) {
			fs := store.NewFSStore(privateTestDir(t))
			a, err := NewOpenCodeAdapter(testOpenCodeConfig(t, OpenCodeConfig{SessionStore: fs}))
			if err != nil {
				t.Fatal(err)
			}
			a.command = helperCommand("opencode", mode, "", "")
			var answer strings.Builder
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", SessionKey: "conversation", Emit: func(e OutputEvent) error { answer.WriteString(e.Text); return nil }})
			if mode != "confirmed-turn" {
				failure := assertExecutionCode(t, err, "incomplete_output")
				if !failure.Indeterminate {
					t.Fatal("unconfirmed turn lost uncertain semantics")
				}
				if _, found, err := fs.LoadSessionState("opencode", a.scopedSessionKey("conversation")); err != nil || found {
					t.Fatal("unconfirmed turn was persisted as completed")
				}
				return
			}
			if err != nil || !result.Completed || !result.Success || result.SessionID != "ses_current" || answer.String() != "verified answer" {
				t.Fatalf("confirmed turn: %+v %q %v", result, answer.String(), err)
			}
			state, found, err := fs.LoadSessionState("opencode", a.scopedSessionKey("conversation"))
			if err != nil || !found || state.SessionID != "ses_current" {
				t.Fatalf("confirmed conversation not saved: %+v %v", state, err)
			}
		})
	}
}
