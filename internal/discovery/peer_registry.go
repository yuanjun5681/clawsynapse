package discovery

import (
	"sort"
	"sync"
	"time"

	"clawsynapse/pkg/types"
)

type Registry struct {
	mu    sync.RWMutex
	peers map[string]types.Peer
	local map[string]types.PeerLocalAttrs
}

func NewRegistry() *Registry {
	return &Registry{
		peers: map[string]types.Peer{},
		local: map[string]types.PeerLocalAttrs{},
	}
}

func (r *Registry) Upsert(peer types.Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if peer.LastSeenMs == 0 {
		peer.LastSeenMs = time.Now().UnixMilli()
	}

	old, exists := r.peers[peer.NodeID]
	if exists {
		if peer.AuthStatus == "" {
			peer.AuthStatus = old.AuthStatus
		}
		if peer.TrustStatus == "" {
			peer.TrustStatus = old.TrustStatus
		}
	}

	if peer.AuthStatus == "" {
		peer.AuthStatus = types.AuthSeen
	}
	if peer.TrustStatus == "" {
		peer.TrustStatus = types.TrustNone
	}

	r.peers[peer.NodeID] = peer
}

// refreshDiscovery updates remotely advertised fields without racing local
// authentication and trust changes.
func (r *Registry) refreshDiscovery(peer types.Peer) (types.Peer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, exists := r.peers[peer.NodeID]
	if exists {
		peer.AuthStatus = old.AuthStatus
		peer.TrustStatus = old.TrustStatus
	}
	if peer.AuthStatus == "" {
		peer.AuthStatus = types.AuthSeen
	}
	if peer.TrustStatus == "" {
		peer.TrustStatus = types.TrustNone
	}
	r.peers[peer.NodeID] = peer
	return old, exists
}

func (r *Registry) SetAuthStatus(nodeID, authStatus string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	peer, ok := r.peers[nodeID]
	if !ok {
		return false
	}
	peer.AuthStatus = authStatus
	peer.LastSeenMs = time.Now().UnixMilli()
	r.peers[nodeID] = peer
	return true
}

func (r *Registry) SetTrustStatus(nodeID, trustStatus string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	peer, ok := r.peers[nodeID]
	if !ok {
		return false
	}
	peer.TrustStatus = trustStatus
	peer.LastSeenMs = time.Now().UnixMilli()
	r.peers[nodeID] = peer
	return true
}

func (r *Registry) Get(nodeID string) (types.Peer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.peers[nodeID]
	if !ok {
		return types.Peer{}, false
	}
	return r.mergeLocalLocked(p), true
}

func (r *Registry) List() []types.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]types.Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, r.mergeLocalLocked(p))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

// mergeLocalLocked overlays local remarks onto a peer view. Callers must
// hold at least a read lock. Local remarks never travel back into peers.
func (r *Registry) mergeLocalLocked(p types.Peer) types.Peer {
	attrs, ok := r.local[p.NodeID]
	if !ok {
		return p
	}
	p.LocalAlias = attrs.Alias
	p.LocalDescription = attrs.Description
	p.LocalLabels = attrs.Labels
	return p
}

// SetLocalAttrs replaces the local remarks for a node. Remarks are kept even
// for nodes that are currently unknown, so they survive rediscovery.
func (r *Registry) SetLocalAttrs(nodeID string, attrs types.PeerLocalAttrs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if attrs.IsZero() {
		delete(r.local, nodeID)
		return
	}
	r.local[nodeID] = attrs
}

// LoadLocalAttrs replaces all local remarks, e.g. from persisted state at
// startup.
func (r *Registry) LoadLocalAttrs(attrs map[string]types.PeerLocalAttrs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.local = map[string]types.PeerLocalAttrs{}
	for nodeID, a := range attrs {
		if a.IsZero() {
			continue
		}
		r.local[nodeID] = a
	}
}

// LocalAttrsSnapshot returns a copy of all local remarks for persistence.
func (r *Registry) LocalAttrsSnapshot() map[string]types.PeerLocalAttrs {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]types.PeerLocalAttrs, len(r.local))
	for nodeID, a := range r.local {
		out[nodeID] = a
	}
	return out
}

func (r *Registry) Remove(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, nodeID)
}
