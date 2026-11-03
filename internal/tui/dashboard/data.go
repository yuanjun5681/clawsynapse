package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"clawsynapse/internal/api"
	appconfig "clawsynapse/internal/config"
	"clawsynapse/pkg/types"
)

func Run(args []string, stdout, stderr io.Writer, opts Options) error {
	_ = stdout

	cfg, err := parseArgs(args, stderr, opts)
	if err != nil {
		return err
	}

	client := opts.Client
	if client == nil {
		options := []api.ClientOption{}
		if cfg.TokenFile != "" {
			options = append(options, api.WithTokenFile(cfg.TokenFile))
		}
		client = api.NewClient(cfg.APIAddr, cfg.Timeout, options...)
	}

	logLines := opts.LogLines
	if logLines <= 0 {
		logLines = dashboardDefaultLogLines
	}

	m := model{
		client:         client,
		logs:           opts.Logs,
		apiAddr:        cfg.APIAddr,
		timeout:        cfg.Timeout,
		version:        opts.Version,
		logSource:      fallbackString(opts.LogSource, "-"),
		logLines:       logLines,
		loading:        true,
		logsFollowTail: true,
		cfgState:       configEditState{fields: initConfigFields()},
		botID:          cfg.BotID,
		selectingBot:   cfg.BotID == "",
	}

	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

func parseArgs(args []string, stderr io.Writer, opts Options) (config, error) {
	defaultAPIAddr := strings.TrimSpace(opts.APIAddr)

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	cfg := config{
		APIAddr: defaultAPIAddr,
		Timeout: timeout,
	}

	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.APIAddr, "api-addr", cfg.APIAddr, "local API address")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "local API timeout")
	fs.StringVar(&cfg.BotID, "bot", opts.BotID, "explicit local Bot ID or prefix; otherwise choose from the Bot list")
	fs.StringVar(&cfg.TokenFile, "token-file", opts.TokenFile, "private credential for the selected daemon")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if len(fs.Args()) > 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if cfg.APIAddr == "" {
		localCfg, err := appconfig.LoadDaemon(nil)
		if err != nil {
			return config{}, err
		}
		cfg.APIAddr = localCfg.LocalAPIAddr
	}
	return cfg, nil
}

func loadSnapshot(ctx context.Context, client Client, logs LogProvider, logLines int) (snapshot, error) {
	var snap snapshot
	configResult, err := client.Get(ctx, "/config")
	if err != nil {
		return snap, err
	}
	if cfgData, ok := configResult.Data["config"].(map[string]any); ok {
		snap.ConfigData = cfgData
	}

	healthResult, err := client.Get(ctx, "/health")
	if err != nil {
		return snap, err
	}
	if err := decodeData(healthResult.Data, &snap.Health); err != nil {
		return snap, fmt.Errorf("decode health: %w", err)
	}

	peersResult, err := client.Get(ctx, "/peers")
	if err != nil {
		return snap, err
	}
	if err := decodeItems(peersResult.Data, &snap.Peers); err != nil {
		return snap, fmt.Errorf("decode peers: %w", err)
	}

	messagesResult, err := client.Get(ctx, "/messages")
	if err != nil {
		return snap, err
	}
	if err := decodeItems(messagesResult.Data, &snap.Messages); err != nil {
		return snap, fmt.Errorf("decode messages: %w", err)
	}
	if logs != nil {
		logText, err := logs.ReadLogs(ctx, logLines)
		if err != nil {
			snap.Logs = "log error: " + err.Error()
		} else {
			if scoped, ok := client.(scopedClient); ok {
				var lines []string
				for _, line := range strings.Split(logText, "\n") {
					var record map[string]any
					if json.Unmarshal([]byte(line), &record) == nil && record["botId"] == scoped.botID {
						lines = append(lines, line)
					}
				}
				snap.Logs = strings.Join(lines, "\n")
			}
		}
	}

	snap.Updated = time.Now()
	return snap, nil
}

func decodeItems(data map[string]any, dst any) error {
	rawItems, ok := data["items"]
	if !ok {
		return errors.New("missing items field")
	}
	return decodeInto(rawItems, dst)
}

func decodeData(data map[string]any, dst any) error {
	return decodeInto(data, dst)
}

