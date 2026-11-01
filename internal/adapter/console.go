package adapter

import (
	"context"
	"strings"
)

// ConsoleAdapter is an inbox endpoint, not an execution Harness. Messaging
// journals its passive deliveries without generating execution events.
type ConsoleAdapter struct{}

func (ConsoleAdapter) InboxOnly(messageType string) bool {
	return messageType == "chat.message" || messageType == "chat.context" || strings.HasPrefix(messageType, "run.") || strings.HasSuffix(messageType, ".response") || strings.HasSuffix(messageType, ".error")
}

func (ConsoleAdapter) DeliverMessage(_ context.Context, _ DeliverMessageRequest) (*DeliverMessageResult, error) {
	return nil, &ExecutionError{Code: "console.execution_unsupported", Stage: "adapter"}
}

func (ConsoleAdapter) GetStatus(_ context.Context) (*AgentStatus, error) {
	return &AgentStatus{Detail: "not applicable: console is a durable inbox, not an execution Harness"}, nil
}
