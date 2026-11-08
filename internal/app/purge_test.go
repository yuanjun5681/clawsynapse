package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/api"
	"clawsynapse/internal/config"
	"clawsynapse/internal/messaging"
	"clawsynapse/internal/natsbus"
	"clawsynapse/internal/store"
	"clawsynapse/internal/trust"
	"clawsynapse/pkg/types"
)

func TestPurgeRemovesAgentIdentityTrustAndCatalog(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil // Keep the test offline and deterministic.
	target := newTestBot(t, a, "to-delete")
	other := newTestBot(t, a, "observer")

	targetFS := store.NewFSStore(a.manager.botDir(target))
	state, err := targetFS.LoadTrustState()
	if err != nil {
		t.Fatal(err)
	}
	state.Trusted = append(state.Trusted, store.TrustPeerState{BotID: other, AtMs: 1})
	if err := targetFS.SaveTrustState(state); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(a.manager.botDir(target), "workspace", "binding", "transfers")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "received-file"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}

	otherFS := store.NewFSStore(a.manager.botDir(other))
	otherState, err := otherFS.LoadTrustState()
	if err != nil {
		t.Fatal(err)
	}
	otherState.Trusted = append(otherState.Trusted, store.TrustPeerState{BotID: target, AtMs: 1})
	if err := otherFS.SaveTrustState(otherState); err != nil {
		t.Fatal(err)
	}
	attrs, err := otherFS.LoadPeerAttrsState()
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Peers == nil {
		attrs.Peers = map[string]types.PeerLocalAttrs{}
	}
	attrs.Peers[target] = types.PeerLocalAttrs{Alias: "local note"}
	if err := otherFS.SavePeerAttrsState(attrs); err != nil {
		t.Fatal(err)
	}

	result, err := a.manager.Purge(target)
	if err != nil {
		t.Fatal(err)
	}
	if result.BotID != target || len(result.Warnings) == 0 {
		t.Fatalf("purge result did not report offline notification: %+v", result)
	}
	if _, err := os.Stat(a.manager.botDir(target)); !os.IsNotExist(err) {
		t.Fatalf("Bot directory survived purge: %v", err)
	}
	if _, err := a.manager.Info(target); !errors.As(err, new(*api.Error)) {
		t.Fatalf("purged Bot is still registered: %v", err)
	}
	catalog, err := os.ReadFile(filepath.Join(a.manager.cfg.DataDir, "bots.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(catalog), target) {
		t.Fatal("Bot catalog retained the purged identity")
	}
	otherState, err = otherFS.LoadTrustState()
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range otherState.Trusted {
		if peer.BotID == target {
			t.Fatal("another local Bot retained trust in the purged Bot")
		}
	}
	attrs, err = otherFS.LoadPeerAttrsState()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := attrs.Peers[target]; exists {
		t.Fatal("another local Bot retained remarks for the purged Bot")
	}
}

