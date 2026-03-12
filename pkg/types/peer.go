package types

type Peer struct {
	NodeID       string         `json:"nodeId"`
	AgentProduct string         `json:"agentProduct,omitempty"`
	Version      string         `json:"version,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Inbox        string         `json:"inbox,omitempty"`
	AuthStatus   string         `json:"authStatus"`
	TrustStatus  string         `json:"trustStatus,omitempty"`
	LastSeenMs   int64          `json:"lastSeenMs"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
