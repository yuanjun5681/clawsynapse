package auth

import (
	"fmt"
	"sync"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

type ReplayGuard struct {
	mu         sync.Mutex
	store      *store.FSStore
	entries    map[string]int64
	maxEntries int
	ttl        time.Duration
}

func NewReplayGuard(fs *store.FSStore, maxEntries int, ttl time.Duration) (*ReplayGuard, error) {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	st, err := fs.LoadReplayState()
	if err != nil {
		return nil, err
	}

	r := &ReplayGuard{
		store:      fs,
		entries:    st.Entries,
		maxEntries: maxEntries,
		ttl:        ttl,
	}
	r.gc(time.Now().UnixMilli())
	if err := r.persist(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ReplayGuard) CheckAndRemember(key string, ts int64) error {
	nowMs := time.Now().UnixMilli()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.gc(nowMs)

	if _, exists := r.entries[key]; exists {
		return protocol.NewError(protocol.ErrReplayDetected, fmt.Sprintf("replay detected for key: %s", key))
	}

	r.entries[key] = ts
	if len(r.entries) > r.maxEntries {
		r.evictOldest()
	}

	if err := r.persistLocked(); err != nil {
		return err
	}
	return nil
}

func (r *ReplayGuard) gc(nowMs int64) {
	deadline := nowMs - r.ttl.Milliseconds()
	for k, ts := range r.entries {
		if ts < deadline {
			delete(r.entries, k)
		}
	}
}

func (r *ReplayGuard) evictOldest() {
	for len(r.entries) > r.maxEntries {
		var oldestKey string
		var oldestTs int64
		first := true
		for k, ts := range r.entries {
			if first || ts < oldestTs {
				oldestKey = k
				oldestTs = ts
				first = false
			}
		}
		if oldestKey == "" {
			return
		}
		delete(r.entries, oldestKey)
	}
}

func (r *ReplayGuard) persist() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persistLocked()
}

func (r *ReplayGuard) persistLocked() error {
	cp := make(map[string]int64, len(r.entries))
	for k, v := range r.entries {
		cp[k] = v
	}
	return r.store.SaveReplayState(store.ReplayState{
		SchemaVersion: 1,
		Entries:       cp,
	})
}
