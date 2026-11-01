// Package harness is the seam between acmd and the CLI that runs inside a
// hosted session (SPEC §4.5). Everything generic (the PTY host, attach,
// switching, the sidebar, typing a reply) works on any CLI; a Harness
// supplies only what differs: how to start it, how to resume it, and
// where its status comes from.
package harness

// StatusSource says how a harness reports busy / waiting / done.
type StatusSource int

const (
	// StatusHooks: the CLI calls acmd's hooks (Claude Code). Precise.
	StatusHooks StatusSource = iota + 1
	// StatusPTY: derived from output activity, idle time, the terminal bell
	// and process exit. Coarser: "waiting" is a guess.
	StatusPTY
)

func (s StatusSource) String() string {
	switch s {
	case StatusHooks:
		return "hooks"
	case StatusPTY:
		return "pty"
	}
	return "unknown"
}

// Spec says what to start.
type Spec struct {
	Dir       string   // the folder the host runs in (informational: the host sets it)
	Name      string   // display name, optional
	Prompt    string   // first prompt, optional
	SessionID string   // the CLI's own session id to use, when it can be chosen up front
	Args      []string // the user's extra flags
}

// Harness describes one CLI that acmd can host. Argv always starts with the
// absolute path of the binary and is run directly (never through a shell).
type Harness interface {
	// Name is the harness's short identifier ("claude").
	Name() string
	// Command is the argv that starts a new session.
	Command(Spec) []string
	// Resume is the argv that continues an earlier session by its id.
	Resume(sessionID string, args []string) []string
	// StatusSource says where status comes from.
	StatusSource() StatusSource
}
