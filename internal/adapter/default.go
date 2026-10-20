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

func (a *DefaultAdapter) DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, cancellationError(err, false)
	}
	runID := defaultRunID()
	message := req.Payload.Text
	if message == "" {
		message = req.Payload.Instruction
	}
	if message == "" {
		message = req.Message
	}
	reply := fmt.Sprintf("node %s handled message from %s (runId=%s): %s", a.nodeID, req.From, runID, message)
	emitter := &answerEmitter{emit: req.Emit}
	if err := emitter.add(reply); err != nil {
		return nil, err
	}
	if err := emitter.flush(); err != nil {
		return nil, err
	}
	if req.Emit != nil {
		reply = ""
	}
	return &DeliverMessageResult{
		Success:   true,
		Accepted:  true,
		Completed: true,
		RunID:     runID,
		Reply:     reply,
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