func decodeInto(src any, dst any) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

var logKnownFields = map[string]bool{
	"time": true, "level": true, "msg": true, "source": true,
	"botId": true, "service": true, "component": true,
	"event": true, "peer": true, "from": true, "to": true,
	"messageId": true, "requestId": true, "sessionKey": true,
	"error": true,
}

func parseLogEntry(line string) parsedLogEntry {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return parsedLogEntry{Raw: line}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return parsedLogEntry{Raw: line}
	}
	entry := parsedLogEntry{IsJSON: true}

	if t, ok := obj["time"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			entry.Time = parsed.Format("15:04:05")
		} else if parsed, err := time.Parse(time.RFC3339, t); err == nil {
			entry.Time = parsed.Format("15:04:05")
		} else {
			entry.Time = t
		}
	}

	strField := func(key string) string {
		if v, ok := obj[key].(string); ok {
			return v
		}
		return ""
	}
	entry.Level = strField("level")
	entry.Msg = strField("msg")
	entry.BotID = strField("botId")
	entry.Service = strField("service")
	entry.Comp = strField("component")
	entry.Event = strField("event")
	entry.Peer = strField("peer")
	entry.From = strField("from")
	entry.To = strField("to")
	entry.MsgID = strField("messageId")
	entry.ReqID = strField("requestId")
	entry.SessKey = strField("sessionKey")
	entry.Err = strField("error")

	keys := make([]string, 0, len(obj))
	for k := range obj {
		if !logKnownFields[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		entry.Extra = append(entry.Extra, fmt.Sprintf("%s=%v", k, obj[k]))
	}
	return entry
}

func initConfigFields() []configField {
	return []configField{
		{Label: "Heartbeat Interval", Key: "heartbeatInterval", Group: "Network", Kind: cfkText},
		{Label: "Announce TTL", Key: "announceTtl", Group: "Network", Kind: cfkText},
		{Label: "Agent Product", Key: "agentProduct", Group: "Discovery", Kind: cfkText},
		{Label: "Agent Version", Key: "agentVersion", Group: "Discovery", Kind: cfkText},
		{Label: "Display Name", Key: "displayName", Group: "Discovery", Kind: cfkText},
		{Label: "Labels", Key: "labels", Group: "Discovery", Kind: cfkText},
		{Label: "Trust Mode", Key: "trustMode", Group: "Security", Kind: cfkEnum, Enums: []string{"open", "tofu", "explicit"}},
		{Label: "Trust Auto Approve", Key: "trustAutoApprove", Group: "Security", Kind: cfkBool},
		{Label: "NATS Credentials File", Key: "natsCredentialsFile", Group: "Security", Kind: cfkText},
		{Label: "NATS TLS CA", Key: "natsTLSCAFile", Group: "Security", Kind: cfkText},
		{Label: "NATS TLS Client Certificate", Key: "natsTLSCertFile", Group: "Security", Kind: cfkText},
		{Label: "NATS TLS Client Key", Key: "natsTLSKeyFile", Group: "Security", Kind: cfkText},
		{Label: "NATS TLS Server Name", Key: "natsTLSServerName", Group: "Security", Kind: cfkText},
		{Label: "Harness Binding (JSON)", Key: "harness", Group: "Agent", Kind: cfkText},
		{Label: "Deliverable Prefixes", Key: "deliverablePrefixes", Group: "Agent", Kind: cfkStringSlice},
		{Label: "Transfer Max Size", Key: "transferMaxFileSize", Group: "Transfer", Kind: cfkText},
		{Label: "Transfer TTL", Key: "transferTtl", Group: "Transfer", Kind: cfkText},
		{Label: "Max Concurrent", Key: "maxConcurrent", Group: "Execution", Kind: cfkText},
		{Label: "Max Queued", Key: "maxQueued", Group: "Execution", Kind: cfkText},
		{Label: "File Quota Bytes", Key: "fileQuotaBytes", Group: "Transfer", Kind: cfkText},
	}
}

