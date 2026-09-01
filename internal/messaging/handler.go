package messaging

import (
	"context"
	"fmt"
	"strings"
	"time"

	"clawsynapse/internal/adapter"
)

type IncomingMessage struct {
	MessageID  string
	Type       string
	AgentID    string
	From       string
	To         string
	Message    string
	SessionKey string
	Metadata   map[string]any
}

type HandlerResult struct {
	Reply string
	RunID string
}

type MessageHandler interface {
	HandleMessage(msg IncomingMessage) (HandlerResult, error)
}

type MessageHandlerFunc func(msg IncomingMessage) (HandlerResult, error)

func (f MessageHandlerFunc) HandleMessage(msg IncomingMessage) (HandlerResult, error) {
	return f(msg)
}

type DefaultMessageHandler struct {
	nodeID string
}

func NewDefaultMessageHandler(nodeID string) *DefaultMessageHandler {
	return &DefaultMessageHandler{nodeID: nodeID}
}

func (h *DefaultMessageHandler) HandleMessage(msg IncomingMessage) (HandlerResult, error) {
	return HandlerResult{Reply: fmt.Sprintf("node %s handled message from %s: %s", h.nodeID, msg.From, msg.Message)}, nil
}

type AdapterMessageHandler struct {
	adapter        adapter.AgentAdapter
	timeout        time.Duration
	acceptFeedback bool
}

// HandlerOption customizes an AdapterMessageHandler at construction time.
type HandlerOption func(*AdapterMessageHandler)

// WithFeedbackDelivery lets the handler forward .response / .error messages to
// the underlying adapter. Use it for passthrough adapters (e.g. webhook) where
// the remote endpoint can consume arbitrary notifications. Do NOT use it for
// LLM-based adapters (openclaw, opencode, codex) — they would re-ingest the
// reply text as a new prompt and create feedback loops.
func WithFeedbackDelivery() HandlerOption {
	return func(h *AdapterMessageHandler) { h.acceptFeedback = true }
}

func NewAdapterMessageHandler(agentAdapter adapter.AgentAdapter, timeout time.Duration, opts ...HandlerOption) *AdapterMessageHandler {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	h := &AdapterMessageHandler{adapter: agentAdapter, timeout: timeout}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func (h *AdapterMessageHandler) HandleMessage(msg IncomingMessage) (HandlerResult, error) {
	if !h.acceptFeedback && isFeedbackType(msg.Type) {
		return HandlerResult{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	result, err := h.adapter.DeliverMessage(ctx, adapter.DeliverMessageRequest{
		MessageID:  msg.MessageID,
		Type:       msg.Type,
		AgentID:    msg.AgentID,
		SessionKey: msg.SessionKey,
		Message:    msg.Message,
		From:       msg.From,
		Metadata:   msg.Metadata,
	})
	if err != nil {
		return HandlerResult{}, err
	}
	if result == nil {
		return HandlerResult{}, fmt.Errorf("adapter returned nil result")
	}
	if result.Error != "" {
		return HandlerResult{}, fmt.Errorf("adapter error: %s", result.Error)
	}
	if !result.Success {
		return HandlerResult{}, fmt.Errorf("adapter did not complete successfully")
	}
	if result.Reply != "" {
		reply := result.Reply
		if result.RunID != "" && !strings.Contains(reply, "runId="+result.RunID) {
			reply = fmt.Sprintf("%s (runId=%s)", reply, result.RunID)
		}
		return HandlerResult{Reply: reply, RunID: result.RunID}, nil
	}
	if result.Accepted {
		reply := "accepted"
		if result.RunID != "" {
			reply = fmt.Sprintf("accepted (runId=%s)", result.RunID)
		}
		return HandlerResult{Reply: reply, RunID: result.RunID}, nil
	}
	return HandlerResult{}, fmt.Errorf("adapter did not accept message")
}

// isFeedbackType 判断消息是否属于"反馈类"（上一条消息的响应或错误）。
// 这类消息不应投递给 LLM 类适配器，也不应触发新的回复（见 service.replyToSender）。
func isFeedbackType(t string) bool {
	return strings.HasSuffix(t, ".response") || strings.HasSuffix(t, ".error")
}
