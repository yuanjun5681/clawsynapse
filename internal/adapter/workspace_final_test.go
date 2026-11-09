package adapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func workspaceNativeLines(t *testing.T, product string) []string {
	t.Helper()
	switch product {
	case "pi", "omp":
		lines := append(piOMPStart(t), piOMPDelta(t, "A complete-looking answer before a tool."))
		message := piOMPMessage("assistant", "A complete-looking answer before a tool.", "toolUse")
		lines = append(lines, piOMPJSON(t, map[string]any{"type": "message_end", "message": message}), `{"type":"tool_execution_start"}`, `{"type":"tool_execution_end"}`, `{"type":"turn_start"}`, piOMPJSON(t, map[string]any{"type": "message_start", "message": piOMPMessage("assistant", "", "")}), piOMPDelta(t, "Now reading is the actual answer."))
		return append(lines, piOMPEnd(t, product, "Now reading is the actual answer.", "stop")...)
	case "codex":
		return []string{`{"type":"thread.started","thread_id":"thread_workspace"}`, `{"type":"turn.started"}`, `{"type":"item.completed","item":{"id":"before","type":"agent_message","text":"A complete-looking answer before a tool."}}`, `{"type":"item.completed","item":{"id":"tool","type":"command_execution"}}`, `{"type":"item.started","item":{"id":"final","type":"agent_message","text":""}}`, `{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"Now reading is the actual answer."}}`, `{"type":"turn.completed"}`}
	case "opencode":
		return []string{`{"type":"step_start","sessionID":"ses_workspace","part":{"type":"step-start","messageID":"before"}}`, `{"type":"text","part":{"id":"before-text","type":"text","messageID":"before","text":"A complete-looking answer before a tool."}}`, `{"type":"step_finish","part":{"type":"step-finish","reason":"tool-calls"}}`, `{"type":"step_start","part":{"type":"step-start","messageID":"final"}}`, `{"type":"text","part":{"id":"final-text","type":"text","messageID":"final","text":"Now reading is the actual answer."}}`, `{"type":"step_finish","part":{"type":"step-finish","reason":"stop"}}`}
	default:
		return []string{`{"status":"ok","result":{"payloads":[{"text":"A complete-looking answer before a tool.","isCommentary":true},{"text":"Earlier assistant message."},{"text":"Now reading is the actual answer."}],"meta":{"stopReason":"stop"}}}`}
	}
}

func TestWorkspaceNativePublishesOnlyAuthoritativeFinalAssistant(t *testing.T) {
	for _, product := range []string{"pi", "omp", "codex", "opencode", "openclaw"} {
		t.Run(product, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				a, err := newCLIAdapter(product, "bot", "binding", t.TempDir(), "", nil, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				a.command = helperCommand(product, "native-json", "", "")
				if a.isPiFamily() {
					piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
				}
				t.Setenv("CLI_JSON", strings.Join(workspaceNativeLines(t, product), "\n")+"\n")
				var visible strings.Builder
				req := DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}}
				if streaming {
					req.Emit = func(event OutputEvent) error { visible.WriteString(event.Text); return nil }
				}
				result, err := a.DeliverMessage(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if !streaming {
					visible.WriteString(result.Reply)
				} else if result.Reply != "" {
					t.Fatal("final answer duplicated")
				}
				if visible.String() != "Now reading is the actual answer." {
					t.Fatalf("%s leaked earlier assistant messages: %q", product, visible.String())
				}
			}
		})
	}
}

func TestWorkspaceNativeFailureNeverPublishesCandidate(t *testing.T) {
	for _, product := range []string{"pi", "omp", "codex", "opencode", "openclaw"} {
		t.Run(product, func(t *testing.T) {
			a, err := newCLIAdapter(product, "bot", "binding", t.TempDir(), "", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.command = helperCommand(product, "native-json", "", "")
			lines := workspaceNativeLines(t, product)
			if product == "openclaw" {
				lines = []string{`{"status":"ok","result":{"payloads":[{"text":"partial"}],"meta":{"aborted":true}}}`}
			} else {
				lines = append(lines, `{"type":"error","error":{"message":"late provider failure"}}`)
			}
			t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
			var visible strings.Builder
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}, Emit: func(event OutputEvent) error { visible.WriteString(event.Text); return nil }})
			if err == nil || result != nil || visible.Len() != 0 {
				t.Fatalf("failed %s published candidate: %#v / %q / %v", product, result, visible.String(), err)
			}
		})
	}
}

