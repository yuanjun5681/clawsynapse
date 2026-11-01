package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"clawsynapse/internal/identity"
	"clawsynapse/internal/protocol"
	"clawsynapse/pkg/types"
)

type stubDashboardClient struct {
	results   map[string]types.APIResult
	err       error
	putResult types.APIResult
	putErr    error
}

func (s stubDashboardClient) Get(_ context.Context, endpoint string) (types.APIResult, error) {
	if s.err != nil {
		return types.APIResult{}, s.err
	}
	return s.results[endpoint], nil
}

func (s stubDashboardClient) Put(_ context.Context, _ string, _ any) (types.APIResult, error) {
	return s.putResult, s.putErr
}

type stubLogProvider struct {
	text string
	err  error
}

func (s stubLogProvider) ReadLogs(_ context.Context, _ int) (string, error) {
	return s.text, s.err
}

func TestDashboardViewRendersStructuredPanels(t *testing.T) {
	model := model{
		apiAddr:     "127.0.0.1:18080",
		version:     "dev",
		logSource:   "journalctl -u clawsynapsed.service",
		logLines:    dashboardDefaultLogLines,
		width:       120,
		height:      32,
		activeTab:   1,
		lastUpdated: time.Unix(0, 0),
		snapshot: snapshot{
			Health: health{
				PeersCount: 1,
				NATS: natsState{
					Connected: true,
					Status:    "CONNECTED",
					InMsgs:    10,
					OutMsgs:   5,
				},
			},
			Peers: []types.Peer{
				{
					BotID:        "bot-beta",
					AuthStatus:   "authenticated",
					TrustStatus:  "trusted",
					AgentProduct: "openclaw",
					Version:      "v0.0.3",
				},
			},
		},
	}

	view := fmt.Sprint(model.View())
	for _, needle := range []string{
		"ClawSynapse Dashboard",
		"Peer Detail",
		"bot-beta",
		"/ filter",
	} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected view to contain %q, got:\n%s", needle, view)
		}
	}
}

func TestHeaderViewKeepsReadyWhileLoading(t *testing.T) {
	model := model{
		apiAddr:   "127.0.0.1:18080",
		version:   "dev",
		logSource: "journalctl -u clawsynapsed.service",
		logLines:  dashboardDefaultLogLines,
		width:     120,
		height:    32,
		loading:   true,
		snapshot:  snapshot{},
	}
	model.recalcLayout()

	header := model.headerView(model.width)
	if strings.Contains(header, "SYNCING") {
		t.Fatalf("expected header to stop showing syncing status, got:\n%s", header)
	}
	if !strings.Contains(header, "READY") {
		t.Fatalf("expected header to keep ready status, got:\n%s", header)
	}
}

func TestPeerListLinesShowFullBotIDWhenWidthAllows(t *testing.T) {
	const botID = "n1-1234567890abcdef1234567890abcdef"
	const did = "did:key:z6Mkgw6m2D1d8Zk7D1bQp4qJx1vYw9sX3rT5uN7mK2hF4jL6"

	model := model{
		snapshot: snapshot{
			Peers: []types.Peer{
				{
					BotID:        botID,
					DID:          did,
					DisplayName:  "beta-bot",
					AuthStatus:   types.AuthAuthenticated,
					TrustStatus:  types.TrustTrusted,
					AgentProduct: "openclaw",
					Version:      "v0.0.3",
				},
			},
		},
	}

	lines := model.peerListLines(4, 120)
	row := parseTaggedOnly(lines[1])
	if !strings.Contains(row, botID) {
		t.Fatalf("expected peer row to contain full botId %q, got %q", botID, row)
	}
	if !strings.Contains(row, "beta-bot") {
		t.Fatalf("expected peer row to contain display name, got %q", row)
	}
	if fp := identity.FingerprintFromDID(did); !strings.Contains(row, fp) {
		t.Fatalf("expected peer row to contain fingerprint %q, got %q", fp, row)
	}
	for _, line := range lines {
		if got := visibleLen(parseTaggedOnly(line)); got > 120 {
			t.Fatalf("line exceeds width: got %d want <= 120, line=%q", got, line)
		}
	}
}

func TestPeerListLinesDropBotIDBeforeFingerprint(t *testing.T) {
	const did = "did:key:z6Mkgw6m2D1d8Zk7D1bQp4qJx1vYw9sX3rT5uN7mK2hF4jL6"
	peers := []types.Peer{
		{
			BotID:       "n1-1234567890abcdef1234567890abcdef",
			DID:         did,
			LocalAlias:  "edge-1",
			AuthStatus:  types.AuthAuthenticated,
			TrustStatus: types.TrustTrusted,
		},
	}
	model := model{snapshot: snapshot{Peers: peers}}

	// Medium width: name and fingerprint stay, full botId is dropped.
	lines := model.peerListLines(4, 80)
	row := parseTaggedOnly(lines[1])
	if !strings.Contains(row, "edge-1") {
		t.Fatalf("expected local alias in row, got %q", row)
	}
	if fp := identity.FingerprintFromDID(did); !strings.Contains(row, fp) {
		t.Fatalf("expected fingerprint in medium-width row, got %q", row)
	}
	if strings.Contains(row, "n1-1234567890abcdef1234567890abcdef") {
		t.Fatalf("expected botId to be dropped at medium width, got %q", row)
	}
	for _, line := range lines {
		if got := visibleLen(parseTaggedOnly(line)); got > 80 {
			t.Fatalf("line exceeds width: got %d want <= 80, line=%q", got, line)
		}
	}
}

