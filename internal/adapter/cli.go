package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"clawsynapse/internal/protocol"
	"clawsynapse/internal/store"
)

type cliAdapter struct {
	product       string
	botID         string
	binding       string
	workspace     string
	model         string
	log           *slog.Logger
	sessionStore  *store.FSStore
	command       commandFactory
	allowedAgents map[string]struct{}
}

func newCLIAdapter(product, botID, binding, workspace, model string, logger *slog.Logger, sessions *store.FSStore, agentIDs []string) (*cliAdapter, error) {
	if strings.TrimSpace(botID) == "" || strings.TrimSpace(binding) == "" || workspace == "" {
		return nil, errors.New("native bindings require Bot ID, binding revision, and workspace")
	}
	if product == "openclaw" && strings.TrimSpace(model) != "" {
		return nil, errors.New("OpenClaw model selection must be configured on its dedicated engine instance")
	}
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) == string(filepath.Separator) {
		return nil, errors.New("native workspace must be an absolute private directory")
	}
	if err := store.EnsurePrivateDir(workspace); err != nil {
		return nil, fmt.Errorf("prepare native workspace: %w", err)
	}
	allowed := make(map[string]struct{}, len(agentIDs))
	for _, id := range agentIDs {
		allowed[id] = struct{}{}
	}
	return &cliAdapter{
		product: product, botID: botID, binding: binding, workspace: workspace, model: strings.TrimSpace(model), log: logger, sessionStore: sessions, allowedAgents: allowed,
		command: func(ctx context.Context, args ...string) *exec.Cmd { return exec.CommandContext(ctx, product, args...) },
	}, nil
}

func (a *cliAdapter) runCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := a.command(ctx, args...)
	cmd.Dir = a.workspace
	return cmd
}

func (a *cliAdapter) scopedSessionKey(key string) string {
	if key == "" {
		return ""
	}
	scope, _ := json.Marshal([]string{a.botID, a.binding, a.product, key})
	digest := sha256.Sum256(scope)
	return hex.EncodeToString(digest[:])
}

func (a *cliAdapter) DeliverMessage(ctx context.Context, req DeliverMessageRequest) (*DeliverMessageResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, cancellationError(err, false)
	}
	if req.AgentID != "" {
		_, allowed := a.allowedAgents[req.AgentID]
		if a.product == "codex" || !safeLocalID(req.AgentID) || !allowed {
			return nil, &ExecutionError{Code: "unsupported_agent", Stage: "validation", Err: errors.New("agent selection is unsupported or not authorized by the local agent allowlist")}
		}
	}
	prompt, err := formatDeliverMessage(a.botID, req)
	if err != nil {
		return nil, err
	}
	if a.product != "codex" && len(prompt) > maxArgvPromptBytes {
		return nil, &ExecutionError{Code: "prompt_limit", Stage: "validation", Err: errors.New("prompt exceeds native argv limit")}
	}
	sessionID, err := a.loadSession(req.SessionKey)
	if err != nil {
		return nil, err
	}
	if a.product == "openclaw" {
		key := req.SessionKey
		if key == "" {
			key = req.From
		}
		if key == "" {
			key = "default"
		}
		sessionID = "cs-" + a.scopedSessionKey(key)
	}
	if sessionID != "" && !safeSessionID(sessionID) {
		return nil, &ExecutionError{Code: "invalid_session", Stage: "validation", Err: errors.New("session ID is not a safe local identifier; create a new session explicitly")}
	}
	args, stdin := a.deliveryArgs(prompt, sessionID, req.AgentID)
	if a.log != nil {
		a.log.Info("executing native CLI", slog.String("product", a.product))
	}
	emitter := &answerEmitter{emit: req.Emit}
	stream := &nativeStream{product: a.product, emitter: emitter, items: make(map[string]string)}
	var onLine func([]byte) error
	if a.product != "openclaw" {
		onLine = stream.consume
	}
	out, err := runProcess(ctx, a.runCommand, args, stdin, onLine, commandTimeout)
	if err != nil {
		return nil, err
	}
	var result *DeliverMessageResult
	if a.product == "openclaw" {
		result, err = consumeOpenClaw(out)
		if err == nil {
			err = emitter.add(result.Reply)
		}
	} else {
		// OpenCode v2's JSON stream omits step_finish. Confirm the exact
		// streamed assistant turn in native persisted state, not by exit code
		// or the presence of answer text alone.
		if a.product == "opencode" && !stream.completed && stream.sessionID != "" && stream.messageID != "" {
			if err := a.confirmOpenCodeTurn(ctx, stream); err != nil {
				return nil, err
			}
		}
		result, err = stream.result()
	}
	if err != nil {
		return nil, err
	}
	if err := emitter.flush(); err != nil {
		return nil, err
	}
	if req.Emit != nil {
		result.Reply = ""
	}
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	if err := a.saveSession(req.SessionKey, result.SessionID); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *cliAdapter) deliveryArgs(prompt, sessionID, agentID string) ([]string, string) {
	var args []string
	switch a.product {
	case "codex":
		args = []string{"exec"}
		if sessionID != "" {
			args = append(args, "resume")
		}
		args = append(args, "--json", "--skip-git-repo-check")
		if a.model != "" {
			args = append(args, "--model", a.model)
		}
		if sessionID != "" {
			args = append(args, sessionID)
		}
		return append(args, "--", "-"), prompt
	case "opencode":
		args = []string{"run", "--format", "json", "--standalone"}
		if a.model != "" {
			args = append(args, "--model", a.model)
		}
		if sessionID != "" {
			args = append(args, "--session", sessionID)
		}
		if agentID != "" {
			args = append(args, "--agent", agentID)
		}
		return append(args, "--", prompt), ""
	default:
		args = []string{"agent", "--json", "--session-id", sessionID}
		if agentID != "" {
			args = append(args, "--agent", agentID)
		}
		return append(args, "--message", prompt), ""
	}
}

