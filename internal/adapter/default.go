package adapter

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

type DefaultAdapter struct {
	nodeID string
}

func NewDefaultAdapter(nodeID string) *DefaultAdapter {
	return &DefaultAdapter{nodeID: nodeID}
}

func (a *DefaultAdapter) DeliverMessage(_ context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error) {
	runID := defaultRunID()
	return &DeliverMessageResult{
		Success:  true,
		Accepted: true,
		RunID:    runID,
		Reply:    fmt.Sprintf("node %s handled message from %s (runId=%s): %s", a.nodeID, req.From, runID, req.Message),
	}, nil
}

func (a *DefaultAdapter) GetStatus(_ context.Context) (*AgentStatus, error) {
	return &AgentStatus{Healthy: true}, nil
}

func defaultRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + base64.RawURLEncoding.EncodeToString(b)
}
