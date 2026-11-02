package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"clawsynapse/internal/store"
)

const piOMPSessionID = "75b8b7c4-7858-4ed9-935e-ac881ab8d17a"

func piOMPJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func piOMPMessage(role, text, stopReason string) map[string]any {
	return map[string]any{
		"role": role,
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "PRIVATE_THINKING"},
			map[string]any{"type": "text", "text": text},
		},
		"stopReason": stopReason,
	}
}

func piOMPStart(t *testing.T) []string {
	t.Helper()
	return []string{
		piOMPJSON(t, map[string]any{"type": "session", "id": piOMPSessionID}),
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		piOMPJSON(t, map[string]any{"type": "message_start", "message": piOMPMessage("assistant", "", "")}),
	}
}

func piOMPDelta(t *testing.T, text string) string {
	t.Helper()
	return piOMPJSON(t, map[string]any{
		"type":                  "message_update",
		"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 1, "delta": text},
	})
}

func piOMPEnd(t *testing.T, product, text, reason string) []string {
	t.Helper()
	message := piOMPMessage("assistant", text, reason)
	lines := []string{
		piOMPJSON(t, map[string]any{"type": "message_end", "message": message}),
		piOMPJSON(t, map[string]any{"type": "turn_end", "message": message, "toolResults": []any{}}),
		piOMPJSON(t, map[string]any{"type": "agent_end", "messages": []any{map[string]any{"role": "user", "content": "PRIVATE_USER"}, message}, "isTerminal": true, "yielded": true}),
	}
	if product == "pi" {
		lines = append(lines, `{"type":"agent_settled","aborted":false}`)
	}
	return lines
}

func piOMPTestAdapter(t *testing.T, product string, sessions *store.FSStore) *cliAdapter {
	t.Helper()
	a, err := newCLIAdapter(product, "test-bot", "binding-one", privateTestDir(t), "provider/test-model", nil, sessions, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.command = helperCommand(product, "native-json", "", "")
	return a
}

func piOMPWriteSession(t *testing.T, a *cliAdapter, filename, headerID string) string {
	t.Helper()
	dir := filepath.Join(a.workspace, ".clawsynapse", a.product+"-sessions")
	if err := store.EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filename)
	header := ""
	if a.product == "omp" {
		header = "{\"type\":\"title\",\"v\":1,\"title\":\"\",\"updatedAt\":\"2026-11-02T10:00:00.000Z\",\"pad\":\"   \"}\n"
	}
	if err := os.WriteFile(path, []byte(header+piOMPJSON(t, map[string]any{"type": "session", "id": headerID})+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPiOMPAnswersExcludePrivateEventsAndDuplicateSnapshots(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				name := "returned"
				if streaming {
					name = "streamed"
				}
				t.Run(name, func(t *testing.T) {
					a := piOMPTestAdapter(t, product, nil)
					piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
					lines := piOMPStart(t)
					lines = append(lines,
						piOMPJSON(t, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "thinking_delta", "delta": "PRIVATE_THINKING"}}),
						piOMPJSON(t, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "toolcall_delta", "delta": "PRIVATE_TOOL_ARGUMENTS"}}),
						`{"type":"tool_execution_start","toolCallId":"call-1","toolName":"read","args":{"path":"PRIVATE_PATH"}}`,
						`{"type":"tool_execution_end","toolCallId":"call-1","result":{"content":[{"type":"text","text":"PRIVATE_TOOL_OUTPUT"}]},"isError":false}`,
						piOMPDelta(t, "Hello "), piOMPDelta(t, "世界"),
						piOMPJSON(t, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_end", "contentIndex": 1, "content": "Hello 世界"}}),
					)
					lines = append(lines, piOMPEnd(t, product, "Hello 世界", "stop")...)
					t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
					var answer strings.Builder
					req := DeliverMessageRequest{Message: "task"}
					if streaming {
						req.Emit = func(event OutputEvent) error { answer.WriteString(event.Text); return nil }
					}
					result, err := a.DeliverMessage(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					if streaming {
						if result.Reply != "" {
							t.Fatalf("streamed answer was also returned: %q", result.Reply)
						}
					} else {
						answer.WriteString(result.Reply)
					}
					if answer.String() != "Hello 世界" || result.SessionID != piOMPSessionID {
						t.Fatalf("answer/session = %q / %q", answer.String(), result.SessionID)
					}
				})
			}
		})
	}
}