func formatDeliverMessage(localBotID string, req DeliverMessageRequest) (string, error) {
	payload := req.Payload
	if payload.Text == "" && payload.Instruction == "" && len(payload.Input) == 0 {
		payload.Text = req.Message
	}
	prompt := struct {
		Source     string                  `json:"source"`
		BotID      string                  `json:"botId"`
		MessageID  string                  `json:"messageId,omitempty"`
		RequestID  string                  `json:"requestId,omitempty"`
		RunID      string                  `json:"runId,omitempty"`
		Seq        uint64                  `json:"seq,omitempty"`
		Type       string                  `json:"type,omitempty"`
		From       string                  `json:"from,omitempty"`
		SessionKey string                  `json:"sessionKey,omitempty"`
		Payload    protocol.MessagePayload `json:"payload"`
		Metadata   map[string]any          `json:"metadata,omitempty"`
	}{"clawsynapse", localBotID, req.MessageID, req.RequestID, req.RunID, req.Seq, req.Type, req.From, req.SessionKey, payload, req.Metadata}
	data, err := json.Marshal(prompt)
	if err != nil {
		return "", &ExecutionError{Code: "invalid_prompt", Stage: "validation", Err: errors.New("request cannot be encoded as structured JSON")}
	}
	if len(data) > maxPromptBytes {
		return "", &ExecutionError{Code: "prompt_limit", Stage: "validation"}
	}
	return string(data), nil
}

func safeLocalID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for i, ch := range []byte(id) {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && (ch == '_' || ch == '-') {
			continue
		}
		return false
	}
	return true
}

func safeSessionID(id string) bool {
	if len(id) > 256 {
		return false
	}
	for i, ch := range []byte(id) {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			continue
		}
		if i > 0 && (ch == '_' || ch == '-') {
			continue
		}
		return false
	}
	return id != ""
}

func (a *cliAdapter) loadSession(key string) (string, error) {
	if a.sessionStore == nil || key == "" {
		return "", nil
	}
	state, found, err := a.sessionStore.LoadSessionState(a.product, a.scopedSessionKey(key))
	if err != nil {
		return "", &ExecutionError{Code: "session_store", Stage: "session_load", Err: errors.New("cannot load persisted session mapping")}
	}
	if !found {
		return "", nil
	}
	if strings.TrimSpace(state.SessionID) == "" {
		return "", &ExecutionError{Code: "session_store", Stage: "session_load", Err: errors.New("persisted session mapping has no session ID")}
	}
	return state.SessionID, nil
}

func (a *cliAdapter) saveSession(key, id string) error {
	if a.sessionStore == nil || key == "" || id == "" {
		return nil
	}
	previous, found, err := a.sessionStore.LoadSessionState(a.product, a.scopedSessionKey(key))
	if err != nil {
		return &ExecutionError{Code: "session_store", Stage: "session_save", Indeterminate: true, Err: errors.New("cannot reload persisted session mapping")}
	}
	if found && previous.SessionID == id {
		return nil
	}
	now := time.Now().UnixMilli()
	created := now
	if found && previous.CreatedAtMs > 0 {
		created = previous.CreatedAtMs
	}
	if err := a.sessionStore.SaveSessionState(store.SessionState{Adapter: a.product, SessionKey: a.scopedSessionKey(key), SessionID: id, CreatedAtMs: created, UpdatedAtMs: now}); err != nil {
		return &ExecutionError{Code: "session_store", Stage: "session_save", Indeterminate: true, Err: errors.New("cannot persist completed native session")}
	}
	return nil
}
