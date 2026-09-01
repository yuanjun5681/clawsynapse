package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"clawsynapse/internal/api"
	appconfig "clawsynapse/internal/config"
)

func Run(args []string, stdout, stderr io.Writer, opts Options) error {
	_ = stdout

	cfg, err := parseArgs(args, stderr, opts)
	if err != nil {
		return err
	}

	client := opts.Client
	if client == nil {
		client = api.NewClient(cfg.APIAddr, cfg.Timeout)
	}

	logLines := opts.LogLines
	if logLines <= 0 {
		logLines = dashboardDefaultLogLines
	}

	fields := initConfigFields()
	localCfg, cfgPath, cfgErr := loadLocalConfig()
	if cfgErr == nil {
		populateFieldsFromConfig(fields, localCfg)
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
		cfgState:       configEditState{fields: fields, configPath: cfgPath},
	}

	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

func parseArgs(args []string, stderr io.Writer, opts Options) (config, error) {
	defaultAPIAddr := strings.TrimSpace(opts.APIAddr)
	if defaultAPIAddr == "" {
		defaultAPIAddr = strings.TrimSpace(os.Getenv("LOCAL_API_ADDR"))
	}
	if defaultAPIAddr == "" {
		defaultAPIAddr = "127.0.0.1:18080"
	}

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
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if len(fs.Args()) > 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return cfg, nil
}

func loadSnapshot(ctx context.Context, client Client, logs LogProvider, logLines int) (snapshot, error) {
	var snap snapshot

	healthResult, err := client.Get(ctx, "/v1/health")
	if err != nil {
		return snapshot{}, err
	}
	if err := decodeData(healthResult.Data, &snap.Health); err != nil {
		return snapshot{}, fmt.Errorf("decode health: %w", err)
	}

	peersResult, err := client.Get(ctx, "/v1/peers")
	if err != nil {
		return snapshot{}, err
	}
	if err := decodeItems(peersResult.Data, &snap.Peers); err != nil {
		return snapshot{}, fmt.Errorf("decode peers: %w", err)
	}

	messagesResult, err := client.Get(ctx, "/v1/messages")
	if err != nil {
		return snapshot{}, err
	}
	if err := decodeItems(messagesResult.Data, &snap.Messages); err != nil {
		return snapshot{}, fmt.Errorf("decode messages: %w", err)
	}
	if logs != nil {
		logText, err := logs.ReadLogs(ctx, logLines)
		if err != nil {
			snap.Logs = "log error: " + err.Error()
		} else {
			snap.Logs = logText
		}
	}

	configResult, err := client.Get(ctx, "/v1/config")
	if err == nil {
		if cfgData, ok := configResult.Data["config"]; ok {
			if m, ok := cfgData.(map[string]any); ok {
				snap.ConfigData = m
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
	"nodeId": true, "service": true, "component": true,
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
	entry.NodeID = strField("nodeId")
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

func loadLocalConfig() (appconfig.Config, string, error) {
	cfg, err := appconfig.LoadFromOS(nil)
	if err != nil {
		return appconfig.Config{}, "", err
	}
	return cfg, cfg.ConfigPath, nil
}

func populateFieldsFromConfig(fields []configField, cfg appconfig.Config) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	for i := range fields {
		f := &fields[i]
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
		default:
			f.Value = fmt.Sprintf("%v", val)
		}
	}
}

func initConfigFields() []configField {
	return []configField{
		{Label: "NATS Servers", Key: "natsServers", Group: "Network", Kind: cfkStringSlice},
		{Label: "Local API Addr", Key: "localApiAddr", Group: "Network", Kind: cfkText},
		{Label: "Heartbeat Interval", Key: "heartbeatInterval", Group: "Network", Kind: cfkText},
		{Label: "Announce TTL", Key: "announceTtl", Group: "Network", Kind: cfkText},
		{Label: "Agent Product", Key: "agentProduct", Group: "Discovery", Kind: cfkText},
		{Label: "Agent Version", Key: "agentVersion", Group: "Discovery", Kind: cfkText},
		{Label: "Capabilities", Key: "capabilities", Group: "Discovery", Kind: cfkStringSlice},
		{Label: "Trust Mode", Key: "trustMode", Group: "Security", Kind: cfkEnum, Enums: []string{"open", "tofu", "explicit"}},
		{Label: "Trust Auto Approve", Key: "trustAutoApprove", Group: "Security", Kind: cfkBool},
		{Label: "Agent Adapter", Key: "agentAdapter", Group: "Agent", Kind: cfkEnum, Enums: []string{"default", "openclaw", "opencode", "webhook"}},
		{Label: "Agent Adapter Timeout", Key: "agentAdapterTimeout", Group: "Agent", Kind: cfkText},
		{Label: "Webhook URL", Key: "webhookUrl", Group: "Agent", Kind: cfkText},
		{Label: "Deliverable Prefixes", Key: "deliverablePrefixes", Group: "Agent", Kind: cfkStringSlice},
		{Label: "Data Dir", Key: "dataDir", Group: "Storage", Kind: cfkText},
		{Label: "Identity Key Path", Key: "identityKeyPath", Group: "Storage", Kind: cfkText},
		{Label: "Identity Pub Path", Key: "identityPubPath", Group: "Storage", Kind: cfkText},
		{Label: "Transfer Dir", Key: "transferDir", Group: "Transfer", Kind: cfkText},
		{Label: "Transfer Max Size", Key: "transferMaxFileSize", Group: "Transfer", Kind: cfkText},
		{Label: "Transfer TTL", Key: "transferTtl", Group: "Transfer", Kind: cfkText},
		{Label: "Log Level", Key: "logLevel", Group: "Logging", Kind: cfkEnum, Enums: []string{"debug", "info", "warn", "error"}},
		{Label: "Log Format", Key: "logFormat", Group: "Logging", Kind: cfkEnum, Enums: []string{"json", "text"}},
		{Label: "Log Add Source", Key: "logAddSource", Group: "Logging", Kind: cfkBool},
	}
}

func (m *model) syncConfigFields(data map[string]any) {
	if data == nil || m.cfgState.editing {
		return
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
