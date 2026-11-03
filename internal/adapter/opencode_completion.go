package adapter

import (
	"context"
	"encoding/json"
	"errors"
)

// confirmOpenCodeTurn verifies the persisted result of the exact turn observed
// in the stream. Export is read-only and sanitized; it cannot execute a new task.
func (a *cliAdapter) confirmOpenCodeTurn(ctx context.Context, stream *nativeStream) error {
	out, err := runProcess(ctx, a.runCommand, []string{"session", "export", "--standalone", "--sanitize", stream.sessionID}, "", nil, statusTimeout)
	if err != nil {
		return &ExecutionError{Code: "completion_unavailable", Stage: "completion", Indeterminate: true, Err: err}
	}
	var state struct {
		Info struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
		} `json:"info"`
		Messages []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Finish  string `json:"finish"`
			Outcome string `json:"outcome"`
			Time    struct {
				Completed int64 `json:"completed"`
			} `json:"time"`
		} `json:"messages"`
	}
	if json.Unmarshal(out, &state) != nil || state.Info.ID != stream.sessionID {
		return malformedNative()
	}
	if state.Info.Outcome != "succeeded" || len(state.Messages) == 0 {
		return incompleteOpenCodeTurn()
	}
	final := state.Messages[len(state.Messages)-1]
	if final.Type != "idle" || final.Outcome != "succeeded" {
		return incompleteOpenCodeTurn()
	}
	for i := len(state.Messages) - 2; i >= 0; i-- {
		message := state.Messages[i]
		if message.Type != "assistant" {
			continue
		}
		if message.ID != stream.messageID || message.Finish != "stop" || message.Time.Completed <= 0 {
			return incompleteOpenCodeTurn()
		}
		stream.completed = true
		return nil
	}
	return incompleteOpenCodeTurn()
}
func incompleteOpenCodeTurn() error {
	return &ExecutionError{Code: "incomplete_output", Stage: "completion", Indeterminate: true, Err: errors.New("native session does not confirm completion of the streamed assistant turn")}
}
