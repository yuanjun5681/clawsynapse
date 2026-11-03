package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These fixtures execute the test binary as a real CLI child. Tests cross the
// public DeliverMessage seam, including stdin, concurrent pipes and cancellation.
func helperCommand(product, mode, record, gate string) commandFactory {
	return func(ctx context.Context, args ...string) *exec.Cmd {
		argv := append([]string{"-test.run=^TestNativeCLIHelperProcess$", "--"}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "CLAWSYNAPSE_ADAPTER_HELPER=1", "CLI_PRODUCT="+product, "CLI_MODE="+mode, "CLI_RECORD="+record, "CLI_GATE="+gate)
		return cmd
	}
}

func fixtureArgs() []string {
	for i, arg := range os.Args {
		if arg == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

func fixtureEvent(value any) { data, _ := json.Marshal(value); fmt.Println(string(data)) }

func fixtureAnswer(product, text string) {
	if product == "codex" {
		fixtureEvent(map[string]any{"type": "item.completed", "item": map[string]any{"id": "answer", "type": "agent_message", "text": text}})
	} else {
		fixtureEvent(map[string]any{"type": "text", "sessionID": "ses_fixture", "part": map[string]any{"id": "answer", "type": "text", "text": text}})
	}
}

func fixtureDone(product string) {
	if product == "codex" {
		fmt.Println(`{"type":"turn.completed"}`)
	} else {
		fmt.Println(`{"type":"step_finish","sessionID":"ses_fixture","part":{"type":"step-finish","reason":"stop"}}`)
	}
}

func fixtureWaitFile(path string) {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(21)
}

func TestNativeCLIHelperProcess(t *testing.T) {
	if os.Getenv("CLAWSYNAPSE_ADAPTER_HELPER") != "1" {
		return
	}
	product, mode := os.Getenv("CLI_PRODUCT"), os.Getenv("CLI_MODE")
	args := fixtureArgs()
	if mode == "confirmed-turn" || mode == "stale-turn" || mode == "unfinished-turn" || mode == "failed-turn" {
		if len(args) > 1 && args[0] == "session" && args[1] == "export" {
			id, finish, outcome := "msg_current", "stop", "succeeded"
			if mode == "stale-turn" {
				id = "msg_previous"
			}
			if mode == "unfinished-turn" {
				finish = "tool-calls"
			}
			if mode == "failed-turn" {
				outcome = "failed"
			}
			fixtureEvent(map[string]any{
				"info": map[string]any{"id": "ses_current", "outcome": outcome},
				"messages": []any{
					map[string]any{"id": id, "type": "assistant", "finish": finish, "time": map[string]any{"completed": 123}},
					map[string]any{"type": "idle", "outcome": outcome},
				},
			})
			os.Exit(0)
		}
		fmt.Println(`{"type":"step_start","sessionID":"ses_current","part":{"type":"step-start","messageID":"msg_current"}}`)
		fmt.Println(`{"type":"text","sessionID":"ses_current","part":{"type":"text","messageID":"msg_current","id":"answer","text":"verified answer"}}`)
		os.Exit(0)
	}
	if mode == "descendant" {
		if err := os.WriteFile(os.Getenv("CLI_RECORD")+".ready", []byte("ready"), 0600); err != nil {
			os.Exit(22)
		}
		fixtureWaitFile(os.Getenv("CLI_GATE"))
		_ = os.WriteFile(os.Getenv("CLI_RECORD"), []byte("escaped"), 0600)
		os.Exit(0)
	}
	if len(args) > 0 && (args[len(args)-1] == "--help" || args[0] == "login" || args[0] == "auth" || args[0] == "health") {
		switch mode {
		case "status-version-only":
			fmt.Println("v1.0.0")
		case "status-no-auth":
			if args[0] == "login" {
				os.Exit(1)
			}
			fmt.Println("--json --skip-git-repo-check stdin")
		case "status-opencode":
			if args[0] == "auth" {
				fmt.Println("1 credential configured")
			} else {
				fmt.Println("--format --session --agent --standalone")
			}
		case "status-openclaw":
			if args[0] == "health" {
				fmt.Println(`{"ok":true}`)
			} else {
				fmt.Println("--json --message --session-id --agent")
			}
		default:
			fmt.Println("--json --skip-git-repo-check stdin")
		}
		os.Exit(0)
	}
	var prompt string
	if product == "codex" {
		data, _ := io.ReadAll(os.Stdin)
		prompt = string(data)
	} else if product == "opencode" && len(args) > 0 {
		prompt = args[len(args)-1]
	} else {
		for i := range len(args) - 1 {
			if args[i] == "--message" {
				prompt = args[i+1]
			}
		}
	}
	if record := os.Getenv("CLI_RECORD"); record != "" && mode != "tree" {
		cwd, _ := os.Getwd()
		data, _ := json.Marshal(map[string]any{"args": args, "cwd": cwd, "prompt": json.RawMessage(prompt)})
		if err := os.WriteFile(record, data, 0600); err != nil {
			os.Exit(23)
		}
	}
	if mode == "private-engine" {
		private := false
		for _, arg := range args {
			if arg == "--standalone" {
				private = true
			}
		}
		if !private {
			os.Exit(25)
		}
		cwd, _ := os.Getwd()
		id := "ses_" + filepath.Base(cwd)
		fixtureEvent(map[string]any{"type": "text", "sessionID": id, "part": map[string]any{"id": "answer", "type": "text", "text": filepath.Base(cwd)}})
		fixtureWaitFile(os.Getenv("CLI_GATE"))
		fixtureEvent(map[string]any{"type": "step_finish", "sessionID": id, "part": map[string]any{"type": "step-finish", "reason": "stop"}})
		os.Exit(0)
	}
	switch mode {
	case "raw":
		fmt.Println("unstructured answer")
		os.Exit(0)
	case "empty":
		os.Exit(0)
	case "start-only":
		if product == "codex" {
			fmt.Println(`{"type":"thread.started","thread_id":"thread_fixture"}`)
		} else {
			fmt.Println(`{"type":"step_start","sessionID":"ses_fixture","part":{"type":"step-start"}}`)
		}
		os.Exit(0)
	case "event-limit":
		fmt.Print(strings.Repeat("x", maxEventBytes+1))
		time.Sleep(30 * time.Second)
	case "total-limit":
		for range 600 {
			fixtureEvent(map[string]any{"type": "reasoning", "text": strings.Repeat("x", 8192)})
		}
		time.Sleep(30 * time.Second)
	case "stderr-limit":
		fmt.Fprint(os.Stderr, strings.Repeat("e", maxStderrBytes+1))
		time.Sleep(30 * time.Second)
	case "native-json":
		fmt.Print(os.Getenv("CLI_JSON"))
		os.Exit(0)
	case "unknown-session":
		fmt.Println(`{"type":"error","error":{"message":"unknown session: not found"}}`)
		time.Sleep(30 * time.Second)
	case "tree":
		child := exec.Command(os.Args[0], "-test.run=^TestNativeCLIHelperProcess$")
		child.Env = append(os.Environ(), "CLI_MODE=descendant")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(24)
		}
		fixtureWaitFile(os.Getenv("CLI_RECORD") + ".ready")
		fixtureAnswer(product, "early")
		time.Sleep(30 * time.Second)
	}
	if product == "openclaw" {
		fmt.Println(`{"runId":"native-run","status":"ok","result":{"payloads":[{"text":"done"}],"meta":{"stopReason":"stop","agentMeta":{"sessionId":"session_fixture"}}}}`)
		os.Exit(0)
	}
	if product == "codex" {
		fmt.Println(`{"type":"thread.started","thread_id":"thread_fixture"}`)
	}
	fixtureAnswer(product, "early")
	switch mode {
	case "gate":
		fixtureWaitFile(os.Getenv("CLI_GATE"))
	case "block":
		time.Sleep(30 * time.Second)
	case "partial-failure":
		if product == "codex" {
			fmt.Println(`{"type":"turn.failed","error":{"message":"provider unavailable"}}`)
		} else {
			fmt.Println(`{"type":"error","error":{"name":"ProviderError","data":{"message":"provider unavailable"}}}`)
		}
		time.Sleep(30 * time.Second)
	case "thinking":
		fmt.Println(`{"type":"reasoning","text":"PRIVATE_THINKING","part":{"type":"reasoning","text":"PRIVATE_THINKING"}}`)
		fmt.Println(`{"type":"text","part":{"type":"reasoning","text":"PRIVATE_THINKING"}}`)
	case "tool-only":
		fmt.Println(`{"type":"step_finish","part":{"type":"step-finish","reason":"tool-calls"}}`)
		os.Exit(0)
	case "length":
		fmt.Println(`{"type":"step_finish","part":{"type":"step-finish","reason":"length"}}`)
		os.Exit(0)
	case "snapshots":
		fixtureAnswer(product, "early plus")
		fixtureAnswer(product, "early plus")
	}
	fixtureDone(product)
	if mode == "nonzero" {
		os.Exit(7)
	}
	os.Exit(0)
}

func assertExecutionCode(t *testing.T, err error, code string) *ExecutionError {
	t.Helper()
	var execution *ExecutionError
	if !errors.As(err, &execution) || execution.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
	return execution
}

func TestPromptUsesPeerReplySessionKey(t *testing.T) {
	prompt, err := formatDeliverMessage("n1-local", DeliverMessageRequest{
		MessageID:       "m1",
		Type:            "chat.message",
		From:            "n1-peer",
		SessionKey:      "scoped-internal-key",
		ReplySessionKey: "topic-1",
		Message:         "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SessionKey string `json:"sessionKey"`
	}
	if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SessionKey != "topic-1" {
		t.Fatalf("prompt session key = %q, want topic-1", envelope.SessionKey)
	}

	prompt, err = formatDeliverMessage("n1-local", DeliverMessageRequest{MessageID: "m2", SessionKey: "legacy-key", Message: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SessionKey != "legacy-key" {
		t.Fatalf("fallback session key = %q, want legacy-key", envelope.SessionKey)
	}
}
