package harness

import "strings"

// remoteControlFlag turns on Remote Control, so a session can be used from
// claude.ai and the Claude app.
const remoteControlFlag = "--remote-control"

// Claude hosts interactive Claude Code. Unlike `--bg`, an interactive
// session runs under acmd's own PTY, so it is not subject to the
// background auto-mode classifier (which blocks `gh pr merge`).
type Claude struct {
	Path string // absolute path to claude
	// RemoteControl starts sessions with --remote-control (the remote_control
	// setting). --remote-control in the user's args turns it on regardless.
	RemoteControl bool
}

// Name implements Harness.
func (Claude) Name() string { return "claude" }

// Command implements Harness: `claude [--remote-control] [--name N] [--session-id U] [args] [-- prompt]`.
func (c Claude) Command(sp Spec) []string {
	name, prompt, args := sp.Name, sp.Prompt, sp.Args
	argv := []string{c.Path}
	if c.RemoteControl && !hasRemoteControl(args) {
		// Before any other flag, so its optional [name] value can't
		// swallow an argument.
		argv = append(argv, remoteControlFlag)
	}
	if name != "" {
		argv = append(argv, "--name", name)
	}
	if sp.SessionID != "" {
		// So acmd knows the id before the first hook: hooks link by it.
		argv = append(argv, "--session-id", sp.SessionID)
	}
	argv = append(argv, args...)
	if prompt != "" {
		argv = append(argv, "--", prompt)
	}
	return argv
}

// Resume implements Harness: `claude [--remote-control] [args] --resume <id>`.
func (c Claude) Resume(sessionID string, args []string) []string {
	argv := []string{c.Path}
	if c.RemoteControl && !hasRemoteControl(args) {
		argv = append(argv, remoteControlFlag)
	}
	argv = append(argv, args...)
	return append(argv, "--resume", sessionID)
}

// StatusSource implements Harness: Claude Code calls acmd's hooks.
func (Claude) StatusSource() StatusSource { return StatusHooks }

func hasRemoteControl(args []string) bool {
	for _, a := range args {
		if a == remoteControlFlag || strings.HasPrefix(a, remoteControlFlag+"=") {
			return true
		}
	}
	return false
}