func TestWorkspaceWebhookRequiresExplicitFinalLane(t *testing.T) {
	for _, test := range []struct {
		name, body, want string
		failed           bool
	}{
		{"final", "{\"type\":\"output\",\"text\":\"process\"}\n{\"type\":\"final\",\"text\":\"answer\"}\n{\"type\":\"completed\"}\n", "answer", false},
		{"legacy output is not final", "{\"type\":\"output\",\"text\":\"process\"}\n{\"type\":\"completed\"}\n", "", true},
		{"failed candidate", "{\"type\":\"final\",\"text\":\"partial\"}\n{\"type\":\"failed\",\"error\":\"failed\"}\n", "", true},
		{"late failure", "{\"type\":\"final\",\"text\":\"partial\"}\n{\"type\":\"completed\"}\n{\"type\":\"failed\",\"error\":\"failed\"}\n", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var visible strings.Builder
			result, err := consumeWebhookStream(strings.NewReader(test.body), func(event OutputEvent) error { visible.WriteString(event.Text); return nil }, true)
			if (err != nil) != test.failed || visible.String() != test.want || test.failed && result != nil {
				t.Fatalf("workspace webhook result %#v / %q / %v", result, visible.String(), err)
			}
		})
	}
}

func TestWorkspaceNativeToolRoundIsNotFinal(t *testing.T) {
	for _, product := range []string{"pi", "omp", "codex", "opencode"} {
		t.Run(product, func(t *testing.T) {
			a, err := newCLIAdapter(product, "bot", "binding", t.TempDir(), "", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.command = helperCommand(product, "native-json", "", "")
			var lines []string
			switch product {
			case "pi", "omp":
				message := piOMPMessage("assistant", "Not a final answer.", "toolUse")
				lines = append(piOMPStart(t), piOMPDelta(t, "Not a final answer."), piOMPJSON(t, map[string]any{"type": "message_end", "message": message}), piOMPJSON(t, map[string]any{"type": "agent_end", "messages": []any{message}, "isTerminal": true}))
			case "codex":
				lines = []string{`{"type":"thread.started","thread_id":"thread_tool"}`, `{"type":"item.completed","item":{"id":"before","type":"agent_message","text":"Not a final answer."}}`, `{"type":"item.completed","item":{"id":"tool","type":"command_execution"}}`, `{"type":"turn.completed"}`}
			case "opencode":
				lines = []string{`{"type":"step_start","sessionID":"ses_tool"}`, `{"type":"text","part":{"id":"before","type":"text","text":"Not a final answer."}}`, `{"type":"step_finish","part":{"type":"step-finish","reason":"tool-calls"}}`}
			}
			t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
			var visible strings.Builder
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}, Emit: func(event OutputEvent) error { visible.WriteString(event.Text); return nil }})
			if err == nil || result != nil || visible.Len() != 0 {
				t.Fatalf("tool-only %s became final: %#v / %q / %v", product, result, visible.String(), err)
			}
		})
	}
}

func TestWorkspaceWebhookDeliveryUsesFinalLane(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, "{\"type\":\"output\",\"text\":\"process\"}\n{\"type\":\"final\",\"text\":\"answer\"}\n{\"type\":\"completed\"}\n")
	}))
	defer server.Close()
	a, err := NewWebhookAdapter(testWebhookConfig(t, WebhookConfig{URL: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}, Emit: func(event OutputEvent) error { visible.WriteString(event.Text); return nil }})
	if err != nil || result == nil || !result.Completed || result.Reply != "" || visible.String() != "answer" {
		t.Fatalf("webhook leaked commentary: %#v / %q / %v", result, visible.String(), err)
	}
}

func TestWorkspacePiUsesFinalSnapshotInsteadOfDraftDelta(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			a := piOMPTestAdapter(t, product, nil)
			piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
			lines := append(piOMPStart(t), piOMPDelta(t, "Draft text that was revised."))
			lines = append(lines, piOMPEnd(t, product, "The final reply.", "stop")...)
			t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}})
			if err != nil || result == nil || result.Reply != "The final reply." {
				t.Fatalf("draft became final: %#v / %v", result, err)
			}
		})
	}
}

func TestWorkspaceOpenCodePersistedFinalTurnExcludesEarlierStep(t *testing.T) {
	a, err := newCLIAdapter("opencode", "bot", "binding", t.TempDir(), "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.command = helperCommand("opencode", "workspace-confirmed-turn", "", "")
	var visible strings.Builder
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", Metadata: map[string]any{"clawsynapseReplyMode": "chat"}, Emit: func(event OutputEvent) error { visible.WriteString(event.Text); return nil }})
	if err != nil || result == nil || !result.Completed || visible.String() != "verified answer" {
		t.Fatalf("persisted turn leaked earlier assistant: %#v / %q / %v", result, visible.String(), err)
	}
}
