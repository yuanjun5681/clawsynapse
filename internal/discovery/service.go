package discovery

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

const (
	announceSubject = "clawsynapse.discovery.global.announce"
	departSubject   = "clawsynapse.discovery.global.depart"
)

type Service struct {
	log       *slog.Logger
	bus       *natsbus.Client
	peers     *Registry
	nodeID    string
	publicKey string
	ttl       time.Duration
	heartbeat time.Duration
	trustMode string
}

func NewService(log *slog.Logger, bus *natsbus.Client, peers *Registry, nodeID string, publicKey string, heartbeat, ttl time.Duration, trustMode string) *Service {
	return &Service{
		log:       log,
		bus:       bus,
		peers:     peers,
		nodeID:    nodeID,
		publicKey: publicKey,
		ttl:       ttl,
		heartbeat: heartbeat,
		trustMode: trustMode,
	}
}

func (s *Service) Start(ctx context.Context) error {
	if _, err := s.bus.Subscribe(announceSubject, s.handleAnnounce); err != nil {
		return err
	}
	if _, err := s.bus.Subscribe(departSubject, s.handleDepart); err != nil {
		return err
	}

	if err := s.publishAnnounce(); err != nil {
		return err
	}

	heartbeatTicker := time.NewTicker(s.heartbeat)
	gcTicker := time.NewTicker(time.Second)

	go func() {
		defer heartbeatTicker.Stop()
		defer gcTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.publishDepart("shutdown")
				return
			case <-heartbeatTicker.C:
				if err := s.publishAnnounce(); err != nil {
					s.log.Warn("publish announce failed", slog.String("error", err.Error()))
				}
			case <-gcTicker.C:
				s.gc()
			}
		}
	}()

	return nil
}

func (s *Service) publishAnnounce() error {
	msg := protocol.DiscoveryAnnounce{
		MessageID:    randID(),
		MessageType:  "discovery.announce",
		NodeID:       s.nodeID,
		Version:      "v0.1.0",
		AgentProduct: "clawsynapse",
		Capabilities: []string{"chat", "tools"},
		Inbox:        "clawsynapse.msg." + s.nodeID + ".inbox",
		PublicKey:    s.publicKey,
		Ts:           time.Now().UnixMilli(),
		TTLms:        s.ttl.Milliseconds(),
	}
	return s.bus.PublishJSON(announceSubject, msg)
}

func (s *Service) publishDepart(reason string) error {
	msg := protocol.DiscoveryDepart{
		MessageID:   randID(),
		MessageType: "discovery.depart",
		NodeID:      s.nodeID,
		Reason:      reason,
		Ts:          time.Now().UnixMilli(),
	}
	return s.bus.PublishJSON(departSubject, msg)
}

func (s *Service) handleAnnounce(_ string, data []byte) {
	var msg protocol.DiscoveryAnnounce
	if err := json.Unmarshal(data, &msg); err != nil {
		s.log.Warn("decode announce failed", slog.String("error", err.Error()))
		return
	}
	if msg.NodeID == s.nodeID {
		return
	}

	authStatus := types.AuthSeen
	trustStatus := types.TrustNone
	metadata := map[string]any{
		"publicKey": msg.PublicKey,
		"ttlMs":     msg.TTLms,
	}
	if existing, ok := s.peers.Get(msg.NodeID); ok {
		authStatus = existing.AuthStatus
		trustStatus = existing.TrustStatus
		if existing.Metadata != nil {
			if known, ok := existing.Metadata["publicKey"].(string); ok && known != "" {
				if s.trustMode == "tofu" || s.trustMode == "explicit" {
					if msg.PublicKey != "" && known != msg.PublicKey {
						s.log.Warn("peer public key mismatch detected", slog.String("peer", msg.NodeID), slog.String("mode", s.trustMode))
						authStatus = types.AuthRejected
						metadata["publicKey"] = known
					}
				}
			}
		}
	}

	s.peers.Upsert(types.Peer{
		NodeID:       msg.NodeID,
		Version:      msg.Version,
		AgentProduct: msg.AgentProduct,
		Capabilities: msg.Capabilities,
		Inbox:        msg.Inbox,
		AuthStatus:   authStatus,
		TrustStatus:  trustStatus,
		LastSeenMs:   msg.Ts,
		Metadata:     metadata,
	})
}

func (s *Service) handleDepart(_ string, data []byte) {
	var msg protocol.DiscoveryDepart
	if err := json.Unmarshal(data, &msg); err != nil {
		s.log.Warn("decode depart failed", slog.String("error", err.Error()))
		return
	}
	if msg.NodeID == s.nodeID {
		return
	}
	s.peers.Remove(msg.NodeID)
}

func (s *Service) gc() {
	now := time.Now().UnixMilli()
	for _, peer := range s.peers.List() {
		if peer.NodeID == s.nodeID {
			continue
		}
		ttlMs := metadataInt64(peer.Metadata, "ttlMs")
		if ttlMs <= 0 {
			ttlMs = s.ttl.Milliseconds()
		}
		if now-peer.LastSeenMs > ttlMs {
			s.peers.Remove(peer.NodeID)
		}
	}
}

func metadataInt64(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return time.Now().Format("20060102150405.000000000")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
