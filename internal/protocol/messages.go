package protocol

import (
	"encoding/json"

	"clawsynapse/pkg/types"
)

type DiscoveryAnnounce struct {
	ProtocolVersion string         `json:"protocolVersion"`
	MessageID       string         `json:"messageId"`
	MessageType     string         `json:"messageType"`
	BotID           string         `json:"nodeId"`
	DID             string         `json:"did,omitempty"`
	Version         string         `json:"version"`
	AgentProduct    string         `json:"agentProduct"`
	DisplayName     string         `json:"displayName,omitempty"`
	Labels          types.Labels   `json:"labels,omitempty"`
	Inbox           string         `json:"inbox"`
	PublicKey       string         `json:"publicKey"`
	Ts              int64          `json:"ts"`
	TTLms           int64          `json:"ttlMs"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Signature       string         `json:"signature,omitempty"`
}

type DiscoveryDepart struct {
	ProtocolVersion string `json:"protocolVersion"`
	MessageID       string `json:"messageId"`
	MessageType     string `json:"messageType"`
	BotID           string `json:"nodeId"`
	Reason          string `json:"reason,omitempty"`
	Ts              int64  `json:"ts"`
	Signature       string `json:"signature,omitempty"`
}

type AuthChallengeRequest struct {
	MessageID   string `json:"messageId"`
	MessageType string `json:"messageType"`
	From        string `json:"from"`
	To          string `json:"to"`
	PublicKey   string `json:"publicKey"`
	Nonce       string `json:"nonce"`
	Ts          int64  `json:"ts"`
	Alg         string `json:"alg"`
	Signature   string `json:"signature,omitempty"`
}

type AuthChallengeResponse struct {
	MessageID    string `json:"messageId"`
	MessageType  string `json:"messageType"`
	From         string `json:"from"`
	To           string `json:"to"`
	PublicKey    string `json:"publicKey"`
	Nonce        string `json:"nonce"`
	ChallengeRef string `json:"challengeRef"`
	Proof        string `json:"proof"`
	Ts           int64  `json:"ts"`
}

type AuthChallengeAck struct {
	MessageID    string `json:"messageId"`
	MessageType  string `json:"messageType"`
	From         string `json:"from"`
	To           string `json:"to"`
	ChallengeRef string `json:"challengeRef"`
	Proof        string `json:"proof"`
	Ts           int64  `json:"ts"`
}

type TrustRequest struct {
	MessageID    string   `json:"messageId"`
	MessageType  string   `json:"messageType"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	RequestID    string   `json:"requestId"`
	Reason       string   `json:"reason,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Ts           int64    `json:"ts"`
	Signature    string   `json:"signature,omitempty"`
}

type TrustResponse struct {
	MessageID   string `json:"messageId"`
	MessageType string `json:"messageType"`
	From        string `json:"from"`
	To          string `json:"to"`
	RequestID   string `json:"requestId"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason,omitempty"`
	Ts          int64  `json:"ts"`
	Signature   string `json:"signature,omitempty"`
}

type TrustRevoke struct {
	MessageID   string `json:"messageId"`
	MessageType string `json:"messageType"`
	From        string `json:"from"`
	To          string `json:"to"`
	Reason      string `json:"reason,omitempty"`
	Ts          int64  `json:"ts"`
	Signature   string `json:"signature,omitempty"`
}

type TransferAvailable struct {
	MessageID   string `json:"messageId"`
	MessageType string `json:"messageType"`
	TransferID  string `json:"transferId"`
	From        string `json:"from"`
	To          string `json:"to"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	MimeType    string `json:"mimeType,omitempty"`
	Bucket      string `json:"bucket"`
	Ts          int64  `json:"ts"`
	Signature   string `json:"signature,omitempty"`
}

type MessageEnvelope struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	AgentID         string         `json:"agentId,omitempty"`
	From            string         `json:"from"`
	To              string         `json:"to,omitempty"`
	Payload         MessagePayload `json:"payload"`
	SessionKey      string         `json:"sessionKey,omitempty"`
	Ts              int64          `json:"ts"`
	ExpiresAt       int64          `json:"expiresAt"`
	RequestID       string         `json:"requestId,omitempty"`
	RunID           string         `json:"runId,omitempty"`
	Seq             uint64         `json:"seq,omitempty"`
	Sig             string         `json:"sig,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
}

// MessagePayload separates user input from transport and execution state.
type MessagePayload struct {
	Text          string          `json:"text,omitempty"`
	Instruction   string          `json:"instruction,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	Channel       string          `json:"channel,omitempty"`
	Code          string          `json:"code,omitempty"`
	Stage         string          `json:"stage,omitempty"`
	Indeterminate bool            `json:"indeterminate,omitempty"`
	AdapterRunID  string          `json:"adapterRunId,omitempty"`
}
