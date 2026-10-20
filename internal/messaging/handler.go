package messaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"clawsynapse/internal/adapter"
	"clawsynapse/internal/protocol"
)

type IncomingMessage struct {
	Context    context.Context
	MessageID  string
	Type       string
	RequestID  string
	RunID      string
	Seq        uint64
	AgentID    string
	From       string
	To         string
	Message    string
	Payload    protocol.MessagePayload
	SessionKey string
	Metadata   map[string]any
	Emit       func(adapter.OutputEvent) error
}

type HandlerResult struct {
	Reply    string
	RunID    string
	Accepted bool
}

type MessageHandler interface {
	HandleMessage(msg IncomingMessage) (HandlerResult, error)
}
type MessageHandlerFunc func(msg IncomingMessage) (HandlerResult, error)

func (f MessageHandlerFunc) HandleMessage(msg IncomingMessage) (HandlerResult, error) { return f(msg) }

type AdapterMessageHandler struct {
	adapter        adapter.AgentAdapter
	timeout        time.Duration
	acceptFeedback bool
}

type HandlerOption func(*AdapterMessageHandler)

// WithFeedbackDelivery is only for non-LLM consumers such as webhook.
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
	if h.adapter == nil {
		return HandlerResult{}, errors.New("adapter unavailable")
	}
	parent := msg.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, h.timeout)
	defer cancel()
	sessionKey := msg.SessionKey
	if !h.acceptFeedback {
		if sessionKey == "" {
			sessionKey = msg.MessageID
		}
		scope, _ := json.Marshal([]string{msg.From, msg.AgentID, sessionKey})
		digest := sha256.Sum256(scope)
		sessionKey = hex.EncodeToString(digest[:])
	}
	result, err := h.adapter.DeliverMessage(ctx, adapter.DeliverMessageRequest{MessageID: msg.MessageID, RequestID: msg.RequestID, RunID: msg.RunID, Seq: msg.Seq, Type: msg.Type, AgentID: msg.AgentID, SessionKey: sessionKey, Message: msg.Message, Payload: msg.Payload, From: msg.From, Metadata: msg.Metadata, Emit: msg.Emit})
	if err != nil {
		return HandlerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return HandlerResult{}, err
	}
	if result == nil || !result.Success || result.Error != "" {
		return HandlerResult{}, errors.New("adapter did not accept execution")
	}
	if !result.Completed && !result.Accepted {
		return HandlerResult{}, errors.New("adapter returned no execution state")
	}
	return HandlerResult{Reply: result.Reply, RunID: result.RunID, Accepted: !result.Completed}, nil
}

func isFeedbackType(t string) bool {
	return strings.HasPrefix(t, "run.") || strings.HasSuffix(t, ".response") || strings.HasSuffix(t, ".error")
}
