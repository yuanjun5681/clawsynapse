package adapter

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeStructuredErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name, product, event, code, stage string
		indeterminate                     bool
	}{
		{"captured invalid model", "opencode", `{"type":"error","sessionID":"","error":{"type":"unknown","message":"Invalid model reference: GLM-5.3-Flash"}}`, "invalid_model", "validation", false},
		{"unknown is not a missing session", "opencode", `{"type":"error","sessionID":"ses_exists","error":{"type":"unknown","message":"unexpected provider failure"}}`, "native_failed", "execution", true},
		{"unrelated prompt is ignored", "opencode", `{"type":"error","text":"unknown session: not found PRIVATE_PROMPT","error":{"type":"unknown","message":"failure PRIVATE_KEY"}}`, "native_failed", "execution", true},
		{"session missing", "codex", `{"type":"turn.failed","error":{"message":"unknown session: not found PRIVATE_KEY"}}`, "session_unavailable", "session_load", false},
		{"session error code", "opencode", `{"type":"error","error":{"code":"session_not_found","message":"PRIVATE_KEY"}}`, "session_unavailable", "session_load", false},
		{"free limit code", "opencode", `{"type":"error","error":{"type":"provider.free-limit","message":"PRIVATE_KEY"}}`, "provider_quota", "provider", false},
		{"free limit message", "opencode", `{"type":"error","error":{"type":"unknown","message":"provider.free-limit: PRIVATE_KEY"}}`, "provider_quota", "provider", false},
		{"nested quota", "opencode", `{"type":"error","error":{"name":"APIError","data":{"error":{"code":"insufficient_quota","message":"PRIVATE_KEY"}}}}`, "provider_quota", "provider", false},
		{"provider auth", "opencode", `{"type":"error","error":{"type":"provider.auth","message":"PRIVATE_KEY"}}`, "provider_auth", "authentication", false},
		{"HTTP authentication", "opencode", `{"type":"error","error":{"name":"APIError","data":{"statusCode":401,"message":"PRIVATE_KEY"}}}`, "provider_auth", "authentication", false},
		{"transport is uncertain", "codex", `{"type":"error","message":"connection reset by peer PRIVATE_KEY"}`, "native_transport", "transport", true},
		{"unclassified runtime", "codex", `{"type":"turn.failed","error":{"message":"runtime crashed PRIVATE_KEY"}}`, "native_failed", "execution", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &nativeStream{product: tc.product, emitter: &answerEmitter{}, items: make(map[string]string)}
			failure := assertExecutionCode(t, stream.consume([]byte(tc.event)), tc.code)
			if failure.Stage != tc.stage || failure.Indeterminate != tc.indeterminate {
				t.Fatalf("wrong native classification: %+v", failure)
			}
			if failure.SafeMessage() == "" || strings.Contains(failure.Error()+failure.SafeMessage(), "PRIVATE_") || strings.Contains(failure.SafeMessage(), "GLM-") {
				t.Fatalf("unsafe or missing diagnostic: %v / %q", failure, failure.SafeMessage())
			}
		})
	}
}

func TestNativeFailurePreservesPossibleEffects(t *testing.T) {
	for _, tc := range []struct {
		name, product, activity string
	}{
		{"buffered answer", "opencode", `{"type":"text","part":{"id":"a","type":"text","text":"partial"}}`},
		{"tool without answer", "opencode", `{"type":"tool_use","part":{"type":"tool","state":{"status":"running"}}}`},
		{"step started", "opencode", `{"type":"step_start","part":{"type":"step-start"}}`},
		{"codex tool", "codex", `{"type":"item.started","item":{"id":"a","type":"command_execution"}}`},
		{"codex inference", "codex", `{"type":"turn.started"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := &nativeStream{product: tc.product, emitter: &answerEmitter{}, items: make(map[string]string)}
			if err := stream.consume([]byte(tc.activity)); err != nil {
				t.Fatal(err)
			}
			failure := assertExecutionCode(t, stream.consume([]byte(`{"type":"error","error":{"message":"Invalid model reference: PRIVATE_KEY"}}`)), "invalid_model")
			if !failure.Indeterminate {
				t.Fatal("prior inference/output/tool activity was incorrectly reported as deterministic")
			}
		})
	}
}

func TestOpenClawFailureIgnoresPayloadText(t *testing.T) {
	_, err := consumeOpenClaw([]byte(`{"status":"error","result":{"payloads":[{"text":"unknown session: not found PRIVATE_PROMPT"}]},"error":{"type":"unknown","message":"unexpected PRIVATE_KEY"}}`))
	failure := assertExecutionCode(t, err, "native_failed")
	if !failure.Indeterminate || strings.Contains(failure.Error(), "PRIVATE_") {
		t.Fatalf("unsafe OpenClaw failure: %v", failure)
	}
}

func TestPiNativeErrorsUseOnlyErrorFields(t *testing.T) {
	stream := &nativeStream{product: "pi", emitter: &answerEmitter{}, items: make(map[string]string)}
	if err := stream.consume([]byte(`{"type":"session","id":"11111111-1111-4111-8111-111111111111"}`)); err != nil {
		t.Fatal(err)
	}
	err := stream.consume([]byte(`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Invalid model reference: PRIVATE_KEY","content":[{"type":"text","text":"unknown session: not found PRIVATE_PROMPT"}]}}`))
	failure := assertExecutionCode(t, err, "invalid_model")
	if strings.Contains(failure.Error(), "PRIVATE_") {
		t.Fatalf("unsafe pi failure: %v", failure)
	}
}

func TestOpenCodeCapturedInvalidModelIsNotRetried(t *testing.T) {
	t.Setenv("CLI_JSON", "{\"type\":\"error\",\"sessionID\":\"\",\"error\":{\"type\":\"unknown\",\"message\":\"Invalid model reference: GLM-5.3-Flash\"}}\n")
	a, err := NewOpenCodeAdapter(testOpenCodeConfig(t, OpenCodeConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	factory := helperCommand("opencode", "native-json", "", "")
	calls := 0
	a.command = func(ctx context.Context, args ...string) *exec.Cmd {
		calls++
		return factory(ctx, args...)
	}
	result, err := a.DeliverMessage(context.Background(), DeliverMessageRequest{Message: "PRIVATE_PROMPT", SessionKey: "conversation"})
	failure := assertExecutionCode(t, err, "invalid_model")
	if result != nil || failure.Stage != "validation" || failure.Indeterminate || calls != 1 || failure.SafeMessage() == "" || strings.Contains(failure.Error(), "PRIVATE_") {
		t.Fatalf("invalid model was retried, leaked or misclassified: calls=%d result=%+v failure=%v", calls, result, failure)
	}
}
