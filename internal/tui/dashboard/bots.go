package dashboard

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"clawsynapse/pkg/types"
)

type botInfo struct {
	BotID       string `json:"botId"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	State       string `json:"state"`
}
type botsMsg struct {
	bots []botInfo
	err  error
}
type scopedClient struct {
	Client
	botID string
}

func (c scopedClient) endpoint(endpoint string) (string, error) {
	if c.botID == "" {
		return "", fmt.Errorf("select a local Bot first")
	}
	if !strings.HasPrefix(endpoint, "/") || strings.Contains(endpoint, "..") || strings.HasPrefix(endpoint, "/v2/") {
		return "", fmt.Errorf("invalid Bot endpoint")
	}
	return "/v2/bots/" + url.PathEscape(c.botID) + endpoint, nil
}
func (c scopedClient) Get(ctx context.Context, endpoint string) (types.APIResult, error) {
	path, err := c.endpoint(endpoint)
	if err != nil {
		return types.APIResult{}, err
	}
	return c.Client.Get(ctx, path)
}
func (c scopedClient) Put(ctx context.Context, endpoint string, body any) (types.APIResult, error) {
	path, err := c.endpoint(endpoint)
	if err != nil {
		return types.APIResult{}, err
	}
	return c.Client.Put(ctx, path, body)
}
func resolveBotSelection(selection string, bots []botInfo) (string, error) {
	found := ""
	for _, bot := range bots {
		if bot.BotID == selection {
			return bot.BotID, nil
		}
		if strings.HasPrefix(bot.BotID, selection) {
			if found != "" {
				return "", fmt.Errorf("ambiguous Bot prefix %q", selection)
			}
			found = bot.BotID
		}
	}
	if found == "" {
		return "", fmt.Errorf("unknown Bot %q", selection)
	}
	return found, nil
}
func (m model) botsCmd() tea.Cmd {
	client, timeout := m.client, m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result, err := client.Get(ctx, "/v2/bots")
		var bots []botInfo
		if err == nil {
			err = decodeItems(result.Data, &bots)
		}
		return botsMsg{bots: bots, err: err}
	}
}
func (m *model) selectBot(id string) {
	m.scope++
	m.botID = id
	m.selectingBot = false
	m.snapshot = snapshot{}
	m.lastUpdated = time.Time{}
	m.loading = true
	m.errText = ""
	m.cursors = [dashboardTabCount]int{}
	m.offsets = [dashboardTabCount]int{}
	m.logsFollowTail = true
	m.cfgState = configEditState{fields: initConfigFields()}
	m.peerFilter = peerFilterState{}
	m.activeTab = 0
}
