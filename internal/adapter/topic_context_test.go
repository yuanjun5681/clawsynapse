package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

// Real child-process fixture: persistence is independent from the stdout events.
// Shapes mirror Pi/OMP JSONL and OpenCode v2 session export, including private
// reasoning, non-user metadata, and the native active-leaf chain.
func fixtureTopicProcess(t *testing.T, product, mode string, args []string) {
	statePath := os.Getenv("CLI_TOPIC_STATE")
	if len(args) > 1 && args[0] == "session" && args[1] == "export" {
		data, err := os.ReadFile(statePath)
		if err != nil {
			os.Exit(41)
		}
		fmt.Print(string(data))
		return
	}
	turn := os.Getenv("CLI_TOPIC_TURN")
	if turn == "" {
		turn = "1"
	}
	prompt, policy := "", ""
	if product == "opencode" {
		prompt = args[len(args)-1]
		var config struct {
			Instructions []string        `json:"instructions"`
			Permission   json.RawMessage `json:"permission"`
		}
		if json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &config) != nil || len(config.Instructions) == 0 {
			os.Exit(42)
		}
		data, err := os.ReadFile(config.Instructions[len(config.Instructions)-1])
		if err != nil {
			os.Exit(43)
		}
		policy = string(data)
	} else {
		data, _ := io.ReadAll(os.Stdin)
		prompt = string(data)
		for i := range len(args) - 1 {
			if args[i] == "--append-system-prompt" {
				policy = args[i+1]
			}
			if args[i] == "--session-dir" {
				statePath = filepath.Join(args[i+1], "2026-11-09T00-00-00-000Z_"+piOMPSessionID+".jsonl")
			}
		}
	}
	if record := os.Getenv("CLI_RECORD"); record != "" {
		data, _ := json.Marshal(map[string]any{"args": args, "input": json.RawMessage(prompt), "policy": policy,
			"config": os.Getenv("OPENCODE_CONFIG_CONTENT"), "contextToken": os.Getenv("CLAWSYNAPSE_CONTEXT_TOKEN")})
		if err := os.WriteFile(record, data, 0600); err != nil {
			os.Exit(44)
		}
	}
	persisted := prompt
	if mode == "topic-wrong-input" {
		persisted = "different native input"
	}
	if mode != "topic-no-persistence" {
		if product == "opencode" {
			var state struct {
				Info     map[string]any   `json:"info"`
				Messages []map[string]any `json:"messages"`
			}
			if data, err := os.ReadFile(statePath); err == nil {
				if json.Unmarshal(data, &state) != nil {
					os.Exit(45)
				}
			}
			reasoning := "PRIVATE_REASONING"
			if mode == "topic-long-history" {
				reasoning = strings.Repeat("private native history ", 300000)
			}
			state.Info = map[string]any{"id": "ses_topic", "outcome": "succeeded"}
			state.Messages = append(state.Messages,
				map[string]any{"id": "msg_user_" + turn, "type": "user", "text": persisted, "time": map[string]any{"created": 123}},
				map[string]any{"id": "msg_answer_" + turn, "type": "assistant", "finish": "stop", "time": map[string]any{"created": 123, "completed": 124}, "content": []any{map[string]any{"type": "text", "text": "answer " + turn}, map[string]any{"type": "reasoning", "text": reasoning}}},
				map[string]any{"id": "msg_idle_" + turn, "type": "idle", "outcome": "succeeded", "time": map[string]any{"created": 125}})
			if mode == "topic-provider-error" {
				state.Messages = state.Messages[:len(state.Messages)-2]
				state.Info["outcome"] = "failed"
			}
			data, _ := json.MarshalIndent(state, "", "  ")
			if err := os.WriteFile(statePath, data, 0600); err != nil {
				os.Exit(46)
			}
		} else {
			var lines []string
			if data, err := os.ReadFile(statePath); err == nil {
				lines = strings.Split(strings.TrimSpace(string(data)), "\n")
			} else {
				if product == "omp" {
					lines = append(lines, `{"type":"title","v":1,"title":"native title","pad":" "}`)
				}
				lines = append(lines, piOMPJSON(t, map[string]any{"type": "session", "id": piOMPSessionID, "version": 3, "cwd": "/private/workspace"}),
					`{"type":"model_change","id":"model","parentId":null,"provider":"provider","modelId":"test"}`)
			}
			parent := "model"
			if turn != "1" {
				n, _ := strconv.Atoi(turn)
				parent = "answer_" + strconv.Itoa(n-1)
			}
			if mode == "topic-branch" {
				parent = "model"
			}
			lines = append(lines,
				piOMPJSON(t, map[string]any{"type": "message", "id": "user_" + turn, "parentId": parent, "timestamp": "2026-11-09T00:00:00Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": persisted}}}}),
				piOMPJSON(t, map[string]any{"type": "message", "id": "answer_" + turn, "parentId": "user_" + turn, "timestamp": "2026-11-09T00:00:01Z", "message": piOMPMessage("assistant", "answer "+turn, "stop")}))
			if mode == "topic-provider-error" {
				lines = lines[:len(lines)-1]
			}
			if product == "omp" && mode != "topic-provider-error" {
				lines = append(lines, piOMPJSON(t, map[string]any{"type": "model_usage", "id": "usage_" + turn, "parentId": "answer_" + turn, "activeLeafId": "answer_" + turn, "usage": map[string]any{"input": 42}}))
			}
			if err := os.WriteFile(statePath, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
				os.Exit(47)
			}
		}
	}
	if mode == "topic-provider-error" {
		if product != "opencode" {
			fixtureEvent(map[string]any{"type": "session", "id": piOMPSessionID})
		}
		fixtureEvent(map[string]any{"type": "error", "error": map[string]any{"code": "authentication_error", "message": "invalid api key"}})
		return
	}
	if product == "opencode" {
		fixtureEvent(map[string]any{"type": "step_start", "sessionID": "ses_topic", "part": map[string]any{"type": "step-start", "messageID": "msg_answer_" + turn}})
		fixtureEvent(map[string]any{"type": "text", "sessionID": "ses_topic", "part": map[string]any{"type": "text", "id": "text_" + turn, "messageID": "msg_answer_" + turn, "text": "answer " + turn}})
	} else {
		lines := piOMPStart(t)
		// Matching stdout user text is intentionally present even when persistence
		// is missing/wrong, proving it is not the receipt source.
		lines = append(lines, piOMPJSON(t, map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": prompt}}), piOMPDelta(t, "answer "+turn))
		lines = append(lines, piOMPEnd(t, product, "answer "+turn, "stop")...)
		fmt.Print(strings.Join(lines, "\n") + "\n")
	}
}

func topicTestAdapter(t *testing.T, product, mode string) (*cliAdapter, string, string) {
	t.Helper()
	sessions := store.NewFSStore(privateTestDir(t))
	a, err := newCLIAdapter(product, "test-bot", "topic-binding", privateTestDir(t), "", nil, sessions, nil)
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(privateTestDir(t), "invocation.json")
	state := filepath.Join(privateTestDir(t), "export.json")
	t.Setenv("CLI_TOPIC_STATE", state)
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"permission":{"bash":"ask"}}`)
	a.command = helperCommand(product, mode, record, "")
	if a.isPiFamily() {
		state = filepath.Join(a.piSessionDir(), "2026-11-09T00-00-00-000Z_"+piOMPSessionID+".jsonl")
	}
	return a, record, state
}

func topicTestRequest(turn int) DeliverMessageRequest {
	events := []protocol.TopicEvent{
		{Revision: 1, Kind: "user.message", MessageID: "old-user", Author: "Alice", Text: "FIRST_PUBLIC_FACT"},
		{Revision: 2, Kind: "bot.reply.completed", MessageID: "other-reply", BotID: "other-bot", Text: "OTHER_BOT_REPLY"},
	}
	through, materialized, previous := uint64(2), uint64(0), ""
	if turn == 2 {
		events = []protocol.TopicEvent{{Revision: 5, Kind: "bot.reply.completed", MessageID: "new-reply", BotID: "other-bot", Text: "NEW_FOREIGN_FACT"}}
		through, materialized, previous = 5, 3, "request-1"
	}
	return DeliverMessageRequest{MessageID: "transport-id", RequestID: fmt.Sprintf("request-%d", turn), RunID: fmt.Sprintf("run-%d", turn), Seq: 999,
		Type: "chat.message", From: "peer", SessionKey: "conversation", Message: fmt.Sprintf("current instruction %d", turn),
		TopicContext: &protocol.TopicExecution{Turn: protocol.TopicTurn{Schema: protocol.TopicTurnSchema, TurnID: fmt.Sprintf("turn-%d", turn), TurnRevision: through + 1, NativeSessionEpoch: "epoch", PreviousRequestID: previous,
			Context: protocol.TopicContextBatch{ThroughRevision: through, Events: events, Digest: protocol.TopicEventsDigest(events)}}, Events: events, MaterializedThrough: materialized, NativePreviousRequestID: previous, RequireResume: turn > 1}}
}

func TestTopicNativeTwoTurnResumeSeparatesPolicyAndUnseenFacts(t *testing.T) {
	for _, product := range []string{"pi", "omp", "opencode"} {
		t.Run(product, func(t *testing.T) {
			a, record, statePath := topicTestAdapter(t, product, "topic")
			var first *TopicInputReceipt
			var encodedPolicy string
			for turn := 1; turn <= 2; turn++ {
				t.Setenv("CLI_TOPIC_TURN", strconv.Itoa(turn))
				req := topicTestRequest(turn)
				result, err := a.DeliverMessage(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if result.TopicReceipt == nil || result.TopicReceipt.ThroughRevision != req.TopicContext.Turn.TurnRevision || result.TopicReceipt.UserEntryID == "" || result.TopicReceipt.LastEntryID == "" {
					t.Fatalf("missing verified receipt: %+v", result)
				}
				if product == "omp" && result.TopicReceipt.LastEntryID != "answer_"+strconv.Itoa(turn) {
					t.Fatal("off-branch usage accounting replaced the active native leaf")
				}
				data, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				var invocation struct {
					Args   []string   `json:"args"`
					Input  topicInput `json:"input"`
					Policy string     `json:"policy"`
					Config string     `json:"config"`
				}
				if err := json.Unmarshal(data, &invocation); err != nil {
					t.Fatal(err)
				}
				var rawInvocation struct {
					Input json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal(data, &rawInvocation); err != nil {
					t.Fatal(err)
				}
				policyJSON, _ := json.Marshal(invocation.Policy)
				encodedPolicy = string(policyJSON[1 : len(policyJSON)-1])
				if strings.Contains(string(rawInvocation.Input), encodedPolicy) || strings.Contains(string(rawInvocation.Input), "executionContext") {
					t.Fatal("native system policy or runtime context entered user input")
				}
				if invocation.Input.Instruction != req.Message || len(invocation.Input.PublicContext) != len(req.TopicContext.Events) || invocation.Policy == "" {
					t.Fatalf("semantic roles/policy lost: %+v", invocation)
				}
				for _, arg := range invocation.Args {
					if arg == "--auto" || arg == "--auto-approve" || arg == "--approve" {
						t.Fatal("topic context bypassed native user approval settings")
					}
				}
				if product == "opencode" {
					var config struct {
						Permission map[string]string `json:"permission"`
					}
					if json.Unmarshal([]byte(invocation.Config), &config) != nil || config.Permission["bash"] != "ask" {
						t.Fatal("local topic policy replaced native tool permissions")
					}
				}
				if invocation.Input.PublicContext[0].Kind != req.TopicContext.Events[0].Kind || invocation.Input.PublicContext[0].BotID != req.TopicContext.Events[0].BotID || invocation.Input.PublicContext[0].Author != req.TopicContext.Events[0].Author {
					t.Fatal("historical source attribution changed")
				}
				input, _ := json.Marshal(invocation.Input)
				if strings.Contains(string(input), "transport-id") || strings.Contains(string(input), "request-1") || strings.Contains(string(input), invocation.Policy) || strings.Contains(string(input), "999") {
					t.Fatal("transport metadata or local policy entered user transcript")
				}
				sum := sha256.Sum256(input)
				if result.TopicReceipt.InputDigest != "sha256:"+hex.EncodeToString(sum[:]) {
					t.Fatal("receipt did not digest persisted semantic input")
				}
				if turn == 1 {
					first = result.TopicReceipt
				} else {
					if strings.Contains(string(input), "FIRST_PUBLIC_FACT") || result.TopicReceipt.UserEntryID == first.UserEntryID {
						t.Fatal("second turn repeated prior input or reused receipt")
					}
					if product == "pi" || product == "omp" {
						if strings.Contains(strings.Join(invocation.Args, " "), req.Message) {
							t.Fatal("Pi-family prompt escaped stdin")
						}
					}
				}
				// Reconstruct the adapter between turns: continuity must be durable.
				resumed, err := newCLIAdapter(product, a.botID, a.binding, a.workspace, "", nil, a.sessionStore, nil)
				if err != nil {
					t.Fatal(err)
				}
				resumed.command = a.command
				a = resumed
			}
			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), "FIRST_PUBLIC_FACT") != 1 || strings.Count(string(data), "current instruction 1") != 1 || strings.Contains(string(data), encodedPolicy) {
				t.Fatal("native user history contains replayed inputs or system policy")
			}
		})
	}
}

func TestTopicNativeFailsClosedWhenVerifiedBasisDisappearsOrChanges(t *testing.T) {
	for _, product := range []string{"pi", "omp", "opencode"} {
		for _, scenario := range []string{"missing-mapping", "legacy-mapping", "missing-native", "changed-history", "changed-epoch", "wrong-previous-request"} {
			t.Run(product+"/"+scenario, func(t *testing.T) {
				a, record, statePath := topicTestAdapter(t, product, "topic")
				if _, err := a.DeliverMessage(context.Background(), topicTestRequest(1)); err != nil {
					t.Fatal(err)
				}
				req := topicTestRequest(2)
				switch scenario {
				case "missing-mapping":
					if err := a.sessionStore.DeleteSessionState(product, a.scopedSessionKey(req.SessionKey)); err != nil {
						t.Fatal(err)
					}
				case "legacy-mapping":
					st, _, err := a.sessionStore.LoadSessionState(product, a.scopedSessionKey(req.SessionKey))
					if err != nil {
						t.Fatal(err)
					}
					st.Topic = nil
					if err := a.sessionStore.SaveSessionState(st); err != nil {
						t.Fatal(err)
					}
				case "missing-native":
					if err := os.Remove(statePath); err != nil {
						t.Fatal(err)
					}
				case "changed-history":
					data, err := os.ReadFile(statePath)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(statePath, []byte(strings.Replace(string(data), "FIRST_PUBLIC_FACT", "CHANGED_PUBLIC_FACT", 1)), 0600); err != nil {
						t.Fatal(err)
					}
				case "changed-epoch":
					req.TopicContext.Turn.NativeSessionEpoch = "new-epoch"
				case "wrong-previous-request":
					req.TopicContext.NativePreviousRequestID = "unrelated-request"
				}
				if err := os.Remove(record); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CLI_TOPIC_TURN", "2")
				result, err := a.DeliverMessage(context.Background(), req)
				if err == nil || result != nil {
					t.Fatalf("changed basis ran: %+v %v", result, err)
				}
				if _, err := os.Stat(record); !os.IsNotExist(err) {
					t.Fatal("native model executed after basis changed")
				}
			})
		}
	}
}

func TestTopicReceiptRequiresPersistedExactUserInputNotStreamCompletion(t *testing.T) {
	for _, product := range []string{"pi", "omp", "opencode"} {
		for _, mode := range []string{"topic-no-persistence", "topic-wrong-input"} {
			t.Run(product+"/"+mode, func(t *testing.T) {
				a, _, _ := topicTestAdapter(t, product, mode)
				result, err := a.DeliverMessage(context.Background(), topicTestRequest(1))
				if err == nil || result != nil {
					t.Fatalf("stdout became receipt: %+v %v", result, err)
				}
				_, found, loadErr := a.sessionStore.LoadSessionState(product, a.scopedSessionKey("conversation"))
				if loadErr != nil || found {
					t.Fatalf("unverified input mapping persisted: %v %v", found, loadErr)
				}
			})
		}
	}
}

func TestTopicNativeRejectsNewInputOnChangedBranch(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			a, record, _ := topicTestAdapter(t, product, "topic")
			if _, err := a.DeliverMessage(context.Background(), topicTestRequest(1)); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLI_TOPIC_TURN", "2")
			a.command = helperCommand(product, "topic-branch", record, "")
			_, err := a.DeliverMessage(context.Background(), topicTestRequest(2))
			failure := assertExecutionCode(t, err, "session_changed")
			if !failure.Indeterminate {
				t.Fatal("branch changed after execution must be indeterminate")
			}
		})
	}
}

func TestTopicCapacityDefersWholeFactsOnlyWithAuthorizedReader(t *testing.T) {
	for _, product := range []string{"pi", "omp", "opencode"} {
		t.Run(product, func(t *testing.T) {
			a, record, _ := topicTestAdapter(t, product, "topic")
			oversized := topicTestRequest(1)
			oversized.Message = strings.Repeat("current instruction ", 2000)
			oversized.TopicContext.Events = nil
			_, err := a.DeliverMessage(context.Background(), oversized)
			assertExecutionCode(t, err, "context_capacity")
			req := topicTestRequest(1)
			req.TopicContext.Events[0].Text = strings.Repeat("mandatory constraint ", 2000)
			_, err = a.DeliverMessage(context.Background(), req)
			assertExecutionCode(t, err, "context_capacity")
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatal("native ran with unreadable required context")
			}
			req.TopicContext.ReaderAvailable = true
			req.ExecutionContext = NewExecutionContext("/local/clawsynapse", "http://127.0.0.1:1", "test-bot", "peer", req.RequestID, req.RunID, "conversation", "")
			req.ExecutionContext.ContextToken = "SECRET_CONTEXT_TOKEN"
			result, err := a.DeliverMessage(context.Background(), req)
			if err != nil || result.TopicReceipt == nil || result.TopicReceipt.RequiredReadThrough != 2 {
				t.Fatalf("authorized deferred input failed: %+v %v", result, err)
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			var invocation struct {
				Input        topicInput `json:"input"`
				ContextToken string     `json:"contextToken"`
			}
			if err := json.Unmarshal(data, &invocation); err != nil {
				t.Fatal(err)
			}
			manifest := invocation.Input.RequiredContext
			if manifest == nil || manifest.ThroughRevision != 2 || manifest.AfterRevision != 0 || manifest.EventCount != 2 || len(invocation.Input.PublicContext) != 0 || manifest.Digest != protocol.TopicEventsDigest(req.TopicContext.Events) || invocation.ContextToken != "SECRET_CONTEXT_TOKEN" {
				t.Fatal("deferred range, digest, or local-only reader authorization lost")
			}
			input, _ := json.Marshal(invocation.Input)
			if strings.Contains(string(input), "SECRET_CONTEXT_TOKEN") || len(input) > maxTopicInputBytes {
				t.Fatal("credential leaked or input exceeds capacity")
			}
			t.Setenv("CLI_TOPIC_TURN", "2")
			next := topicTestRequest(2)
			next.TopicContext.Events[0].Text = strings.Repeat("new mandatory constraint ", 2000)
			next.TopicContext.ReaderAvailable = true
			next.ExecutionContext = NewExecutionContext("/local/clawsynapse", "http://127.0.0.1:1", "test-bot", "peer", next.RequestID, next.RunID, "conversation", "")
			next.ExecutionContext.ContextToken = "NEXT_CONTEXT_TOKEN"
			result, err = a.DeliverMessage(context.Background(), next)
			if err != nil || result.TopicReceipt == nil || result.TopicReceipt.RequiredReadThrough != 5 {
				t.Fatalf("deferred resume failed: %+v %v", result, err)
			}
			data, err = os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &invocation); err != nil {
				t.Fatal(err)
			}
			if invocation.Input.RequiredContext.AfterRevision != 3 {
				t.Fatal("reader restarted at zero and could duplicate native-known inputs")
			}
		})
	}
}

func TestUnsupportedTopicAdapterDoesNotAdvertiseOrPretendReaderSupport(t *testing.T) {
	a, err := newCLIAdapter("codex", "test-bot", "binding", privateTestDir(t), "", nil, store.NewFSStore(privateTestDir(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.TopicContextIdentity() != "" {
		t.Fatal("unsupported native tool advertised topic support")
	}
	_, err = a.DeliverMessage(context.Background(), topicTestRequest(1))
	assertExecutionCode(t, err, "context_unsupported")
}

func TestTopicOpenCodeResumesWithLongPrivateNativeHistory(t *testing.T) {
	a, _, _ := topicTestAdapter(t, "opencode", "topic-long-history")
	for turn := 1; turn <= 2; turn++ {
		t.Setenv("CLI_TOPIC_TURN", strconv.Itoa(turn))
		result, err := a.DeliverMessage(context.Background(), topicTestRequest(turn))
		if err != nil || result.TopicReceipt == nil {
			t.Fatalf("long native conversation lost verified continuity: %+v %v", result, err)
		}
	}
}

func TestTopicProviderFailureAfterPersistedInputIsIndeterminate(t *testing.T) {
	for _, product := range []string{"pi", "omp", "opencode"} {
		t.Run(product, func(t *testing.T) {
			a, _, statePath := topicTestAdapter(t, product, "topic-provider-error")
			result, err := a.DeliverMessage(context.Background(), topicTestRequest(1))
			failure := assertExecutionCode(t, err, "provider_auth")
			if result != nil || !failure.Indeterminate {
				t.Fatal("post-spawn provider error was certain despite persisted input")
			}
			data, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "current instruction 1") {
				t.Fatal("fixture did not persist user input before provider failure")
			}
		})
	}
}