func (m *model) syncConfigFields(data map[string]any) {
	if data == nil || m.cfgState.editing {
		return
	}
	if data["role"] == "console" {
		fields := m.cfgState.fields[:0]
		for _, field := range m.cfgState.fields {
			if field.Key != "harness" && field.Key != "maxConcurrent" && field.Key != "maxQueued" {
				fields = append(fields, field)
			}
		}
		m.cfgState.fields = fields
	}
	for i := range m.cfgState.fields {
		f := &m.cfgState.fields[i]
		if f.Changed {
			continue
		}
		v, ok := data[f.Key]
		if !ok {
			continue
		}
		switch val := v.(type) {
		case string:
			f.Value = val
		case bool:
			if val {
				f.Value = "true"
			} else {
				f.Value = "false"
			}
		case float64:
			f.Value = fmt.Sprintf("%.0f", val)
		case []any:
			parts := make([]string, 0, len(val))
			for _, item := range val {
				parts = append(parts, fmt.Sprintf("%v", item))
			}
			f.Value = strings.Join(parts, ",")
		case map[string]any:
			raw, err := json.Marshal(val)
			if err != nil {
				continue
			}
			f.Value = string(raw)
		default:
			f.Value = fmt.Sprintf("%v", val)
		}
	}
}

func logLevelTag(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "INFO":
		return dashboardTagLogInfo
	case "WARN", "WARNING":
		return dashboardTagLogWarn
	case "ERROR", "FATAL":
		return dashboardTagLogError
	case "DEBUG":
		return dashboardTagLogDebug
	default:
		return dashboardTagMuted
	}
}

type peerFilterKind int

const (
	peerFilterNone peerFilterKind = iota
	peerFilterText
	peerFilterExact
	peerFilterMember
)

// peerFilterExpr is a parsed peers-tab filter expression.
type peerFilterExpr struct {
	kind  peerFilterKind
	key   string
	value string
}

// parsePeerFilter parses a filter expression: "key=value" matches a remote
// label scalar exactly, "key~=value" matches a remote label set member, and
// anything else is a case-insensitive substring over display name, local
// alias, and Bot ID. Plain text is never split on commas.
func parsePeerFilter(raw string) peerFilterExpr {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return peerFilterExpr{kind: peerFilterNone}
	}
	if i := strings.Index(raw, "~="); i >= 0 {
		key, value := strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+2:])
		if key != "" && value != "" {
			return peerFilterExpr{kind: peerFilterMember, key: key, value: value}
		}
		return peerFilterExpr{kind: peerFilterText, value: raw}
	}
	if i := strings.Index(raw, "="); i >= 0 {
		key, value := strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		if key != "" && value != "" {
			return peerFilterExpr{kind: peerFilterExact, key: key, value: value}
		}
		return peerFilterExpr{kind: peerFilterText, value: raw}
	}
	return peerFilterExpr{kind: peerFilterText, value: raw}
}

func (f peerFilterExpr) matches(peer types.Peer) bool {
	switch f.kind {
	case peerFilterNone:
		return true
	case peerFilterExact:
		return peer.Labels.MatchExact(f.key, f.value)
	case peerFilterMember:
		return peer.Labels.MatchMember(f.key, f.value)
	default:
		needle := strings.ToLower(f.value)
		return strings.Contains(strings.ToLower(peer.DisplayName), needle) ||
			strings.Contains(strings.ToLower(peer.LocalAlias), needle) ||
			strings.Contains(strings.ToLower(peer.BotID), needle)
	}
}

// filteredPeers applies the active peers-tab filter to the snapshot.
func (m model) filteredPeers() []types.Peer {
	expr := parsePeerFilter(m.peerFilter.raw)
	if expr.kind == peerFilterNone {
		return m.snapshot.Peers
	}
	out := make([]types.Peer, 0, len(m.snapshot.Peers))
	for _, peer := range m.snapshot.Peers {
		if expr.matches(peer) {
			out = append(out, peer)
		}
	}
	return out
}

// parseLabelsField parses the Labels config field (a JSON object such as
// {"env":"prod","caps":["chat"]}) and normalizes it. An empty field means no
// labels. Parse or validation errors abort the save instead of silently
// dropping labels.
func parseLabelsField(raw string) (types.Labels, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var labels types.Labels
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil, fmt.Errorf("labels must be a JSON object, e.g. {\"env\":\"prod\"}: %w", err)
	}
	normalized, err := types.NormalizeLabels(labels)
	if err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}
