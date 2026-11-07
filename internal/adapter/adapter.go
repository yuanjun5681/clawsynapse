package adapter

import (
	"context"

	"clawsynapse/internal/protocol"
)

type AgentAdapter interface {
	DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error)
	GetStatus(ctx context.Context) (*AgentStatus, error)
}

type DeliverMessageRequest struct {
	MessageID string
	RequestID string
	RunID     string
	Seq       uint64
	Type      string
	AgentID   string
	// SessionKey scopes the local Harness session store. ReplySessionKey is the
	// peer-visible key the Harness must use after a successful publish; it must
	// not be replaced by the scoped internal key.
	SessionKey      string
	ReplySessionKey string
	Message         string
	From            string
	Metadata        map[string]any
	Payload         protocol.MessagePayload
	// Emit applies synchronous backpressure; an error must stop execution.
	Emit func(OutputEvent) error
}

type DeliverMessageResult struct {
	Success  bool
	Accepted bool
	// Completed distinguishes a terminal synchronous result from async acceptance.
	Completed bool
	RunID     string
	SessionID string
	// Reply is retained only when Emit is nil; streamed answers are not repeated.
	Reply string
	Error string
}

type AgentStatus struct {
	Healthy  bool
	Degraded bool
	Detail   string
}

// OutputEvent is a normalized answer delta, never an unfiltered product event.
type OutputEvent struct {
	Text string
}

// ExecutionError classifies failures without exposing prompts or native output.
// Indeterminate means execution may have produced effects before it stopped.
type ExecutionError struct {
	Code          string
	Stage         string
	Indeterminate bool
	Err           error
}

func (e *ExecutionError) Error() string {
	if e.Err != nil {
		return e.Code + " at " + e.Stage + ": " + e.Err.Error()
	}
	return e.Code + " at " + e.Stage
}

func (e *ExecutionError) Unwrap() error { return e.Err }

// SafeMessage returns an allowlisted public diagnostic, never the wrapped error.
func (e *ExecutionError) SafeMessage() string {
	switch e.Code {
	case "invalid_model":
		return "The selected model is invalid or unavailable. Choose a model supported by this Harness."
	case "provider_quota":
		return "The model provider's quota or free usage limit has been reached."
	case "provider_auth":
		return "The model provider rejected authentication. Check the provider credentials and access."
	case "native_transport":
		return "The connection to the model provider failed; execution may have started."
	case "session_unavailable":
		return "The native session is unavailable; create a new session explicitly."
	case "native_failed":
		return "The native Harness reported an execution failure."
	default:
		return ""
	}
}