func TestPurgeRefusesConsoleAndBusyAgent(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil

	console, _, err := a.manager.BindWorkspace("workspace-protected", "Console")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.manager.Purge(console.BotID); !purgeFailureCode(err, "bot.protected") {
		t.Fatalf("Console purge was not refused: %v", err)
	}

	busy := newTestBot(t, a, "busy")
	journal := filepath.Join(a.manager.botDir(busy), "state", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	record := []byte(`{"work":{"key":"peer/message","state":"queued"}}` + "\n")
	if err := os.WriteFile(journal, record, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manager.Purge(busy); !purgeFailureCode(err, "bot.busy") {
		t.Fatalf("busy Bot purge was not refused: %v", err)
	}
	if _, err := os.Stat(a.manager.botDir(busy)); err != nil {
		t.Fatalf("busy Bot directory changed: %v", err)
	}
}

func purgeFailureCode(err error, code string) bool {
	var failure *api.Error
	return errors.As(err, &failure) && failure.Code == code
}

func TestPurgeHTTPRouteRequiresManagementScope(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "http-delete")
	requireStatus(t, request(t, a, "DELETE", "/v2/bots/"+id, botToken(t, a, id), nil), 403)
	requireStatus(t, request(t, a, "DELETE", "/v2/bots/"+id, "", nil), 200)
	requireStatus(t, request(t, a, "GET", "/v2/bots/"+id, "", nil), 404)
}

func TestPurgeFailedBotStillProtectsQueuedJournal(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "failed-with-work")
	path := filepath.Join(a.manager.botDir(id), "state", "messages.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	record := []byte(`{"work":{"key":"peer/message","state":"queued"}}` + "\n")
	if err := os.WriteFile(path, record, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manager.Purge(id); !purgeFailureCode(err, "bot.busy") {
		t.Fatalf("stopped queued Bot purge: %v", err)
	}
	if _, err := a.manager.Transition(id, "start"); err != nil {
		t.Fatal(err)
	}
	b, _ := a.manager.entry(id)
	b.transition.Wait()
	info, err := a.manager.Info(id)
	if err != nil || info.State != "failed" {
		t.Fatalf("startup did not fail: %+v, %v", info, err)
	}
	if _, err := a.manager.Purge(id); !purgeFailureCode(err, "bot.busy") {
		t.Fatalf("failed queued Bot purge: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(record) {
		t.Fatalf("failed Bot journal changed: %q, %v", got, err)
	}
}

// A runtime with real journal/trust state but no server lets these concurrency
// tests block at the public handler pin without starting transport workers.
func purgeTestRuntime(t *testing.T, a *App, id string) (*managedBot, *Runtime) {
	t.Helper()
	b, err := a.manager.entry(id)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.RuntimeConfig(a.manager.cfg, b.cfg, id)
	fs, identity, peers, err := loadBotState(cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bus := &natsbus.Client{}
	messages := messaging.NewService(slog.Default(), peers, bus, id, identity, cfg.TrustMode, cfg.DeliverablePrefixes)
	messages.SetMessageHandler(messaging.MessageHandlerFunc(func(messaging.IncomingMessage) (messaging.HandlerResult, error) {
		return messaging.HandlerResult{}, nil
	}))
	if err := messages.SetStatePath(filepath.Join(cfg.DataDir, "state", "messages.jsonl")); err != nil {
		t.Fatal(err)
	}
	trusts, err := trust.NewService(slog.Default(), peers, bus, fs, id, identity, false)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{ctx: ctx, cancel: cancel, identity: identity, bus: bus, messaging: messages, trust: trusts, handler: http.NotFoundHandler()}
	b.mu.Lock()
	b.runtime = r
	b.info.State = "running"
	b.mu.Unlock()
	return b, r
}

func waitForDeleting(t *testing.T, m *Manager, id string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for _, info := range m.List() {
			if info.BotID == id && info.State == "deleting" {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("purge did not claim the pinned Bot")
		default:
			runtime.Gosched()
		}
	}
}

func TestPurgeExcludesManagementWhilePinnedAndRestoresBusyRuntime(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "pinned")
	b, r := purgeTestRuntime(t, a, id)
	cfg := cloneConfig(b.cfg)
	server, _, _ := purgeNATSPeer(t, false)
	bus, err := natsbus.Connect(r.ctx, []string{server}, "purge-rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	r.bus = bus
	control := make(chan struct{}, 1)
	if _, err := bus.Subscribe("purge.control", func(string, []byte) { control <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	_, release, err := a.manager.Acquire(id, true)
	if err != nil {
		t.Fatal(err)
	}
	// The admitted callback is work even though it has no durable inbox record.
	started, finish, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	r.messaging.SetMessageHandler(messaging.MessageHandlerFunc(func(messaging.IncomingMessage) (messaging.HandlerResult, error) {
		close(started)
		<-finish
		return messaging.HandlerResult{}, nil
	}))
	go func() { finished <- r.messaging.HandleLocalNotification(messaging.IncomingMessage{}) }()
	<-started
	purged := make(chan error, 1)
	go func() { _, err := a.manager.Purge(id); purged <- err }()
	waitForDeleting(t, a.manager, id)
	for _, op := range []string{"start", "stop", "archive", "restore"} {
		if _, err := a.manager.Transition(id, op); !purgeFailureCode(err, "bot.busy") {
			t.Errorf("%s admitted during purge: %v", op, err)
		}
	}
	if _, err := a.manager.Update(id, cfg); !purgeFailureCode(err, "bot.busy") {
		t.Errorf("configuration write admitted during purge: %v", err)
	}
	if _, err := a.manager.Config(id); !purgeFailureCode(err, "bot.busy") {
		t.Errorf("configuration read admitted during purge: %v", err)
	}
	for _, write := range []bool{false, true} {
		if _, done, err := a.manager.Acquire(id, write); !purgeFailureCode(err, "bot.busy") {
			if done != nil {
				done()
			}
			t.Errorf("handler admitted during purge (write=%v): %v", write, err)
		}
	}
	release()
	if err := <-purged; !purgeFailureCode(err, "bot.busy") {
		t.Errorf("active local work was deleted: %v", err)
	}
	close(finish)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if info, err := a.manager.Info(id); err != nil || info.State != "running" || r.ctx.Err() != nil {
		t.Fatalf("busy rollback stopped runtime: %+v, %v, %v", info, err, r.ctx.Err())
	}
	r.messaging.SetMessageHandler(messaging.MessageHandlerFunc(func(messaging.IncomingMessage) (messaging.HandlerResult, error) {
		return messaging.HandlerResult{}, nil
	}))
	if err := r.messaging.HandleLocalNotification(messaging.IncomingMessage{}); err != nil {
		t.Fatalf("busy rollback did not reopen local admissions: %v", err)
	}
	if err := bus.Publish("purge.control", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-control:
	case <-time.After(5 * time.Second):
		t.Fatal("busy rollback did not resume core transport callbacks")
	}
}

func TestPurgeCatalogFailureRestoresLiveRuntime(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "catalog-failure")
	_, r := purgeTestRuntime(t, a, id)
	catalog := filepath.Join(a.manager.cfg.DataDir, "bots.json")
	if err := os.Remove(catalog); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(catalog, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.manager.Purge(id); err == nil {
		t.Fatal("catalog failure was ignored")
	}
	if info, err := a.manager.Info(id); err != nil || info.State != "running" || r.ctx.Err() != nil {
		t.Fatalf("catalog rollback did not preserve runtime: %+v, %v", info, err)
	}
	if err := r.messaging.HandleLocalNotification(messaging.IncomingMessage{}); err != nil {
		t.Fatalf("catalog rollback left admissions frozen: %v", err)
	}
}

func TestShutdownJoinsClaimedPurgeBeforeClosingRuntime(t *testing.T) {
	a := testDaemon(t)
	a.manager.cfg.NATSServers = nil
	id := newTestBot(t, a, "purge-shutdown")
	_, r := purgeTestRuntime(t, a, id)
	_, release, err := a.manager.Acquire(id, false)
	if err != nil {
		t.Fatal(err)
	}
	purged := make(chan error, 1)
	go func() { _, err := a.manager.Purge(id); purged <- err }()
	waitForDeleting(t, a.manager, id)
	stopped := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { stopped <- a.manager.Shutdown(ctx) }()
	release()
	if err := <-purged; err != nil {
		t.Fatalf("purge failed during shutdown: %v", err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if r.ctx.Err() == nil {
		t.Fatal("shutdown returned before purged runtime closed")
	}
}
