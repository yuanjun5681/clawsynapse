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
	adapter adapter.AgentAdapter
	timeout time.Duration
}

func NewAdapterMessageHandler(agentAdapter adapter.AgentAdapter, timeout time.Duration) *AdapterMessageHandler {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AdapterMessageHandler{adapter: agentAdapter, timeout: timeout}
}

func (h *AdapterMessageHandler) HandleMessage(msg IncomingMessage) (HandlerResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	result, err := h.adapter.DeliverMessage(ctx, adapter.DeliverMessageRequest{
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
