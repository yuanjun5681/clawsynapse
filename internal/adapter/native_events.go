package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// The first answer is published immediately. Later tiny deltas are coalesced
// into bounded batches, and the terminal event flushes the tail.
type answerEmitter struct {
	emit      func(OutputEvent) error
	pending   []byte
	published bool
}

func (e *answerEmitter) add(text string) error {
	if e.emit == nil {
		return nil
	}
	for len(text) > 0 {
		n := min(maxDeltaBytes-len(e.pending), len(text))
		if n < len(text) {
			for n > 0 && !utf8.RuneStart(text[n]) {
				n--
			}
		}
		if n == 0 {
			if err := e.flush(); err != nil {
				return err
			}
			continue
		}
		e.pending = append(e.pending, text[:n]...)
		text = text[n:]
		if !e.published || len(e.pending) >= maxDeltaBytes-3 {
			if err := e.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *answerEmitter) flush() error {
	if len(e.pending) == 0 {
		return nil
	}
	if err := e.emit(OutputEvent{Text: string(e.pending)}); err != nil {
		return &ExecutionError{Code: "emit_failed", Stage: "delivery", Indeterminate: true, Err: err}
	}
	e.pending = e.pending[:0]
	e.published = true
	return nil
}

type nativeStream struct {
	product   string
	emitter   *answerEmitter
	sessionID string
	messageID string
	completed bool
	items     map[string]string
	reply     strings.Builder
	pi        *piStreamState
}

func (s *nativeStream) consume(line []byte) error {
	if s.product == "codex" {
		return s.consumeCodex(line)
	}
	if s.product == "pi" || s.product == "omp" {
		return s.consumePiFamily(line)
	}
	return s.consumeOpenCode(line)
}

func malformedNative() error {
	return &ExecutionError{Code: "native_schema", Stage: "decode", Indeterminate: true, Err: errors.New("native CLI emitted unsupported or malformed structured output")}
}

func nativeFailure(raw json.RawMessage) error {
	code := "native_failed"
	lower := strings.ToLower(string(raw))
	if strings.Contains(lower, "session") && (strings.Contains(lower, "not found") || strings.Contains(lower, "unknown") || strings.Contains(lower, "no recorded") || strings.Contains(lower, "no such")) {
		code = "session_unavailable"
	}
	return &ExecutionError{Code: code, Stage: "execution", Indeterminate: true, Err: errors.New("native CLI reported failure; if the prior session is unavailable, create a new session explicitly")}
}

func hasNativeError(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte(`""`)) && !bytes.Equal(raw, []byte("false"))
}

func (s *nativeStream) setSession(id string) error {
	if id == "" {
		return nil
	}
	if !safeSessionID(id) || s.sessionID != "" && s.sessionID != id {
		return malformedNative()
	}
	s.sessionID = id
	return nil
}

func (s *nativeStream) answer(text string) error {
	if s.completed {
		return malformedNative()
	}
	if s.emitter.emit == nil {
		s.reply.WriteString(text)
	}
	return s.emitter.add(text)
}

func (s *nativeStream) consumeCodex(line []byte) error {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Item     *struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type == "" {
		return malformedNative()
	}
	if event.Type == "error" || event.Type == "turn.failed" || hasNativeError(event.Error) {
		if err := s.emitter.flush(); err != nil {
			return err
		}
		return nativeFailure(line)
	}
	switch event.Type {
	case "thread.started":
		if event.ThreadID == "" {
			return malformedNative()
		}
		return s.setSession(event.ThreadID)
	case "turn.started":
		if s.completed {
			return malformedNative()
		}
	case "turn.completed":
		if s.completed {
			return malformedNative()
		}
		s.completed = true
		return s.emitter.flush()
	case "item.started", "item.updated", "item.completed":
		if event.Item == nil {
			return malformedNative()
		}
		if event.Item.Type != "agent_message" {
			return nil
		}
		text := event.Item.Text
		if event.Item.ID != "" {
			previous := s.items[event.Item.ID]
			if !strings.HasPrefix(text, previous) {
				return malformedNative()
			}
			s.items[event.Item.ID] = text
			text = text[len(previous):]
		} else if event.Type != "item.completed" {
			return malformedNative()
		}
		return s.answer(text)
	}
	return nil
}

func (s *nativeStream) consumeOpenCode(line []byte) error {
	var event struct {
		Type      string          `json:"type"`
		SessionID string          `json:"sessionID"`
		Text      string          `json:"text"`
		Reason    string          `json:"reason"`
		Error     json.RawMessage `json:"error"`
		Part      *struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Text      string `json:"text"`
			Reason    string `json:"reason"`
			MessageID string `json:"messageID"`
		} `json:"part"`
	}
	if json.Unmarshal(line, &event) != nil || event.Type == "" {
		return malformedNative()
	}
	if err := s.setSession(event.SessionID); err != nil {
		return err
	}
	if event.Type == "error" || hasNativeError(event.Error) {
		if err := s.emitter.flush(); err != nil {
			return err
		}
		return nativeFailure(line)
	}
	switch event.Type {
	case "step_start":
		if event.Part != nil && event.Part.MessageID != "" {
			s.messageID = event.Part.MessageID
		}
		s.completed = false
	case "text":
		text := event.Text
		if event.Part != nil {
			// An allowlist, not a denylist: thinking/reasoning/tool parts can
			// never become an answer even if they contain a text property.
			if event.Part.Type != "text" {
				return nil
			}
			if event.Part.MessageID != "" {
				s.messageID = event.Part.MessageID
			}
			text = event.Part.Text
			if event.Part.ID != "" {
				previous := s.items[event.Part.ID]
				if !strings.HasPrefix(text, previous) {
					return malformedNative()
				}
				s.items[event.Part.ID] = text
				text = text[len(previous):]
			}
		}
		return s.answer(text)
	case "step_finish":
		reason := event.Reason
		if event.Part != nil {
			if event.Part.Type != "step-finish" {
				return malformedNative()
			}
			reason = event.Part.Reason
		}
		switch reason {
		case "stop":
			s.completed = true
			return s.emitter.flush()
		case "tool-calls":
			s.completed = false
			return s.emitter.flush()
		case "length", "error", "content-filter", "unknown":
			if err := s.emitter.flush(); err != nil {
				return err
			}
			return nativeFailure(line)
		default:
			return malformedNative()
		}
	}
	return nil
}

