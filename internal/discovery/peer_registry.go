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
}

func NewRegistry() *Registry {
	return &Registry{peers: map[string]types.Peer{}}
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
	return p, ok
}

func (r *Registry) List() []types.Peer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]types.Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func (r *Registry) Remove(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, nodeID)
}
