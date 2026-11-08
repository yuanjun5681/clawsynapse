package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"clawsynapse/internal/api"
	"clawsynapse/internal/config"
	"clawsynapse/internal/discovery"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/store"
	"clawsynapse/internal/trust"
)

// Purge removes a local Agent Bot and its identity, trust, files, and durable
// transport state. It refuses the workspace Console identity and any Bot with
// in-flight work. Remote revocation is best-effort because NATS core messages
// are not queued for offline peers.
func (m *Manager) Purge(id string) (api.PurgeResult, error) {
	result := api.PurgeResult{BotID: id, Revoked: []string{}, Warnings: []string{}}
	b, err := m.entry(id)
	if err != nil {
		return result, err
	}

	b.mu.Lock()
	// A failed Bot can have an unreadable config. An explicit non-agent role
	// or a workspace claim is protected; every candidate must still pass the
	// durable work inspection below, including failed Bots.
	if b.workspaceID != "" || (b.cfg.Role != "" && b.cfg.Role != config.RoleAgent) {
		b.mu.Unlock()
		return result, api.Failure(403, "bot.protected", "workspace Console identities cannot be purged")
	}
	switch b.info.State {
	case "deleting", "starting", "stopping":
		state := b.info.State
		b.mu.Unlock()
		return result, api.Failure(409, "bot.busy", "Bot is "+state+"; wait for the transition to finish")
	}
	// Register while holding both locks: shutdown closes admission under m.mu,
	// then joins the entry registration before waiting for transitions.
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		b.mu.Unlock()
		return result, api.Failure(503, "daemon.stopping", "daemon is stopping")
	}
	m.purges.Add(1)
	b.transition.Add(1)
	m.mu.Unlock()
	runtime := b.runtime
	state := b.info.State
	cfg := cloneConfig(b.cfg)
	b.info.State = "deleting"
	b.mu.Unlock()
	defer m.purges.Done()
	defer b.transition.Done()

	committed := false
	defer func() {
		if committed {
			return
		}
		// Reopen only after Quiesce has joined all callbacks, while management
		// admission is still blocked by the deleting state.
		if runtime != nil {
			runtime.bus.ResumeCallbacks()
			runtime.messaging.ResumeAdmissions()
		}
		b.mu.Lock()
		b.info.State = state
		b.mu.Unlock()
	}()

	if runtime != nil {
		// The durable inbox is a pull subscription, not a core bus callback.
		// Freeze journal admission separately before joining HTTP/core handlers.
		runtime.messaging.FreezeAdmissions()
		b.refs.Wait()
		runtime.bus.Quiesce()
	}

	if runtime != nil {
		idle := runtime.messaging.IdleState()
		running, queued := 0, 0
		if runtime.limiter != nil {
			running, queued = runtime.limiter.Snapshot()
		}
		if !idle.Idle() || running != 0 || queued != 0 {
			return result, api.Failure(409, "bot.busy", fmt.Sprintf(
				"Bot still has unfinished work (pending publications=%d, pending commands=%d, active work=%d, running=%d, queued=%d)",
				idle.PendingPublications, idle.PendingCommands, idle.ActiveWork, running, queued))
		}
	} else {
		idle, err := inspectStoppedBot(m.cfg, cfg, id)
		if err != nil {
			return result, api.Failure(409, "bot.busy", "cannot verify that the Bot is idle: "+err.Error())
		}
		if !idle.Idle() {
			return result, api.Failure(409, "bot.busy", fmt.Sprintf(
				"Bot still has unfinished work (pending publications=%d, pending commands=%d, active work=%d)",
				idle.PendingPublications, idle.PendingCommands, idle.ActiveWork))
		}
	}

	// Commit the catalog before irreversible revocation/teardown. A failed
	// catalog write restores the original, still-live runtime and admissions.
	if err := m.removeCatalogEntry(id); err != nil {
		return result, err
	}
	committed = true

	// Revocation must happen while the identity can still sign. A running
	// Runtime has its NATS connection and trust service; a stopped Bot needs a
	// short-lived departure context that never starts the Harness.
	if runtime != nil {
		revoked, warnings := departRunningBot(runtime, id)
		result.Revoked = append(result.Revoked, revoked...)
		result.Warnings = append(result.Warnings, warnings...)
	} else {
		revoked, warnings := departStoppedBot(m.cfg, cfg, id)
		result.Revoked = append(result.Revoked, revoked...)
		result.Warnings = append(result.Warnings, warnings...)
	}

	// Keep Bot-owned credentials available until broker cleanup finishes.
	// A running Bot already has the correctly authenticated connection.
	var consumerErr error
	if runtime != nil {
		consumerErr = runtime.bus.DeleteMessagingConsumer(id)
	} else {
		consumerErr = deleteMessagingConsumer(m.cfg, cfg, id)
	}
	if consumerErr != nil {
		result.Warnings = append(result.Warnings, "NATS durable consumer cleanup failed: "+consumerErr.Error())
	}

	// Keep the deleting claim throughout teardown; m.stop would expose stopped
	// and allow a second journal owner or a new lifecycle transition.
	if runtime != nil {
		runtime.Close()
	}
	b.mu.Lock()
	b.runtime = nil
	b.info.NetworkHealthy = false
	b.info.HarnessHealthy = false
	b.mu.Unlock()

	dir := m.botDir(id)
	if err := os.RemoveAll(dir); err != nil {
		result.Warnings = append(result.Warnings, "Bot data directory could not be fully removed: "+err.Error())
		m.mu.Lock()
		m.recovery = append(m.recovery, id)
		m.mu.Unlock()
	} else if err := syncDir(filepath.Dir(dir)); err != nil {
		result.Warnings = append(result.Warnings, "Bot data directory removal was not synced: "+err.Error())
	}

	result.Warnings = append(result.Warnings, m.scrubPeer(id)...)

	// Shared content is removed only when no remaining Bot references the
	// digest. GC is the authority for that decision.
	if err := m.blobs.GC(); err != nil {
		result.Warnings = append(result.Warnings, "content collection did not finish: "+err.Error())
	}
	return result, nil
}

