package adapter

import "encoding/json"

// pi and omp share assistant events, but neither an exit code nor an answer
// delta proves completion. A successful assistant terminal must be followed by
// agent_end; later errors/aborts still invalidate the invocation.
type piStreamState struct {
	expectedSession string
	role            string
	deltas          bool
	terminal        bool
	stopReason      string
}

type piMessage struct {
	Role         string          `json:"role"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Content      json.RawMessage `json:"content"`
}

func (s *nativeStream) consumePiFamily(line []byte) error {
	var event struct {
		Type       string          `json:"type"`
		ID         string          `json:"id"`
		Error      json.RawMessage `json:"error"`
		Aborted    bool            `json:"aborted"`
		WillRetry  bool            `json:"willRetry"`
		IsTerminal *bool           `json:"isTerminal"`
		Message    *piMessage      `json:"message"`
		Messages   []struct {
			Role         string `json:"role"`
			StopReason   string `json:"stopReason"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"messages"`
		AssistantMessageEvent *struct {
			Type   string          `json:"type"`
			Delta  *string         `json:"delta"`
			Reason string          `json:"reason"`
			Error  json.RawMessage `json:"error"`
		} `json:"assistantMessageEvent"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type == "" {
		return malformedNative()
	}
	if event.Type == "error" || event.Type == "aborted" || event.Aborted || hasNativeError(event.Error) {
		return s.failPiNative(event.Error)
	}
	if s.pi == nil {
		s.pi = &piStreamState{}
	}
	state := s.pi
	if event.Type == "session" {
		if s.sessionID != "" || !safePiSessionID(event.ID) || state.expectedSession != "" && event.ID != state.expectedSession {
			return malformedNative()
		}
		return s.setSession(event.ID)
	}
	if s.sessionID == "" {
		return malformedNative()
	}
	switch event.Type {
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
		s.active = true
	case "agent_start", "turn_start":
		s.active = true
		state.role = ""
		state.deltas = false
		state.terminal = false
		state.stopReason = ""
		if s.finalOnly {
			s.candidate.Reset()
			s.candidateReady = false
		}
		s.completed = false
	case "message_start":
		if event.Message == nil || event.Message.Role == "" || s.completed {
			return malformedNative()
		}
		state.role = event.Message.Role
		if state.role != "user" {
			s.active = true
		}
		if state.role == "assistant" {
			state.deltas = false
			state.terminal = false
			state.stopReason = ""
			if s.finalOnly {
				s.candidate.Reset()
				s.candidateReady = false
			}
		}
	case "message_update":
		update := event.AssistantMessageEvent
		if update == nil || update.Type == "" {
			return malformedNative()
		}
		if update.Type == "error" || hasNativeError(update.Error) || update.Type == "done" && update.Reason != "stop" && update.Reason != "toolUse" {
			if hasNativeError(update.Error) {
				return s.failPiNative(update.Error)
			}
			if event.Message != nil {
				return s.failPiMessage(event.Message.ErrorMessage)
			}
			return s.failPiNative(nil)
		}
		s.active = true
		// Only an assistant's incremental text may stream. Thinking and tool
		// deltas are ignored even when they carry a delta/text property.
		if update.Type != "text_delta" {
			return nil
		}
		if event.Message != nil && event.Message.Role != "assistant" {
			return nil
		}
		if state.role != "assistant" {
			if state.role != "" {
				return nil
			}
			return malformedNative()
		}
		if state.terminal || update.Delta == nil {
			return malformedNative()
		}
		state.deltas = true
		return s.answer(*update.Delta)
	case "message_end":
		if event.Message == nil || event.Message.Role == "" {
			return malformedNative()
		}
		if event.Message.Role == "assistant" {
			return s.finishPiMessage(event.Message)
		}
	case "turn_end":
		if event.Message == nil || event.Message.Role != "assistant" {
			return malformedNative()
		}
		return s.finishPiMessage(event.Message)
	case "agent_end":
		// agent_end includes snapshots for the entire run. Inspect the last
		// assistant terminal but never publish these snapshots a second time.
		if len(event.Messages) > 0 {
			last := event.Messages[len(event.Messages)-1]
			if last.Role != "assistant" {
				return malformedNative()
			}
			if last.StopReason != "stop" || last.ErrorMessage != "" {
				return s.failPiMessage(last.ErrorMessage)
			}
		}
		if !state.terminal || state.stopReason != "stop" {
			return nil
		}
		if event.WillRetry || event.IsTerminal != nil && !*event.IsTerminal {
			s.completed = false
			return nil
		}
		// omp's yielded=true means control was returned, not an abort.
		s.completed = true
		return s.emitter.flush()
	}
	return nil
}

func (s *nativeStream) finishPiMessage(message *piMessage) error {
	if s.completed {
		return malformedNative()
	}
	if message.ErrorMessage != "" {
		return s.failPiMessage(message.ErrorMessage)
	}
	switch message.StopReason {
	case "stop", "toolUse":
	case "":
		return malformedNative()
	default:
		return s.failPiNative(nil)
	}
	state := s.pi
	if s.finalOnly && !state.terminal {
		// The assistant terminal snapshot is the message authority. Discard
		// text from tool-use messages, including earlier streamed commentary.
		s.candidate.Reset()
		s.candidateReady = message.StopReason == "stop"
		if message.StopReason == "stop" {
			var content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(message.Content, &content) != nil {
				return malformedNative()
			}
			for _, part := range content {
				if part.Type == "text" {
					s.candidate.WriteString(part.Text)
				}
			}
		}
		state.role, state.terminal, state.stopReason = "assistant", true, message.StopReason
		return nil
	}
	if state.terminal {
		if state.stopReason != message.StopReason {
			return malformedNative()
		}
		return nil
	}
	// Some providers emit a terminal message without text_delta. That single
	// authoritative snapshot is a safe fallback; turn_end/agent_end repeats
	// never add text, and a delta-bearing message never republishes snapshots.
	if !state.deltas {
		var content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(message.Content, &content) != nil {
			return malformedNative()
		}
		for _, part := range content {
			if part.Type == "text" {
				if err := s.answer(part.Text); err != nil {
					return err
				}
			}
		}
	}
	state.role = "assistant"
	state.terminal = true
	state.stopReason = message.StopReason
	return s.emitter.flush()
}

func (s *nativeStream) failPiMessage(message string) error {
	raw, _ := json.Marshal(message)
	return s.failPiNative(raw)
}

func (s *nativeStream) failPiNative(raw json.RawMessage) error {
	if err := s.emitter.flush(); err != nil {
		return err
	}
	return nativeFailure(raw, s.active)
}
