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
	// Sticky once inference, answer or tool activity makes effects possible.
	active bool
	items  map[string]string
	reply  strings.Builder
	pi     *piStreamState
	// Workspace chat selects the last authoritative assistant message, never
	// the concatenation of text emitted across tool rounds.
	finalOnly      bool
	candidate      strings.Builder
	candidateID    string
	candidateReady bool
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

// raw must be an explicit error field, never a serialized event or result.
func nativeFailure(raw json.RawMessage, active bool) error {
	code, stage, deterministic := classifyNativeError(raw, 0)
	failure := &ExecutionError{Code: code, Stage: stage, Indeterminate: active || !deterministic}
	failure.Err = errors.New(failure.SafeMessage())
	return failure
}

func classifyNativeError(raw json.RawMessage, depth int) (string, string, bool) {
	if depth > 4 {
		return "native_failed", "execution", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return classifyNativeErrorText("", text, 0)
	}
	var detail struct {
		Code       string          `json:"code"`
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		Message    string          `json:"message"`
		StatusCode int             `json:"statusCode"`
		Data       json.RawMessage `json:"data"`
		Error      json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &detail) != nil {
		return "native_failed", "execution", false
	}
	for _, kind := range [...]string{detail.Code, detail.Type, detail.Name} {
		code, stage, deterministic := classifyNativeErrorText(kind, "", 0)
		if code != "native_failed" {
			return code, stage, deterministic
		}
	}
	for _, nested := range [...]json.RawMessage{detail.Error, detail.Data} {
		if hasNativeError(nested) {
			code, stage, deterministic := classifyNativeError(nested, depth+1)
			if code != "native_failed" {
				return code, stage, deterministic
			}
		}
	}
	return classifyNativeErrorText("", detail.Message, detail.StatusCode)
}

func classifyNativeErrorText(kind, message string, status int) (string, string, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	message = strings.ToLower(strings.TrimSpace(message))
	switch kind {
	case "invalid_model", "model_not_found", "modelnotfounderror", "provider.model-not-found":
		return "invalid_model", "validation", true
	case "provider.free-limit", "insufficient_quota", "quota_exceeded", "provider_quota":
		return "provider_quota", "provider", true
	case "provider.auth", "authentication_error", "authenticationerror", "providerautherror", "invalid_api_key", "unauthorized":
		return "provider_auth", "authentication", true
	case "session_not_found", "sessionnotfounderror", "session_unavailable", "thread_not_found":
		return "session_unavailable", "session_load", true
	case "provider.transport", "connection_error", "connectionerror", "network_error", "fetcherror":
		return "native_transport", "transport", false
	}
	switch {
	case strings.HasPrefix(message, "invalid model reference:"),
		strings.HasPrefix(message, "model not found:"):
		return "invalid_model", "validation", true
	case strings.HasPrefix(message, "provider.free-limit"),
		strings.HasPrefix(message, "free usage limit exceeded"),
		strings.HasPrefix(message, "free limit exceeded"),
		strings.HasPrefix(message, "quota exceeded"),
		strings.HasPrefix(message, "insufficient quota"):
		return "provider_quota", "provider", true
	case status == 401 || status == 403,
		strings.HasPrefix(message, "provider.auth"),
		strings.HasPrefix(message, "invalid api key"),
		strings.HasPrefix(message, "authentication failed"),
		strings.HasPrefix(message, "unauthorized"):
		return "provider_auth", "authentication", true
	case strings.HasPrefix(message, "unknown session"),
		strings.HasPrefix(message, "session not found"),
		strings.HasPrefix(message, "no such session"),
		strings.HasPrefix(message, "no recorded session"),
		strings.HasPrefix(message, "unknown thread"),
		strings.HasPrefix(message, "thread not found"),
		strings.HasPrefix(message, "no recorded rollout"):
		return "session_unavailable", "session_load", true
	case strings.HasPrefix(message, "connection reset"),
		strings.HasPrefix(message, "connection refused"),
		strings.HasPrefix(message, "connection closed"),
		strings.HasPrefix(message, "network error"),
		strings.HasPrefix(message, "fetch failed"),
		strings.HasPrefix(message, "transport error"):
		return "native_transport", "transport", false
	default:
		return "native_failed", "execution", false
	}
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
	if text != "" {
		s.active = true
	}
	if s.completed {
		return malformedNative()
	}
	if s.finalOnly {
		// Pi/omp's terminal message carries the authoritative full snapshot.
		if s.product != "pi" && s.product != "omp" {
			s.candidate.WriteString(text)
		}
		return nil
	}
	if s.emitter.emit == nil {
		s.reply.WriteString(text)
	}
	return s.emitter.add(text)
}

