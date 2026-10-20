package types

// Peer is the API-facing view of a known node. DisplayName and Labels are
// self-reported by the remote node over signed discovery announcements.
// Local* fields are local remarks maintained on this node; they are never
// overwritten by remote heartbeats and never leave this node.
type Peer struct {
	NodeID           string         `json:"nodeId"`
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
// peer-attrs.json and keyed by NodeID. They survive restarts and peer
// rediscovery, and changing them never affects identity or trust.
type PeerLocalAttrs struct {
	Alias       string `json:"alias,omitempty"`
	Description string `json:"description,omitempty"`
	Labels      Labels `json:"labels,omitempty"`
}
