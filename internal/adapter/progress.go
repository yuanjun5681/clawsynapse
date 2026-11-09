package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// ExecutionContext is local invocation metadata, never a new wire envelope field.
// Command is an argv prefix: append one progress sentence after --message.
// It is also in the prompt because a gateway need not inherit the CLI child's env.
type ExecutionContext struct {
	CLIPath    string   `json:"cliPath"`
	APIAddr    string   `json:"apiAddr"`
	BotID      string   `json:"botId"`
	TargetBot  string   `json:"targetBot"`
	RequestID  string   `json:"requestId"`
	RunID      string   `json:"runId"`
	SessionKey string   `json:"sessionKey"`
	AgentID    string   `json:"agentId,omitempty"`
	Command    []string `json:"progressCommand"`
}

func NewExecutionContext(cliPath, apiAddr, botID, targetBot, requestID, runID, sessionKey, agentID string) *ExecutionContext {
	if cliPath == "" || apiAddr == "" || botID == "" || targetBot == "" || requestID == "" || runID == "" || sessionKey == "" {
		return nil
	}
	args := []string{cliPath, "--api-addr", apiAddr, "--bot", botID, "--json", "publish", "--type", "chat.progress", "--target", targetBot, "--request-id", requestID, "--run-id", runID, "--session-key", sessionKey}
	if agentID != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--message")
	return &ExecutionContext{CLIPath: cliPath, APIAddr: apiAddr, BotID: botID, TargetBot: targetBot, RequestID: requestID, RunID: runID, SessionKey: sessionKey, AgentID: agentID, Command: args}
}

func (c *ExecutionContext) environment() []string {
	return []string{
		"CLAWSYNAPSE_CLI=" + c.CLIPath,
		"CLAWSYNAPSE_API_ADDR=" + c.APIAddr,
		"CLAWSYNAPSE_BOT_ID=" + c.BotID,
		"CLAWSYNAPSE_TARGET_BOT=" + c.TargetBot,
		"CLAWSYNAPSE_REQUEST_ID=" + c.RequestID,
		"CLAWSYNAPSE_RUN_ID=" + c.RunID,
		"CLAWSYNAPSE_SESSION_KEY=" + c.SessionKey,
		"CLAWSYNAPSE_AGENT_ID=" + c.AgentID,
	}
}

// ResolveCLIPath prefers the daemon's installed sibling, then the actual PATH.
// Missing CLI disables optional progress rather than breaking the main task.
func ResolveCLIPath() string {
	name := "clawsynapse"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if executable, err := os.Executable(); err == nil {
		if path, err := exec.LookPath(filepath.Join(filepath.Dir(executable), name)); err == nil {
			return path
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		absolute, err := filepath.Abs(path)
		if err == nil {
			return absolute
		}
	}
	return ""
}