func (s *nativeStream) consumeCodex(line []byte) error {
	var event struct {
		Type     string `json:"type"`
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
		Item     *struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Text  string `json:"text"`
			Phase string `json:"phase"`
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
		raw := event.Error
		if !hasNativeError(raw) {
			raw, _ = json.Marshal(event.Message)
		}
		return nativeFailure(raw, s.active)
	}
	switch event.Type {
	case "thread.started":
		if event.ThreadID == "" {
			return malformedNative()
		}
		return s.setSession(event.ThreadID)
	case "turn.started":
		s.active = true
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
		s.active = true
		if s.finalOnly {
			if s.completed {
				return malformedNative()
			}
			if event.Item.Type != "agent_message" {
				s.candidate.Reset()
				s.candidateReady = false
				s.candidateID = ""
				return nil
			}
			if event.Item.Phase != "" && event.Item.Phase != "final_answer" {
				return nil
			}
			if event.Item.ID != s.candidateID || event.Item.ID == "" {
				s.candidate.Reset()
				s.candidateReady = false
				s.candidateID = event.Item.ID
			}
			// Completed items are authoritative snapshots, not additional text.
			if event.Type == "item.completed" {
				s.candidate.Reset()
				s.candidateReady = true
				s.candidate.WriteString(event.Item.Text)
			}
			return nil
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
		return nativeFailure(event.Error, s.active)
	}
	switch event.Type {
	case "reasoning":
		s.active = true
	case "tool_use", "tool_call", "tool_result":
		s.active = true
		if s.finalOnly {
			s.candidate.Reset()
			s.candidateReady = false
		}
	case "step_start":
		s.active = true
		if s.finalOnly {
			s.candidate.Reset()
			s.candidateReady = false
			s.candidateID = ""
			s.messageID = ""
		}
		if event.Part != nil && event.Part.MessageID != "" {
			s.messageID = event.Part.MessageID
		}
		s.completed = false
	case "text":
		s.active = true
		text := event.Text
		if event.Part != nil {
			// An allowlist, not a denylist: thinking/reasoning/tool parts can
			// never become an answer even if they contain a text property.
			if event.Part.Type != "text" {
				return nil
			}
			if event.Part.MessageID != "" {
				if s.finalOnly && event.Part.MessageID != s.candidateID {
					s.candidate.Reset()
					s.candidateID = event.Part.MessageID
				}
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
		s.active = true
		if event.Part != nil {
			if event.Part.Type != "step-finish" {
				return malformedNative()
			}
			reason = event.Part.Reason
		}
		switch reason {
		case "stop":
			s.candidateReady = s.finalOnly
			s.completed = true
			return s.emitter.flush()
		case "tool-calls":
			s.completed = false
			if s.finalOnly {
				s.candidate.Reset()
				s.candidateReady = false
			}
			return s.emitter.flush()
		case "length", "error", "content-filter", "unknown":
			if err := s.emitter.flush(); err != nil {
				return err
			}
			return nativeFailure(nil, s.active)
		default:
			return malformedNative()
		}
	}
	return nil
}

func (s *nativeStream) result() (*DeliverMessageResult, error) {
	if !s.completed || (s.finalOnly && !s.candidateReady) {
		return nil, &ExecutionError{Code: "incomplete_output", Stage: "completion", Indeterminate: true, Err: errors.New("native CLI exited without a successful terminal event")}
	}
	reply := s.reply.String()
	if s.finalOnly {
		reply = s.candidate.String()
	}
	return &DeliverMessageResult{Success: true, Accepted: true, Completed: true, SessionID: s.sessionID, Reply: reply}, nil
}

type openClawResult struct {
	Payloads []struct {
		Text         string `json:"text"`
		IsError      bool   `json:"isError"`
		IsReasoning  bool   `json:"isReasoning"`
		IsCommentary bool   `json:"isCommentary"`
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

func consumeOpenClaw(data []byte, finalOnly bool) (*DeliverMessageResult, error) {
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
		return nil, nativeFailure(response.Error, response.Result != nil)
	}
	body := response.Result
	if response.Status != "ok" {
		return nil, nativeFailure(nil, body != nil)
	}
	if body == nil {
		return nil, malformedNative()
	}
	if body.Meta != nil {
		meta := body.Meta
		if meta.Aborted || meta.Yielded || meta.ContinuationPending || hasNativeError(meta.Error) {
			return nil, nativeFailure(meta.Error, true)
		}
		if meta.StopReason != "" {
			switch meta.StopReason {
			case "stop", "completed", "end_turn":
			default:
				return nil, nativeFailure(nil, true)
			}
		}
	}
	var reply strings.Builder
	var finalText string
	for _, part := range body.Payloads {
		if part.IsError {
			return nil, nativeFailure(nil, true)
		}
		if !part.IsReasoning && (!finalOnly || !part.IsCommentary) {
			if finalOnly {
				// Gateway payloads are assistant messages, not stream deltas.
				finalText = part.Text
			} else {
				reply.WriteString(part.Text)
			}
		}
	}
	if !finalOnly {
		finalText = reply.String()
	}
	result := &DeliverMessageResult{Success: true, Accepted: true, Completed: true, RunID: response.RunID, Reply: finalText}
	if body.Meta != nil && body.Meta.AgentMeta != nil {
		result.SessionID = body.Meta.AgentMeta.SessionID
	}
	return result, nil
}