func TestPiOMPToolUseTurnDoesNotFinishTheAnswer(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			a := piOMPTestAdapter(t, product, nil)
			piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
			toolTurn := piOMPMessage("assistant", "Checking. ", "toolUse")
			lines := append(piOMPStart(t), piOMPDelta(t, "Checking. "),
				piOMPJSON(t, map[string]any{"type": "message_end", "message": toolTurn}),
				piOMPJSON(t, map[string]any{"type": "message_start", "message": piOMPMessage("toolResult", "PRIVATE_TOOL_OUTPUT", "")}),
				piOMPJSON(t, map[string]any{"type": "message_end", "message": piOMPMessage("toolResult", "PRIVATE_TOOL_OUTPUT", "")}),
				piOMPJSON(t, map[string]any{"type": "turn_end", "message": toolTurn, "toolResults": []any{piOMPMessage("toolResult", "PRIVATE_TOOL_OUTPUT", "")}}),
				`{"type":"turn_start"}`,
				piOMPJSON(t, map[string]any{"type": "message_start", "message": piOMPMessage("assistant", "", "")}),
				piOMPDelta(t, "Done."),
			)
			lines = append(lines, piOMPEnd(t, product, "Done.", "stop")...)
			t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
			result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Reply != "Checking. Done." {
				t.Fatalf("tool turn answer = %q", result.Reply)
			}
		})
	}
}

func TestPiOMPRejectIncompleteOrFailedTurnsDespiteAnswerText(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			cases := map[string][]string{
				"delta-only":                     {},
				"message-stop-without-agent-end": {piOMPJSON(t, map[string]any{"type": "message_end", "message": piOMPMessage("assistant", "partial", "stop")})},
				"agent-end-without-stop":         {`{"type":"agent_end","messages":[],"isTerminal":true,"yielded":true}`},
				"explicit-error":                 {`{"type":"error","error":{"message":"provider unavailable"}}`},
			}
			cases["settled-aborted"] = append(piOMPEnd(t, product, "partial", "stop"), `{"type":"agent_settled","aborted":true}`)
			cases["late-error"] = append(piOMPEnd(t, product, "partial", "stop"), `{"type":"error","error":{"message":"provider unavailable"}}`)
			for _, reason := range []string{"error", "aborted", "length", "toolUse"} {
				cases[reason] = piOMPEnd(t, product, "partial", reason)
			}
			for name, tail := range cases {
				t.Run(name, func(t *testing.T) {
					a := piOMPTestAdapter(t, product, nil)
					lines := append(piOMPStart(t), piOMPDelta(t, "partial"))
					lines = append(lines, tail...)
					t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
					result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task"})
					if err == nil || result != nil {
						t.Fatalf("unsuccessful native turn accepted: result=%+v err=%v", result, err)
					}
				})
			}
		})
	}
}

func TestPiOMPSnapshotOnlyAnswerIsPublishedOnce(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				name := "returned"
				if streaming {
					name = "streamed"
				}
				t.Run(name, func(t *testing.T) {
					a := piOMPTestAdapter(t, product, nil)
					piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
					lines := []string{
						piOMPJSON(t, map[string]any{"type": "session", "id": piOMPSessionID}),
						`{"type":"agent_start"}`,
						piOMPJSON(t, map[string]any{"type": "message_start", "message": map[string]any{"role": "user", "content": "PRIVATE_USER"}}),
						piOMPJSON(t, map[string]any{"type": "message_end", "message": map[string]any{"role": "user", "content": "PRIVATE_USER"}}),
						`{"type":"turn_start"}`,
						piOMPJSON(t, map[string]any{"type": "message_start", "message": piOMPMessage("assistant", "", "")}),
					}
					lines = append(lines, piOMPEnd(t, product, "snapshot answer", "stop")...)
					t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
					var answer strings.Builder
					req := DeliverMessageRequest{Message: "task"}
					if streaming {
						req.Emit = func(event OutputEvent) error { answer.WriteString(event.Text); return nil }
					}
					result, err := a.DeliverMessage(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					if streaming && result.Reply != "" {
						t.Fatalf("streamed snapshot also returned: %q", result.Reply)
					}
					answer.WriteString(result.Reply)
					if answer.String() != "snapshot answer" {
						t.Fatalf("snapshot answer = %q", answer.String())
					}
				})
			}
		})
	}
}

