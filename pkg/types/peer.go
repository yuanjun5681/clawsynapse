package types

// Peer is the local view of a discovered remote Bot. Public names and labels
// arrive through signed discovery. Local remarks belong to the receiving Bot
// and are never overwritten by remote heartbeats or broadcast.
type Peer struct {
	BotID            string         `json:"botId"`
	DID              string         `json:"did,omitempty"`
	AgentProduct     string         `json:"agentProduct,omitempty"`
	Version          string         `json:"version,omitempty"`
	DisplayName      string         `json:"displayName,omitempty"`
	Labels           Labels         `json:"labels,omitempty"`
	Inbox            string         `json:"inbox,omitempty"`
	AuthStatus       string         `json:"authStatus"`
	TrustStatus      string         `json:"trustStatus,omitempty"`
	LastSeenMs       int64          `json:"lastSeenMs"`
	LocalAlias       string         `json:"localAlias,omitempty"`
	LocalDescription string         `json:"localDescription,omitempty"`
	LocalLabels      Labels         `json:"localLabels,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// PeerLocalAttrs are locally maintained remarks about a peer, persisted in
// peer-attrs.json and keyed by BotID. They survive restarts and peer
// rediscovery, and changing them never affects identity or trust.
type PeerLocalAttrs struct {
	Alias       string `json:"alias,omitempty"`
	Description string `json:"description,omitempty"`
	Labels      Labels `json:"labels,omitempty"`
}
