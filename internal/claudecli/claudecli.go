// Package claudecli runs the `claude` management commands acmd still needs
// (SPEC §4.3): `agents --json` for status cross-checks, and `stop` / `rm`
// for sessions started by `claude --bg` before acmd hosted its own (SPEC
// §4.5). Sessions themselves are started by internal/harness under a PTY
// host, not here. Agent view is a research preview, so parsing is lenient
// and pinned by contract tests on recorded output (testdata/claudecli).
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/procenv"
)

// Runner executes a command and returns its combined output.
type Runner func(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)

// CLI runs a specific claude binary.
type CLI struct {
	Path    string   // absolute path to claude
	Env     []string // base environment (scrubbed of CLAUDE* before use)
	Run     Runner
	Timeout time.Duration
}

// New returns a CLI for the claude binary at path, using the real runner.
func New(path string, env []string) *CLI {
	return &CLI{Path: path, Env: env, Run: execRunner, Timeout: 20 * time.Second}
}

func execRunner(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed claude binary, argv not via a shell
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

func (c *CLI) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	out, err := c.Run(ctx, dir, procenv.Scrub(c.Env), c.Path, args...)
	if err != nil {
		return out, fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(firstLine(out)))
	}
	return out, nil
}

// Agent is one entry of `claude agents --json`.
type Agent struct {
	ID        string  `json:"id"`        // short id (first 8 chars of SessionID); empty for interactive sessions
	SessionID string  `json:"sessionId"` // full Claude session id
	Kind      string  `json:"kind"`      // "background" | "interactive"
	Name      string  `json:"name"`
	Status    *string `json:"status"` // busy|idle|waiting; null when stopped (M0 C3)
	State     string  `json:"state"`  // unreliable (M0 A11/A12): never used for status
	Pid       *int    `json:"pid"`    // null when stopped
	Cwd       string  `json:"cwd"`
	StartedAt int64   `json:"startedAt"`
}

// Stopped reports whether a listed session isn't running (no pid, no status).
func (a Agent) Stopped() bool { return a.Pid == nil || *a.Pid == 0 }

// StatusStopped is StatusString for a session that isn't running; it
// equals core.AgentStopped.
const StatusStopped = "stopped"

// StatusString returns the status, StatusStopped for stopped sessions.
func (a Agent) StatusString() string {
	if a.Stopped() || a.Status == nil {
		return StatusStopped
	}
	return *a.Status
}

// Background reports whether this is a background (attachable) session.
func (a Agent) Background() bool { return a.Kind == "background" }

// UnmarshalJSON tolerates id/pid as number, string or null.
func (a *Agent) UnmarshalJSON(b []byte) error {
	var raw struct {
		ID        json.RawMessage `json:"id"`
		SessionID string          `json:"sessionId"`
		Kind      string          `json:"kind"`
		Name      string          `json:"name"`
		Status    *string         `json:"status"`
		State     string          `json:"state"`
		Pid       json.RawMessage `json:"pid"`
		Cwd       string          `json:"cwd"`
		StartedAt json.Number     `json:"startedAt"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*a = Agent{SessionID: raw.SessionID, Kind: raw.Kind, Name: raw.Name, Status: raw.Status, State: raw.State, Cwd: raw.Cwd}
	a.ID = flexString(raw.ID)
	if p := flexString(raw.Pid); p != "" {
		var n int
		if _, err := fmt.Sscan(p, &n); err == nil && n > 0 {
			a.Pid = &n
		}
	}
	if raw.StartedAt != "" {
		a.StartedAt, _ = raw.StartedAt.Int64()
	}
	return nil
}

func flexString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// ParseAgents decodes `claude agents --json` output.
func ParseAgents(out []byte) ([]Agent, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	var agents []Agent
	if err := json.Unmarshal(out, &agents); err != nil {
		return nil, fmt.Errorf("parse claude agents --json: %w", err)
	}
	return agents, nil
}

// Agents lists sessions (`claude agents --json --all`).
func (c *CLI) Agents(ctx context.Context) ([]Agent, error) {
	out, err := c.run(ctx, "", "agents", "--json", "--all")
	if err != nil {
		return nil, err
	}
	return ParseAgents(out)
}

// Stop runs `claude stop <id>`; the conversation is kept.
func (c *CLI) Stop(ctx context.Context, id string) error {
	_, err := c.run(ctx, "", "stop", id)
	return err
}

// Remove runs `claude rm <id>` (purge).
func (c *CLI) Remove(ctx context.Context, id string) error {
	_, err := c.run(ctx, "", "rm", id)
	return err
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	line, _, _ := strings.Cut(s, "\n")
	return line
}
