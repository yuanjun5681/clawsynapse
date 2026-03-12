package messaging

import (
	"context"
	"fmt"
	"strings"
	"time"

	"clawsynapse/internal/adapter"
)

type IncomingRequest struct {
	RequestID  string
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

type RequestHandler interface {
	HandleRequest(req IncomingRequest) (HandlerResult, error)
}

type RequestHandlerFunc func(req IncomingRequest) (HandlerResult, error)

func (f RequestHandlerFunc) HandleRequest(req IncomingRequest) (HandlerResult, error) {
	return f(req)
}

type DefaultRequestHandler struct {
	nodeID string
}

func NewDefaultRequestHandler(nodeID string) *DefaultRequestHandler {
	return &DefaultRequestHandler{nodeID: nodeID}
}

func (h *DefaultRequestHandler) HandleRequest(req IncomingRequest) (HandlerResult, error) {
	return HandlerResult{Reply: fmt.Sprintf("node %s handled request from %s: %s", h.nodeID, req.From, req.Message)}, nil
}

type AdapterRequestHandler struct {
	adapter adapter.AgentAdapter
	timeout time.Duration
}

func NewAdapterRequestHandler(agentAdapter adapter.AgentAdapter, timeout time.Duration) *AdapterRequestHandler {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &AdapterRequestHandler{adapter: agentAdapter, timeout: timeout}
}

func (h *AdapterRequestHandler) HandleRequest(req IncomingRequest) (HandlerResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	result, err := h.adapter.DeliverMessage(ctx, adapter.DeliverMessageRequest{
		SessionKey: req.SessionKey,
		Message:    req.Message,
		From:       req.From,
		Metadata:   req.Metadata,
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