func (s *nativeStream) result() (*DeliverMessageResult, error) {
	if !s.completed {
		return nil, &ExecutionError{Code: "incomplete_output", Stage: "completion", Indeterminate: true, Err: errors.New("native CLI exited without a successful terminal event")}
	}
	return &DeliverMessageResult{Success: true, Accepted: true, Completed: true, SessionID: s.sessionID, Reply: s.reply.String()}, nil
}

type openClawResult struct {
	Payloads []struct {
		Text        string `json:"text"`
		IsError     bool   `json:"isError"`
		IsReasoning bool   `json:"isReasoning"`
	} `json:"payloads"`
	Meta *struct {
		Aborted             bool            `json:"aborted"`
		Yielded             bool            `json:"yielded"`
		ContinuationPending bool            `json:"continuationPending"`
		StopReason          string          `json:"stopReason"`
		Error               json.RawMessage `json:"error"`
		AgentMeta           *struct {
			SessionID string `json:"sessionId"`
		} `json:"agentMeta"`
	} `json:"meta"`
}

func consumeOpenClaw(data []byte) (*DeliverMessageResult, error) {
	var response struct {
		RunID  string          `json:"runId"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
		Result *openClawResult `json:"result"`
	}
	if json.Unmarshal(data, &response) != nil {
		return nil, malformedNative()
	}
	if hasNativeError(response.Error) {
		return nil, nativeFailure(response.Error)
	}
	body := response.Result
	if response.Status != "ok" {
		return nil, nativeFailure(data)
	}
	if body == nil {
		return nil, malformedNative()
	}
	if body.Meta != nil {
		meta := body.Meta
		if meta.Aborted || meta.Yielded || meta.ContinuationPending || hasNativeError(meta.Error) {
			return nil, nativeFailure(data)
		}
		if meta.StopReason != "" {
			switch meta.StopReason {
			case "stop", "completed", "end_turn":
			default:
				return nil, nativeFailure(data)
			}
		}
	}
	var reply strings.Builder
	for _, part := range body.Payloads {
		if part.IsError {
			return nil, nativeFailure(data)
		}
		if !part.IsReasoning {
			reply.WriteString(part.Text)
		}
	}
	result := &DeliverMessageResult{Success: true, Accepted: true, Completed: true, RunID: response.RunID, Reply: reply.String()}
	if body.Meta != nil && body.Meta.AgentMeta != nil {
		result.SessionID = body.Meta.AgentMeta.SessionID
	}
	return result, nil
}
