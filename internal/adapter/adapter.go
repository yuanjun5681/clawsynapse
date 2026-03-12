package adapter

import "context"

type AgentAdapter interface {
	DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error)
	GetStatus(ctx context.Context) (*AgentStatus, error)
}

type DeliverMessageRequest struct {
	SessionKey string
	Message    string
	From       string
	Metadata   map[string]any
}

type DeliverMessageResult struct {
	Success  bool
	Accepted bool
	RunID    string
	Reply    string
	Error    string
}

type AgentStatus struct {
	Healthy bool
}