func (m *Manager) removeCatalogEntry(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, ok := m.catalog[id]
	if !ok {
		return nil
	}
	bot, hadBot := m.bots[id]
	binding, hadBinding := m.bindings[id]
	token, hadToken := m.tokens[id]
	delete(m.catalog, id)
	delete(m.bots, id)
	delete(m.bindings, id)
	delete(m.tokens, id)
	if err := m.saveCatalogLocked(); err != nil {
		m.catalog[id] = previous
		if hadBot {
			m.bots[id] = bot
		}
		if hadBinding {
			m.bindings[id] = binding
		}
		if hadToken {
			m.tokens[id] = token
		}
		return err
	}
	return nil
}

// inspectStoppedBot loads the durable journal without starting the Bot. A
// stopped Bot can still have queued or unacknowledged work.
func inspectStoppedBot(d config.DaemonConfig, b config.BotConfig, botID string) (messaging.IdleState, error) {
	cfg := config.RuntimeConfig(d, b, botID)
	_, id, peers, err := loadBotState(cfg, botID)
	if err != nil {
		return messaging.IdleState{}, err
	}
	messages := messaging.NewService(slog.Default(), peers, nil, botID, id, cfg.TrustMode, cfg.DeliverablePrefixes)
	messages.SetRole(b.Role)
	if err := messages.SetStatePath(filepath.Join(cfg.DataDir, "state", "messages.jsonl")); err != nil {
		return messaging.IdleState{}, err
	}
	defer messages.Close()
	return messages.IdleState(), nil
}

func departRunningBot(r *Runtime, botID string) ([]string, []string) {
	// Join the heartbeat/depart loop so it cannot announce this identity after
	// the final deletion departure, and keep the connection open for flushing.
	r.cancel()
	if r.discovery != nil {
		r.discovery.Wait()
	}
	revoked, warnings := r.trust.RevokeAll("Bot deleted")
	if err := discovery.PublishDeparture(r.bus, r.identity, botID, "deleted"); err != nil {
		warnings = append(warnings, "discovery departure was not published: "+err.Error())
	}
	if err := r.bus.FlushTimeout(2 * time.Second); err != nil {
		warnings = append(warnings, "NATS flush did not confirm: "+err.Error())
	}
	return revoked, warnings
}