func TestPiOMPSavedSessionCannotSilentlyStartOver(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			sessions := store.NewFSStore(privateTestDir(t))
			a := piOMPTestAdapter(t, product, sessions)
			path := piOMPWriteSession(t, a, "2026-11-02T10-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
			lines := append(piOMPStart(t), piOMPDelta(t, "saved answer"))
			lines = append(lines, piOMPEnd(t, product, "saved answer", "stop")...)
			t.Setenv("CLI_JSON", strings.Join(lines, "\n")+"\n")
			req := DeliverMessageRequest{Message: "task", SessionKey: "conversation"}
			result, err := a.DeliverMessage(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.Reply != "saved answer" || result.SessionID != piOMPSessionID {
				t.Fatalf("first answer/session = %q / %q", result.Reply, result.SessionID)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			// Reconstructing the binding must recover its mapping, not silently
			// create a new conversation when the native history is unavailable.
			resumed, err := newCLIAdapter(product, "test-bot", "binding-one", a.workspace, "provider/test-model", nil, sessions, nil)
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(privateTestDir(t), "invoked")
			resumed.command = helperCommand(product, "native-json", record, "")
			result, err = resumed.DeliverMessage(context.Background(), req)
			if err == nil || result != nil {
				t.Fatalf("lost saved history silently restarted: result=%+v err=%v", result, err)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("CLI ran after saved history disappeared: %v", err)
			}
		})
	}
}

func TestPiOMPRejectNamedAgentsBeforeLaunchingCLI(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			a := piOMPTestAdapter(t, product, nil)
			// Even a locally allowlisted name cannot enable routing these CLIs lack.
			a.allowedAgents["named-agent"] = struct{}{}
			record := filepath.Join(privateTestDir(t), "invoked")
			a.command = helperCommand(product, "native-json", record, "")
			_, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", AgentID: "named-agent"})
			assertExecutionCode(t, err, "unsupported_agent")
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("CLI ran for unsupported agent: %v", err)
			}
		})
	}
}

func TestPiOMPMappedSessionMustResolveInsidePrivateNativeDirectory(t *testing.T) {
	for _, product := range []string{"pi", "omp"} {
		t.Run(product, func(t *testing.T) {
			for _, scenario := range []string{"missing", "wrong-header", "corrupt", "ambiguous", "symlink", "outside-directory"} {
				t.Run(scenario, func(t *testing.T) {
					sessions := store.NewFSStore(privateTestDir(t))
					a := piOMPTestAdapter(t, product, sessions)
					if err := sessions.SaveSessionState(store.SessionState{Adapter: product, SessionKey: a.scopedSessionKey("conversation"), SessionID: piOMPSessionID}); err != nil {
						t.Fatal(err)
					}
					filename := "2026-11-02T10-00-00-000Z_" + piOMPSessionID + ".jsonl"
					switch scenario {
					case "wrong-header":
						piOMPWriteSession(t, a, filename, "f7fdf275-8b86-42be-a29a-e40e7418d105")
					case "corrupt":
						path := piOMPWriteSession(t, a, filename, piOMPSessionID)
						if err := os.WriteFile(path, []byte("not json\n"), 0600); err != nil {
							t.Fatal(err)
						}
					case "ambiguous":
						piOMPWriteSession(t, a, filename, piOMPSessionID)
						piOMPWriteSession(t, a, "2026-11-02T11-00-00-000Z_"+piOMPSessionID+".jsonl", piOMPSessionID)
					case "symlink", "outside-directory":
						outside := filepath.Join(privateTestDir(t), filename)
						if err := os.WriteFile(outside, []byte(piOMPJSON(t, map[string]any{"type": "session", "id": piOMPSessionID})+"\n"), 0600); err != nil {
							t.Fatal(err)
						}
						if scenario == "symlink" {
							dir := filepath.Join(a.workspace, ".clawsynapse", product+"-sessions")
							if err := store.EnsurePrivateDir(dir); err != nil {
								t.Fatal(err)
							}
							if err := os.Symlink(outside, filepath.Join(dir, filename)); err != nil {
								t.Fatal(err)
							}
						}
					}
					record := filepath.Join(privateTestDir(t), "invoked")
					a.command = helperCommand(product, "native-json", record, "")
					result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "task", SessionKey: "conversation"})
					if err == nil || result != nil {
						t.Fatalf("unsafe mapped session accepted: result=%+v err=%v", result, err)
					}
					if _, err := os.Stat(record); !os.IsNotExist(err) {
						t.Fatalf("CLI ran instead of failing closed: %v", err)
					}
				})
			}
		})
	}
}
