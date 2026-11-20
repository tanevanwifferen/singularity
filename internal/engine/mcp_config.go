package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The ask_questions MCP tool reaches an agent as a stdio MCP server the
// agent's own CLI launches: `singularity mcp-questions --agent <id> --server
// <daemon>` (internal/questionmcp), which relays the questions to this daemon
// and polls for the answers. Each agent gets a config file naming that
// command, handed to the CLI with --mcp-config.

// QuestionBridge is what an agent's MCP server needs to find the daemon.
// The zero value disables the tool: no config is written and agents start
// exactly as they did before it existed.
type QuestionBridge struct {
	// Binary is the singularity executable to launch as the MCP server.
	Binary string
	// Server is the daemon endpoint it connects to (unix:///…, http://…).
	Server string
}

// MCPBackend is implemented by backends whose CLI can load an MCP server
// from a config file. pi cannot (it ships without MCP), so agents on that
// backend have no way to ask questions.
type MCPBackend interface {
	// MCPArgs returns the CLI args that load the config at path. They are
	// appended after everything Args returned.
	MCPArgs(configPath string) []string
}

// BlockAwareBackend is implemented by backends whose subprocess runs its own
// per-turn timer and must know when the agent is waiting on the operator, so
// that timer does not count the wait. BlockedInput returns the line to write
// to the subprocess's stdin when the agent starts (true) or stops (false)
// waiting.
type BlockAwareBackend interface {
	BlockedInput(blocked bool) []byte
}

// mcpToolTimeoutMS is handed to claude as MCP_TOOL_TIMEOUT so it never gives
// up on an ask_questions call by itself: the largest delay a JS timer takes,
// a little under 25 days.
const mcpToolTimeoutMS = "2147483647"

// mcpServerName is the name the tool is registered under; the agent sees it
// as mcp__singularity__ask_questions.
const mcpServerName = "singularity"

// builtinQuestionTool is claude's own ask-the-user tool. In an interactive
// session it opens a dialog in the pane, which nobody is watching: herdr
// reports the agent blocked and the turn fails. It is switched off wherever
// ask_questions is on, so a question can only take the route the operator
// sees.
const builtinQuestionTool = "AskUserQuestion"

// questionToolHint tells the agent which tool reaches the operator.
const questionToolHint = "To ask the human operator anything, call the mcp__" + mcpServerName +
	"__ask_questions tool. It is the only way to reach them: nobody is watching this terminal."

// claudeMCPArgs is the claude CLI args that load the config at path and
// route questions through it. --mcp-config takes any number of values, so it
// has to stay the last flag: anything after it would be read as a config.
func claudeMCPArgs(configPath string) []string {
	return []string{
		"--disallowedTools", builtinQuestionTool,
		"--append-system-prompt", questionToolHint,
		"--mcp-config", configPath,
	}
}

// SetQuestionBridge enables the ask_questions tool for agents started from
// now on. Call before starting any agents.
func (e *Engine) SetQuestionBridge(b QuestionBridge) {
	e.mu.Lock()
	e.bridge = b
	e.mu.Unlock()
}

// SingularityBinary resolves the singularity executable: self when self is
// already it, else a sibling of self, else PATH. The herdr driver and the
// questions MCP server are both subcommands of it.
func SingularityBinary() string {
	if self, err := os.Executable(); err == nil {
		if filepath.Base(self) == "singularity" {
			return self
		}
		sibling := filepath.Join(filepath.Dir(self), "singularity")
		if _, err := os.Stat(sibling); err == nil {
			return sibling
		}
	}
	if p, err := exec.LookPath("singularity"); err == nil {
		return p
	}
	// Nothing found: exec will fail with a legible "not found" naming the
	// binary the operator needs on PATH, which beats guessing.
	return "singularity"
}

// writeMCPConfig writes the agent's MCP config file and returns its path.
func writeMCPConfig(agentID string, bridge QuestionBridge) (string, error) {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			mcpServerName: map[string]any{
				"type":    "stdio",
				"command": bridge.Binary,
				"args":    []string{"mcp-questions", "--agent", agentID, "--server", bridge.Server},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "singl-mcp-*.json")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// mcpArgs returns the extra CLI args that give this agent the ask_questions
// tool, writing its config file on the way. Nil when the tool is disabled or
// the backend cannot load one. Caller holds mu.
func (a *Agent) mcpArgs() []string {
	mcp, ok := a.backend.(MCPBackend)
	if !ok || a.bridge.Binary == "" || a.bridge.Server == "" {
		return nil
	}
	path, err := writeMCPConfig(a.ID, a.bridge)
	if err != nil {
		a.appendOutputLocked("error", fmt.Sprintf("ask_questions tool unavailable: %v", err))
		return nil
	}
	a.mcpConfigPath = path
	return mcp.MCPArgs(path)
}
