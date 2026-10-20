package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
				fmt.Println("--format --session --agent")
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
		data, _ := json.Marshal(map[string]any{"args": args, "prompt": json.RawMessage(prompt)})
		if err := os.WriteFile(record, data, 0600); err != nil {
			os.Exit(23)
		}
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
