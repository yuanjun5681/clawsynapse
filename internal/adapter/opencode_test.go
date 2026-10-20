package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"clawsynapse/internal/store"
)

func TestOpenCodeOnlyPublishesAnswerParts(t *testing.T) {
	a, _ := NewOpenCodeAdapter(OpenCodeConfig{})
	a.command = helperCommand("opencode", "thinking", "", "")
	var answer strings.Builder
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Emit: func(event OutputEvent) error { answer.WriteString(event.Text); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || !result.Success || result.Reply != "" || answer.String() != "early" {
		t.Fatalf("reasoning reached the consumer or completion was lost: %+v / %q", result, answer.String())
	}
}

func TestOpenCodeRequiresSuccessfulFinalStep(t *testing.T) {
	for _, tc := range []struct{ mode, code string }{
		{"partial-failure", "native_failed"}, {"tool-only", "incomplete_output"}, {"length", "native_failed"},
		{"start-only", "incomplete_output"}, {"raw", "native_schema"}, {"empty", "incomplete_output"}, {"nonzero", "exit_failed"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			a, _ := NewOpenCodeAdapter(OpenCodeConfig{})
			a.command = helperCommand("opencode", tc.mode, "", "")
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task"})
			assertExecutionCode(t, err, tc.code)
			if result != nil && result.Success {
				t.Fatal("failed/incomplete native output became success")
			}
		})
	}
}

func TestOpenCodePersistsCompletedSessionForNextTurn(t *testing.T) {
	fs := store.NewFSStore(t.TempDir())
	a, _ := NewOpenCodeAdapter(OpenCodeConfig{SessionStore: fs})
	record := filepath.Join(t.TempDir(), "request")
	a.command = helperCommand("opencode", "success", record, "")
	for range 2 {
		result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", SessionKey: "conversation"})
		if err != nil || result.SessionID != "ses_fixture" || !result.Completed {
			t.Fatalf("completed turn: %+v %v", result, err)
		}
	}
	state, found, err := fs.LoadSessionState("opencode", "conversation")
	if err != nil || !found || state.SessionID != "ses_fixture" {
		t.Fatalf("session not preserved: %+v %v", state, err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(captured.Args, " ")
	if !strings.Contains(joined, "--session ses_fixture") {
		t.Fatal("second turn did not resume the persisted native conversation")
	}
}

func TestOpenCodeNamedAgentsRequireLocalAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, agent string
		allowed     []string
		ok          bool
	}{
		{"default", "", nil, true}, {"unauthorized", "build", nil, false},
		{"authorized", "build", []string{"build"}, true}, {"unsafe", "build;rm", []string{"build;rm"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := NewOpenCodeAdapter(OpenCodeConfig{AllowAgentIDs: tc.allowed})
			a.command = helperCommand("opencode", "success", "", "")
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{AgentID: tc.agent, Message: "task"})
			if !tc.ok {
				assertExecutionCode(t, err, "unsupported_agent")
				return
			}
			if err != nil || !result.Completed {
				t.Fatalf("locally authorized route failed: %+v %v", result, err)
			}
		})
	}
}

func TestNativeSnapshotUpdatesDoNotDuplicateAnswers(t *testing.T) {
	for _, product := range []string{"codex", "opencode"} {
		t.Run(product, func(t *testing.T) {
			a := newCLIAdapter(product, "", nil, nil, nil)
			a.command = helperCommand(product, "snapshots", "", "")
			var answer strings.Builder
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Emit: func(event OutputEvent) error { answer.WriteString(event.Text); return nil }})
			if err != nil || result.Reply != "" || answer.String() != "early plus" {
				t.Fatalf("snapshot was duplicated or lost: %+v / %q / %v", result, answer.String(), err)
			}
		})
	}
}

func TestAnswerBatchesPreserveUnicodeAndBoundConsumerMemory(t *testing.T) {
	text := strings.Repeat("世界", 2500)
	part, _ := json.Marshal(map[string]any{"type": "text", "part": map[string]any{"type": "text", "text": text}})
	t.Setenv("CLI_JSON", string(part)+"\n"+`{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}`)
	a, _ := NewOpenCodeAdapter(OpenCodeConfig{})
	a.command = helperCommand("opencode", "native-json", "", "")
	var answer strings.Builder
	batches := 0
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Emit: func(event OutputEvent) error {
		if len(event.Text) > maxDeltaBytes || !utf8.ValidString(event.Text) {
			return errors.New("invalid consumer batch")
		}
		batches++
		answer.WriteString(event.Text)
		return nil
	}})
	if err != nil || result.Reply != "" || answer.String() != text || batches < 2 {
		t.Fatalf("answer batching lost data: batches=%d err=%v", batches, err)
	}
}

func TestOpenCodeReadinessReportsItsProbeLimitation(t *testing.T) {
	a, _ := NewOpenCodeAdapter(OpenCodeConfig{})
	a.command = helperCommand("opencode", "status-opencode", "", "")
	status, err := a.GetStatus(context.Background())
	if err != nil || !status.Healthy || !status.Degraded || status.Detail == "" {
		t.Fatalf("credential listing cannot establish model connectivity: %+v %v", status, err)
	}
}

func TestArgvOnlyAdaptersRejectOversizedPromptsBeforeExecution(t *testing.T) {
	for _, product := range []string{"opencode", "openclaw"} {
		t.Run(product, func(t *testing.T) {
			a := newCLIAdapter(product, "", nil, nil, nil)
			_, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: strings.Repeat("x", maxArgvPromptBytes+1)})
			failure := assertExecutionCode(t, err, "prompt_limit")
			if failure.Indeterminate {
				t.Fatal("validation rejection must happen before native execution")
			}
		})
	}
}

func TestOpenCodeTextArrivesBeforeFinalStep(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	a, _ := NewOpenCodeAdapter(OpenCodeConfig{})
	a.command = helperCommand("opencode", "gate", "", gate)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observed := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := a.DeliverMessage(ctx, DeliverMessageRequest{Message: "task", Emit: func(event OutputEvent) error { observed <- event.Text; return nil }})
		done <- err
	}()
	select {
	case answer := <-observed:
		if answer != "early" {
			t.Fatalf("wrong answer: %q", answer)
		}
	case err := <-done:
		t.Fatalf("no early text before completion: %v", err)
	case <-ctx.Done():
		t.Fatal("no early text")
	}
	select {
	case err := <-done:
		t.Fatalf("turn finished before its native terminal step: %v", err)
	default:
	}
	if err := os.WriteFile(gate, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
