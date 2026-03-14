package protocol

type DiscoveryAnnounce struct {
	MessageID    string         `json:"messageId"`
	MessageType  string         `json:"messageType"`
	NodeID       string         `json:"nodeId"`
	Version      string         `json:"version"`
	AgentProduct string         `json:"agentProduct"`
	Capabilities []string       `json:"capabilities"`
	Inbox        string         `json:"inbox"`
	PublicKey    string         `json:"publicKey"`
	Ts           int64          `json:"ts"`
	TTLms        int64          `json:"ttlMs"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Signature    string         `json:"signature,omitempty"`
}

type DiscoveryDepart struct {
	MessageID   string `json:"messageId"`
	MessageType string `json:"messageType"`
	NodeID      string `json:"nodeId"`
	Reason      string `json:"reason,omitempty"`
	Ts          int64  `json:"ts"`
	Signature   string `json:"signature,omitempty"`
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

type MessageEnvelope struct {
	ID              string         `json:"id"`
	Type            string         `json:"type"`
	From            string         `json:"from"`
	To              string         `json:"to,omitempty"`
	Content         string         `json:"content,omitempty"`
	SessionKey      string         `json:"sessionKey,omitempty"`
	Ts              int64          `json:"ts"`
	Sig             string         `json:"sig,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	ProtocolVersion string         `json:"protocolVersion,omitempty"`
}