func TestMessageListLinesFitPanelWidth(t *testing.T) {
	model := model{
		snapshot: snapshot{
			Messages: []protocol.MessageEnvelope{
				{
					Type:    "conversation.message",
					From:    "bot-with-a-very-long-name",
					To:      "trustmesh-dev",
					Payload: protocol.MessagePayload{Text: `{"conversation_id":"abcd","summary":"long preview body for the dashboard list"}`},
					Ts:      1711111111111,
				},
			},
		},
	}

	lines := model.messageListLines(6, 54)
	for _, line := range lines {
		if got := visibleLen(parseTaggedOnly(line)); got > 54 {
			t.Fatalf("line exceeds width: got %d want <= 54, line=%q", got, line)
		}
	}
}

func TestFormatMessageRowCollapsesEmbeddedNewlinesInPreview(t *testing.T) {
	cols := messageColumnsForWidth(54)
	row := formatMessageRow(" ", protocol.MessageEnvelope{
		Type:    "conversation.message",
		From:    "bot-jack",
		To:      "trustmesh-dev",
		Payload: protocol.MessagePayload{Text: "line one\n\nline two\tline three"},
		Ts:      1711111111111,
	}, cols)

	if strings.Contains(row, "\n") {
		t.Fatalf("expected single-line preview, got %q", row)
	}
	if !strings.Contains(row, "line one li") {
		t.Fatalf("expected normalized preview prefix, got %q", row)
	}
	if strings.Contains(row, "line one\n") {
		t.Fatalf("expected newlines to be removed from preview, got %q", row)
	}
}

func TestRenderPanelWrapsWideCharactersWithoutCorruption(t *testing.T) {
	panel := renderPanel("Message Detail", 36, 8, []string{
		"这是一个包含中文和 ASCII JSON 的长消息内容，用来验证换行不会打断字符边界。",
	})

	if strings.Contains(panel, "\ufffd") {
		t.Fatalf("panel contains replacement rune, got:\n%s", panel)
	}
}

func TestRenderPanelKeepsLayoutForMultilineContent(t *testing.T) {
	panel := renderPanel("Message Detail", 32, 8, []string{
		taggedLine(dashboardTagSection, arrowR+" Content"),
		"  first line\n\n  second line\n  third line",
	})

	if got := countLines(panel); got != 8 {
		t.Fatalf("expected panel height 8, got %d\n%s", got, panel)
	}
	lines := strings.Split(panel, "\n")
	for i, line := range lines[1 : len(lines)-1] {
		plain := stripANSI(line)
		if !strings.HasPrefix(plain, boxV) || !strings.HasSuffix(plain, boxV) {
			t.Fatalf("content line %d lost panel border: %q", i+1, plain)
		}
	}
}

func TestSyncConfigFieldsPopulatesValues(t *testing.T) {
	m := model{
		cfgState: configEditState{
			fields: initConfigFields(),
		},
	}

	data := map[string]any{
		"trustMode":   "explicit",
		"displayName": "edge-bot",
		"labels":      map[string]any{"env": "prod", "caps": []any{"chat", "tools"}},
	}
	m.syncConfigFields(data)

	check := map[string]string{
		"trustMode":   "explicit",
		"displayName": "edge-bot",
		"labels":      `{"caps":["chat","tools"],"env":"prod"}`,
	}
	seen := make(map[string]bool, len(check))
	for _, f := range m.cfgState.fields {
		if expected, ok := check[f.Key]; ok {
			seen[f.Key] = true
			if f.Value != expected {
				t.Fatalf("field %s: expected %q, got %q", f.Key, expected, f.Value)
			}
		}
	}
	for key := range check {
		if !seen[key] {
			t.Fatalf("expected config field %q to exist", key)
		}
	}
}

func TestSyncConfigFieldsSkipsDuringEdit(t *testing.T) {
	m := model{
		cfgState: configEditState{
			fields:  initConfigFields(),
			editing: true,
		},
	}

	data := map[string]any{"trustMode": "explicit"}
	m.syncConfigFields(data)

	for _, f := range m.cfgState.fields {
		if f.Key == "trustMode" && f.Value == "explicit" {
			t.Fatal("syncConfigFields should skip during editing")
		}
	}
}

