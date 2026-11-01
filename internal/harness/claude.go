package harness

import "strings"

// remoteControlFlag enables Remote Control on every session acmd launches,
// so they can be driven from a phone.
const remoteControlFlag = "--remote-control"

// Claude hosts interactive Claude Code. Unlike `--bg`, an interactive
// session runs under acmd's own PTY, so it is not subject to the
// background auto-mode classifier (which blocks `gh pr merge`).
type Claude struct {
	Path string // absolute path to claude
}

// Name implements Harness.
func (Claude) Name() string { return "claude" }

// Command implements Harness: `claude --remote-control [--name N] [args] [-- prompt]`.
func (c Claude) Command(_, name, prompt string, args []string) []string {
	argv := []string{c.Path}
	if !hasRemoteControl(args) {
		// Before any other flag, so its optional [name] value can't
		// swallow an argument.
		argv = append(argv, remoteControlFlag)
	}
	if name != "" {
		argv = append(argv, "--name", name)
	}
	argv = append(argv, args...)
	if prompt != "" {
		argv = append(argv, "--", prompt)
	}
	return argv
}

// Resume implements Harness: `claude --remote-control [args] --resume <id>`.
func (c Claude) Resume(sessionID string, args []string) []string {
	argv := []string{c.Path}
	if !hasRemoteControl(args) {
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
