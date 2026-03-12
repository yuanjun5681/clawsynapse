package protocol

type ControlMessage struct {
	MessageID       string         `json:"messageId"`
	MessageType     string         `json:"messageType"`
	From            string         `json:"from,omitempty"`
	To              string         `json:"to,omitempty"`
	Ts              int64          `json:"ts"`
	TTLms           int64          `json:"ttlMs,omitempty"`
	Alg             string         `json:"alg,omitempty"`
	Signature       string         `json:"signature,omitempty"`
	TraceID         string         `json:"traceId,omitempty"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
	Payload         map[string]any `json:"payload,omitempty"`
}
