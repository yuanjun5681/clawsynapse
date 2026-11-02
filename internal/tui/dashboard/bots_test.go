package dashboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"clawsynapse/pkg/types"
)

type scopeClient struct {
	paths  []string
	bodies []any
}

func (c *scopeClient) Get(_ context.Context, path string) (types.APIResult, error) {
	c.paths = append(c.paths, path)
	return types.APIResult{OK: true}, nil
}
func (c *scopeClient) Put(_ context.Context, path string, body any) (types.APIResult, error) {
	c.paths = append(c.paths, path)
	c.bodies = append(c.bodies, body)
	return types.APIResult{OK: true}, nil
}

func TestBotSwitchResetsIdentityScopedStateAndRejectsOldResults(t *testing.T) {
	m := model{botID: "a", scope: 1, snapshot: snapshot{Logs: "private-a", ConfigData: map[string]any{"revision": float64(3)}}, lastUpdated: time.Now(), activeTab: 4, peerFilter: peerFilterState{raw: "secret", editing: true}, cfgState: configEditState{dirty: true, editing: true, fields: initConfigFields()}, cursors: [dashboardTabCount]int{1, 2, 3, 4, 5}, offsets: [dashboardTabCount]int{1, 2, 3, 4, 5}}
	m.selectBot("b")
	if m.botID != "b" || m.snapshot.Logs != "" || m.snapshot.ConfigData != nil || m.peerFilter.raw != "" || m.cfgState.dirty || m.cfgState.editing || !m.lastUpdated.IsZero() || m.activeTab != 0 {
		t.Fatalf("Bot state leaked: %#v", m)
	}
	for _, n := range m.cursors {
		if n != 0 {
			t.Fatal("cursor leaked")
		}
	}
	for _, n := range m.offsets {
		if n != 0 {
			t.Fatal("offset leaked")
		}
	}
	updated, _ := m.Update(refreshMsg{scope: 1, snapshot: snapshot{Logs: "late-a"}})
	m = updated.(model)
	if m.snapshot.Logs != "" || !m.loading {
		t.Fatal("old refresh mutated current Bot")
	}
	updated, _ = m.Update(configSaveMsg{scope: 1})
	m = updated.(model)
	if m.cfgState.statusMsg != "" {
		t.Fatal("old save mutated current Bot")
	}
	if !strings.Contains(m.headerView(120), "Active Bot: b") {
		t.Fatal("active identity not shown")
	}
}

func TestScopedClientRequiresBotAndBuildsExactPaths(t *testing.T) {
	client := &scopeClient{}
	if _, err := (scopedClient{Client: client}).Get(context.Background(), "/peers"); err == nil {
		t.Fatal("implicit Bot accepted")
	}
	scoped := scopedClient{Client: client, botID: "a"}
	if _, err := scoped.Get(context.Background(), "/peers"); err != nil {
		t.Fatal(err)
	}
	if _, err := scoped.Put(context.Background(), "/config", map[string]any{"revision": 3}); err != nil {
		t.Fatal(err)
	}
	if len(client.paths) != 2 || client.paths[0] != "/v2/bots/a/peers" || client.paths[1] != "/v2/bots/a/config" {
		t.Fatalf("wrong scope: %v", client.paths)
	}
}

func TestBotListNeverAutomaticallyChoosesFirst(t *testing.T) {
	m := model{selectingBot: true}
	updated, _ := m.Update(botsMsg{bots: []botInfo{{BotID: "a"}, {BotID: "b"}}})
	m = updated.(model)
	if m.botID != "" || !m.selectingBot {
		t.Fatal("implicitly selected Bot")
	}
	if _, err := resolveBotSelection("n1-ab", []botInfo{{BotID: "n1-abc"}, {BotID: "n1-abd"}}); err == nil {
		t.Fatal("ambiguous prefix accepted")
	}
}

func TestBotConfigEditsPreserveRevisionAndUneditedBinding(t *testing.T) {
	fields := initConfigFields()
	for i := range fields {
		if fields[i].Key == "displayName" {
			fields[i].Changed = true
			fields[i].Value = "new"
		}
	}
	cfg, err := buildBotConfigFromFields(fields, map[string]any{"role": "agent", "revision": float64(7), "bindingRevision": "binding-a", "harness": map[string]any{"adapter": "opencode", "model": "free-model", "timeout": "1m"}, "displayName": "old"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 7 || cfg.BindingRevision != "binding-a" || cfg.Harness.Model != "free-model" || cfg.DisplayName != "new" {
		t.Fatalf("configuration lost scope/CAS fields: %#v", cfg)
	}
}
