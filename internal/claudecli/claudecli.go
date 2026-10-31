// Package claudecli is the only place acmd runs the `claude` CLI (SPEC
// §4.3): background sessions (`--bg`, `--bg --resume`), `agents --json`,
// `stop`, `rm` and the argv for `attach`. Agent view is a research preview,
// so parsing is lenient and pinned by contract tests on recorded output
// (testdata/claudecli).
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/procenv"
)

// ErrNotTrusted means Claude refused to start in an untrusted folder
// (M0 A1); the user has to accept the trust prompt interactively once.
var ErrNotTrusted = errors.New("workspace not trusted by Claude")

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
		if bytes.Contains(out, []byte("Workspace not trusted")) {
			return out, ErrNotTrusted
		}
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

// StartOptions configure a new background session.
type StartOptions struct {
	Dir    string   // folder to start in
	Name   string   // --name, optional
	Args   []string // extra claude args (config claude_args)
	Prompt string   // optional first prompt
}

// StartBackground runs `claude --bg` and returns the short id it prints.
// ErrNotTrusted if Claude hasn't trusted Dir yet.
func (c *CLI) StartBackground(ctx context.Context, o StartOptions) (string, error) {
	args := []string{"--bg"}
	if o.Name != "" {
		args = append(args, "--name", o.Name)
	}
	args = append(args, o.Args...)
	if o.Prompt != "" {
		args = append(args, "--", o.Prompt)
	}
	out, err := c.run(ctx, o.Dir, args...)
	if err != nil {
		return "", err
	}
	return ParseShortID(out)
}

// Resume runs `claude --bg --resume <sessionID>` (M0 C3: keeps the id).
func (c *CLI) Resume(ctx context.Context, dir, sessionID string) (string, error) {
	out, err := c.run(ctx, dir, "--bg", "--resume", sessionID)
	if err != nil {
		return "", err
	}
	return ParseShortID(out)
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

// AttachArgv is the command line for showing a session in a pane. It must
// run in the foreground of the pane (M0 A9).
func (c *CLI) AttachArgv(id string) []string { return []string{c.Path, "attach", id} }

var (
	attachRe      = regexp.MustCompile(`claude attach ([0-9a-f]{8})\b`)
	backgroundRe  = regexp.MustCompile(`backgrounded · ([0-9a-f]{8}) ·`)
	errNoIDInText = errors.New("no session id in claude output")
)

// ParseShortID extracts the short session id from `claude --bg` output.
func ParseShortID(out []byte) (string, error) {
	for _, re := range []*regexp.Regexp{backgroundRe, attachRe} {
		if m := re.FindSubmatch(out); m != nil {
			return string(m[1]), nil
		}
	}
	return "", fmt.Errorf("%w: %q", errNoIDInText, firstLine(out))
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	line, _, _ := strings.Cut(s, "\n")
	return line
}
