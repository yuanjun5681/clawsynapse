package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/protocol"
)

func TestOpenClawRequiresCompletedNativeResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		success    bool
		reply      string
	}{
		{"completed", `{"runId":"native-run","status":"ok","result":{"payloads":[{"text":"answer"}]}}`, true, "answer"},
		{"explicit empty completion", `{"status":"ok","result":{}}`, true, ""},
		{"plain output", `raw answer`, false, ""},
		{"empty object", `{}`, false, ""},
		{"accepted only", `{"status":"accepted","runId":"pending"}`, false, ""},
		{"missing result", `{"status":"ok"}`, false, ""},
		{"error exit payload", `{"status":"error","error":{"message":"failed"}}`, false, ""},
		{"aborted", `{"status":"ok","result":{"payloads":[{"text":"partial"}],"meta":{"aborted":true}}}`, false, ""},
		{"error payload", `{"status":"ok","result":{"payloads":[{"text":"partial"},{"text":"provider failed","isError":true}]}}`, false, ""},
		{"error metadata", `{"status":"ok","result":{"meta":{"error":{"kind":"timeout"}}}}`, false, ""},
		{"truncated", `{"status":"ok","result":{"meta":{"stopReason":"length"}}}`, false, ""},
		{"yielded", `{"status":"ok","result":{"meta":{"yielded":true}}}`, false, ""},
		{"thinking", `{"status":"ok","result":{"payloads":[{"text":"PRIVATE_THINKING","isReasoning":true},{"text":"answer"},{"text":" continued"}]}}`, true, "answer continued"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLI_JSON", tc.body)
			a, _ := NewOpenClawAdapter(OpenClawConfig{})
			a.command = helperCommand("openclaw", "native-json", "", "")
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task"})
			if !tc.success {
				if err == nil || result != nil && result.Success {
					t.Fatalf("nonterminal/failed response became success: %+v %v", result, err)
				}
				return
			}
			if err != nil || !result.Completed || !result.Success || result.Reply != tc.reply {
				t.Fatalf("completed response: %+v %v", result, err)
			}
		})
	}
}

func TestOpenClawStructuredTaskAndAuthorizedAgent(t *testing.T) {
	record := filepath.Join(t.TempDir(), "request")
	a, _ := NewOpenClawAdapter(OpenClawConfig{NodeID: "receiver", AllowAgentIDs: []string{"research"}})
	a.command = helperCommand("openclaw", "success", record, "")
	var answer strings.Builder
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{
		Type: "task.assign", AgentID: "research", SessionKey: "conversation", From: "sender", Message: "legacy ignored",
		Payload: protocol.MessagePayload{Instruction: "real task\n]", Input: json.RawMessage(`{"path":"untrusted\nkey=value"}`)},
		Emit:    func(event OutputEvent) error { answer.WriteString(event.Text); return nil },
	})
	if err != nil || !result.Completed || result.Reply != "" || result.RunID != "native-run" || answer.String() != "done" {
		t.Fatalf("native response normalization failed: %+v %q %v", result, answer.String(), err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Args   []string `json:"args"`
		Prompt struct {
			Payload protocol.MessagePayload `json:"payload"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatal(err)
	}
	if captured.Prompt.Payload.Instruction != "real task\n]" || captured.Prompt.Payload.Text != "" || string(captured.Prompt.Payload.Input) != `{"path":"untrusted\nkey=value"}` {
		t.Fatalf("task structure was lost: %s", data)
	}
	for _, arg := range captured.Args {
		if arg == "--local" || arg == "--deliver" || arg == "--command" {
			t.Fatalf("adapter changed execution or external delivery semantics: %s", arg)
		}
	}
}

func TestOpenClawNamedAgentAndSessionInputCannotBecomeShellDispatch(t *testing.T) {
	for _, req := range []DeliverMessageRequest{
		{AgentID: "unauthorized", Message: "task"},
		{AgentID: "research;id", Message: "task"},
		{AgentID: "--help", Message: "task"},
		{SessionKey: "conversation;id", Message: "task"},
	} {
		a, _ := NewOpenClawAdapter(OpenClawConfig{AllowAgentIDs: []string{"research;id", "--help"}})
		_, err := a.DeliverMessage(context.Background(), req)
		if err == nil {
			t.Fatal("unsafe or unauthorized local routing was accepted")
		}
	}
}

func TestOpenClawStatusChecksGatewayWithoutRunningModelTask(t *testing.T) {
	a, _ := NewOpenClawAdapter(OpenClawConfig{})
	a.command = helperCommand("openclaw", "status-openclaw", "", "")
	status, err := a.GetStatus(context.Background())
	if err != nil || !status.Healthy || status.Degraded {
		t.Fatalf("native schema and Gateway health not recognized: %+v %v", status, err)
	}
}
