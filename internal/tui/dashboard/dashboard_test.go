package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
					NodeID:       "node-beta",
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
		"node-beta",
		"Keys: q quit",
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

func TestPeerListLinesShowFullNodeIDWhenWidthAllows(t *testing.T) {
	const nodeID = "n1-1234567890abcdef1234567890abcdef"

	model := model{
		snapshot: snapshot{
			Peers: []types.Peer{
				{
					NodeID:       nodeID,
					AuthStatus:   types.AuthAuthenticated,
					TrustStatus:  types.TrustTrusted,
					AgentProduct: "openclaw",
					Version:      "v0.0.3",
				},
			},
		},
	}

	lines := model.peerListLines(4, 78)
	if got := parseTaggedOnly(lines[1]); !strings.Contains(got, nodeID) {
		t.Fatalf("expected peer row to contain full nodeId %q, got %q", nodeID, got)
	}
	for _, line := range lines {
		if got := visibleLen(parseTaggedOnly(line)); got > 78 {
			t.Fatalf("line exceeds width: got %d want <= 78, line=%q", got, line)
		}
	}
}

func TestMessageListLinesFitPanelWidth(t *testing.T) {
	model := model{
		snapshot: snapshot{
			Messages: []protocol.MessageEnvelope{
				{
					Type:    "conversation.message",
					From:    "node-with-a-very-long-name",
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
		From:    "node-jack",
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
		"trustMode":    "explicit",
		"logLevel":     "debug",
		"logAddSource": true,
		"natsServers":  []any{"nats://a:4222", "nats://b:4222"},
	}
	m.syncConfigFields(data)

	check := map[string]string{
		"trustMode":    "explicit",
		"logLevel":     "debug",
		"logAddSource": "true",
		"natsServers":  "nats://a:4222,nats://b:4222",
	}
	for _, f := range m.cfgState.fields {
		if expected, ok := check[f.Key]; ok {
			if f.Value != expected {
				t.Fatalf("field %s: expected %q, got %q", f.Key, expected, f.Value)
			}
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

func parseTaggedOnly(line string) string {
	_, text := parseTaggedLine(line)
	return text
}