func departStoppedBot(d config.DaemonConfig, b config.BotConfig, botID string) ([]string, []string) {
	warnings := make([]string, 0, 2)
	cfg := config.RuntimeConfig(d, b, botID)
	fs, id, peers, err := loadBotState(cfg, botID)
	if err != nil {
		return nil, []string{"could not load identity for trust revocation: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	bus, err := natsbus.Connect(ctx, cfg.NATSServers, "clawsynapsed-purge-"+botID, natsbus.Options{
		CredentialsFile: cfg.NATSCredentialsFile,
		TLSCAFile:       cfg.NATSTLSCAFile,
		TLSCertFile:     cfg.NATSTLSCertFile,
		TLSKeyFile:      cfg.NATSTLSKeyFile,
		TLSServerName:   cfg.NATSTLSServerName,
	})
	if err != nil {
		return nil, []string{"NATS unavailable; remote peers were not notified: " + err.Error()}
	}
	defer bus.Close()
	trusts, err := trust.NewService(slog.Default(), peers, bus, fs, botID, id, cfg.TrustAutoApprove)
	if err != nil {
		return nil, []string{"could not load trust state: " + err.Error()}
	}
	revoked, revokeWarnings := trusts.RevokeAll("Bot deleted")
	warnings = append(warnings, revokeWarnings...)
	if err := discovery.PublishDeparture(bus, id, botID, "deleted"); err != nil {
		warnings = append(warnings, "discovery departure was not published: "+err.Error())
	}
	if err := bus.FlushTimeout(2 * time.Second); err != nil {
		warnings = append(warnings, "NATS flush did not confirm: "+err.Error())
	}
	return revoked, warnings
}

func deleteMessagingConsumer(d config.DaemonConfig, b config.BotConfig, botID string) error {
	cfg := config.RuntimeConfig(d, b, botID)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	bus, err := natsbus.Connect(ctx, cfg.NATSServers, "clawsynapsed-purge-consumer-"+botID, natsbus.Options{
		CredentialsFile: cfg.NATSCredentialsFile,
		TLSCAFile:       cfg.NATSTLSCAFile,
		TLSCertFile:     cfg.NATSTLSCertFile,
		TLSKeyFile:      cfg.NATSTLSKeyFile,
		TLSServerName:   cfg.NATSTLSServerName,
	})
	if err != nil {
		return err
	}
	defer bus.Close()
	return bus.DeleteMessagingConsumer(botID)
}

// scrubPeer removes the deleted Bot from other local Bots' trust and remark
// state. Running Bots use their live trust service; stopped or failed Bots are
// scrubbed on disk. Starting/stopping Bots are skipped rather than racing their
// state load.
func (m *Manager) scrubPeer(botID string) []string {
	m.mu.RLock()
	entries := make([]*managedBot, 0, len(m.bots))
	for _, b := range m.bots {
		if b.info.BotID != botID {
			entries = append(entries, b)
		}
	}
	m.mu.RUnlock()

	warnings := []string{}
	for _, other := range entries {
		other.mu.Lock()
		runtime := other.runtime
		state := other.info.State
		id := other.info.BotID
		if runtime != nil && state == "running" {
			other.refs.Add(1)
			other.mu.Unlock()
			err := runtime.trust.RemovePeer(botID)
			other.refs.Done()
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("could not remove deleted peer from %s: %v", id, err))
			}
			continue
		}
		if state == "starting" || state == "stopping" || state == "deleting" {
			other.mu.Unlock()
			warnings = append(warnings, fmt.Sprintf("peer reference in %s was not scrubbed while %s", id, state))
			continue
		}
		err := removePeerFiles(store.NewFSStore(m.botDir(id)), botID)
		other.mu.Unlock()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not remove deleted peer from %s: %v", id, err))
		}
	}
	return warnings
}

func removePeerFiles(fs *store.FSStore, botID string) error {
	st, err := fs.LoadTrustState()
	if err != nil {
		return err
	}
	st.Trusted = filterPeerStates(st.Trusted, botID)
	st.Rejected = filterPeerStates(st.Rejected, botID)
	st.Revoked = filterPeerStates(st.Revoked, botID)
	pending := st.Pending[:0]
	for _, p := range st.Pending {
		if p.From != botID && p.To != botID {
			pending = append(pending, p)
		}
	}
	st.Pending = pending
	if err := fs.SaveTrustState(st); err != nil {
		return err
	}
	attrs, err := fs.LoadPeerAttrsState()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	delete(attrs.Peers, botID)
	return fs.SavePeerAttrsState(attrs)
}

func filterPeerStates(states []store.TrustPeerState, botID string) []store.TrustPeerState {
	out := states[:0]
	for _, p := range states {
		if p.BotID != botID {
			out = append(out, p)
		}
	}
	return out
}
