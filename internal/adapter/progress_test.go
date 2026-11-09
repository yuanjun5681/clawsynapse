package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProgressContextOverridesInheritedRoutingAndReachesGatewayPrompt(t *testing.T) {
	t.Setenv("CLAWSYNAPSE_API_ADDR", "http://127.0.0.1:19999")
	t.Setenv("CLAWSYNAPSE_BOT_ID", "wrong-inherited-bot")
	for _, product := range []string{"codex", "opencode", "openclaw"} {
		t.Run(product, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "invocation.json")
			a, err := newCLIAdapter(product, "local", "binding", t.TempDir(), "", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.command = helperCommand(product, "success", record, "")
			execution := NewExecutionContext("/private/tools/clawsynapse", "http://127.0.0.1:18082", "local", "sender", "request", "run", "visible-topic", "")
			_, err = a.DeliverMessage(context.Background(), DeliverMessageRequest{MessageID: "request", RequestID: "request", RunID: "run", Type: "chat.message", From: "sender", SessionKey: "private-session", ReplySessionKey: "visible-topic", Message: "hello", ExecutionContext: execution})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Prompt struct {
					ExecutionContext *ExecutionContext `json:"executionContext"`
					SessionKey       string            `json:"sessionKey"`
				} `json:"prompt"`
				Env map[string]string `json:"progressEnv"`
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Prompt.ExecutionContext, execution) || got.Prompt.SessionKey != "visible-topic" || got.Env["apiAddr"] != execution.APIAddr || got.Env["botId"] != "local" || got.Env["requestId"] != "request" || got.Env["sessionKey"] != "visible-topic" {
				t.Fatalf("native invocation inherited wrong progress context: %s", data)
			}
		})
	}
}

