package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
)

type OpenClawConfig struct {
	NodeID  string
	AgentID string
	Logger  *slog.Logger
}

type OpenClawAdapter struct {
	nodeID  string
	agentID string
	log     *slog.Logger
	execCmd func(ctx context.Context, args ...string) ([]byte, error)
}

func NewOpenClawAdapter(cfg OpenClawConfig) (*OpenClawAdapter, error) {
	if strings.TrimSpace(cfg.AgentID) == "" {
		return nil, errors.New("openclaw agent id is required")
	}

	return &OpenClawAdapter{
		nodeID:  strings.TrimSpace(cfg.NodeID),
		agentID: strings.TrimSpace(cfg.AgentID),
		log:     cfg.Logger,
		execCmd: defaultExecCmd,
	}, nil
}

func (a *OpenClawAdapter) DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error) {
	msg := formatDeliverMessage(a.nodeID, req)
	sessionID := a.resolveSessionID(req)
	args := []string{
		"agent",
		"--agent", a.agentID,
		"--message", msg,
		"--json",
		"--session-id", sessionID,
	}
	a.logCommand(args)

	out, err := a.execCmd(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("openclaw agent command: %w", err)
	}

	return parseOpenClawResult(out)
}

func (a *OpenClawAdapter) GetStatus(ctx context.Context) (*AgentStatus, error) {
	out, err := a.execCmd(ctx, "--version")
	if err != nil {
		return &AgentStatus{Healthy: false}, err
	}
	if len(out) == 0 {
		return &AgentStatus{Healthy: false}, nil
	}
	return &AgentStatus{Healthy: true}, nil
}

type openClawResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
	Result struct {
		Payloads []struct {
			Text string `json:"text"`
		} `json:"payloads"`
		Meta struct {
			DurationMs int `json:"durationMs"`
		} `json:"meta"`
	} `json:"result"`
	Error string `json:"error"`
}

func parseOpenClawResult(data []byte) (*DeliverMessageResult, error) {
	var resp openClawResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse openclaw response: %w", err)
	}

	if resp.Error != "" {
		return &DeliverMessageResult{
			Success: false,
			RunID:   resp.RunID,
			Error:   resp.Error,
		}, nil
	}

	if resp.Status != "ok" {
		return &DeliverMessageResult{
			Success: false,
			RunID:   resp.RunID,
			Error:   fmt.Sprintf("openclaw status: %s", resp.Status),
		}, nil
	}

	var reply string
	for _, p := range resp.Result.Payloads {
		text := strings.TrimSpace(p.Text)
		if text != "" {
			reply = text
			break
		}
	}

	return &DeliverMessageResult{
		Success:  true,
		Accepted: true,
		RunID:    resp.RunID,
		Reply:    reply,
	}, nil
}

func (a *OpenClawAdapter) resolveSessionID(req DeliverMessageRequest) string {
	if s := strings.TrimSpace(req.SessionKey); s != "" {
		return s
	}
	from := req.From
	if from == "" {
		from = "_anon"
	}
	return "cs-" + from + "-" + a.nodeID
}

func (a *OpenClawAdapter) logCommand(args []string) {
	if a.log == nil {
		return
	}
	a.log.Info("executing openclaw agent command",
		slog.String("agentID", a.agentID),
		slog.String("sessionID", redactedSessionID(args)),
		slog.String("command", formatOpenClawCommandForLog(args)),
	)
}

func formatDeliverMessage(localNodeID string, req DeliverMessageRequest) string {
	var b strings.Builder
	b.WriteString("[clawsynapse")
	if req.From != "" {
		b.WriteString(" from=")
		b.WriteString(req.From)
	}
	if localNodeID != "" {
		b.WriteString(" to=")
		b.WriteString(localNodeID)
	}
	if req.SessionKey != "" {
		b.WriteString(" session=")
		b.WriteString(req.SessionKey)
	}
	b.WriteString("]\n")
	b.WriteString(req.Message)
	return b.String()
}

func formatOpenClawCommandForLog(args []string) string {
	logArgs := append([]string(nil), args...)
	for i := 0; i < len(logArgs)-1; i++ {
		if logArgs[i] == "--message" {
			logArgs[i+1] = truncateForLog(logArgs[i+1], 240)
			break
		}
	}

	parts := make([]string, 0, len(logArgs)+1)
	parts = append(parts, "openclaw")
	for _, arg := range logArgs {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func truncateForLog(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const suffixTemplate = "...(truncated, %d bytes total)"
	suffix := fmt.Sprintf(suffixTemplate, len(value))
	if len(suffix) >= limit {
		return value[:limit]
	}
	return value[:limit-len(suffix)] + suffix
}

func redactedSessionID(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--session-id" {
			return args[i+1]
		}
	}
	return ""
}

func defaultExecCmd(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "openclaw", args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			return nil, fmt.Errorf("openclaw exited %s: %s", strconv.Itoa(exitErr.ExitCode()), stderr)
		}
		return nil, err
	}
	return out, nil
}