func TestConfigViewShowsUnsavedIndicator(t *testing.T) {
	m := model{
		width:     120,
		height:    32,
		activeTab: 4,
		cfgState: configEditState{
			fields: initConfigFields(),
			dirty:  true,
		},
	}
	m.recalcLayout()

	view := m.configView(m.width, m.height)
	if !strings.Contains(view, "Unsaved") {
		t.Fatal("expected unsaved changes indicator")
	}
}

func TestParsePeerFilterMatches(t *testing.T) {
	peer := types.Peer{
		BotID:       "n1-aabbccddeeff0011aabbccddeeff0011",
		DisplayName: "Edge Bot",
		LocalAlias:  "Kitchen",
		Labels: types.Labels{
			"env":  types.LabelString("prod"),
			"caps": types.LabelSet([]string{"chat", "tools"}),
		},
	}

	cases := []struct {
		raw   string
		match bool
	}{
		{"env=prod", true},       // exact scalar match
		{"env~=prod", false},     // member match never hits scalars
		{"env=staging", false},   // exact mismatch
		{"caps~=chat", true},     // set member match
		{"caps=chat", false},     // exact match never hits sets
		{"caps~=missing", false}, // set member mismatch
		{"zone=eu", false},       // unknown key
		{"edge", true},           // substring over display name
		{"EDGE", true},           // case-insensitive
		{"kitchen", true},        // substring over local alias
		{"aabbccdd", true},       // substring over bot ID
		{"prod,chat", false},     // plain text is never comma-split
		{"nope", false},
		{"", true}, // empty filter matches everything
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := parsePeerFilter(tc.raw).matches(peer); got != tc.match {
				t.Fatalf("parsePeerFilter(%q).matches = %v, want %v", tc.raw, got, tc.match)
			}
		})
	}
}

func TestFilteredPeersHidesNonMatching(t *testing.T) {
	model := model{
		snapshot: snapshot{
			Peers: []types.Peer{
				{BotID: "n1-aaa", Labels: types.Labels{"env": types.LabelString("prod")}},
				{BotID: "n1-bbb", Labels: types.Labels{"env": types.LabelString("staging")}},
			},
		},
		peerFilter: peerFilterState{raw: "env=prod"},
	}
	peers := model.filteredPeers()
	if len(peers) != 1 || peers[0].BotID != "n1-aaa" {
		t.Fatalf("expected only n1-aaa to match, got %#v", peers)
	}
}

func TestFormatLabelSetVsScalar(t *testing.T) {
	if got := formatLabel("env", types.LabelString("prod")); got != "env=prod" {
		t.Fatalf("expected scalar rendering, got %q", got)
	}
	if got := formatLabel("caps", types.LabelSet([]string{"chat", "tools"})); got != "caps=[chat,tools]" {
		t.Fatalf("expected set rendering, got %q", got)
	}
	lines := formatLabelsLines(types.Labels{
		"caps": types.LabelSet([]string{"chat"}),
		"env":  types.LabelString("prod"),
	}, "  ")
	if len(lines) != 2 || lines[0] != "  caps=[chat]" || lines[1] != "  env=prod" {
		t.Fatalf("expected sorted label lines, got %#v", lines)
	}
	if got := formatLabelsLines(nil, "  "); got != nil {
		t.Fatalf("expected nil for empty labels, got %#v", got)
	}
}

func TestBuildConfigFromFieldsIncludesDisplayNameAndLabels(t *testing.T) {
	fields := initConfigFields()
	for i := range fields {
		switch fields[i].Key {
		case "displayName":
			fields[i].Changed = true
			fields[i].Value = "edge-bot"
		case "labels":
			fields[i].Changed = true
			fields[i].Value = `{"env":"prod","caps":["chat","tools"]}`
		}
	}

	cfg, err := buildBotConfigFromFields(fields, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DisplayName != "edge-bot" {
		t.Fatalf("expected displayName to round-trip, got %q", cfg.DisplayName)
	}
	if !cfg.Labels.MatchExact("env", "prod") {
		t.Fatalf("expected env=prod label, got %#v", cfg.Labels)
	}
	if got := cfg.Labels.Caps(); len(got) != 2 || got[0] != "chat" || got[1] != "tools" {
		t.Fatalf("expected caps set, got %#v", got)
	}
}

func TestBuildConfigFromFieldsRejectsBadLabels(t *testing.T) {
	for _, raw := range []string{
		`not json`,
		`["chat"]`,        // not a JSON object
		`{"caps":"chat"}`, // reserved caps key must be a set
		`{"bad key":"v"}`, // invalid label key
	} {
		fields := initConfigFields()
		for i := range fields {
			if fields[i].Key == "labels" {
				fields[i].Value = raw
				fields[i].Changed = true
			}
		}
		if _, err := buildBotConfigFromFields(fields, nil); err == nil {
			t.Fatalf("expected error for labels %q, got nil", raw)
		}
	}
}

func parseTaggedOnly(line string) string {
	_, text := parseTaggedLine(line)
	return text
}
