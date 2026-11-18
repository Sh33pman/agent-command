package harness

// Antigravity hosts Google's Antigravity CLI (agy), interactive. agy picks
// its own conversation id, which acmd learns from its first hook.
type Antigravity struct {
	Path string // absolute path to agy
	// RemoteControl starts sessions with --remote-control (the agent_remote_control
	// setting), so they can be used from Antigravity's Remote Control in a
	// browser. --remote-control in the user's args turns it on regardless.
	RemoteControl bool
}

// Name implements Harness.
func (Antigravity) Name() string { return "antigravity" }

// Command implements Harness: `agy [--remote-control] [-i=prompt] [args]`. agy
// has no flag for a name or a conversation id, so Spec's are unused.
func (a Antigravity) Command(sp Spec) []string {
	argv := a.start(sp.Args)
	if sp.Prompt != "" {
		argv = append(argv, a.PromptArgs(sp.Prompt)...)
	}
	return append(argv, sp.Args...)
}

// Resume implements Harness: `agy [--remote-control] --conversation <id> [args]`.
func (a Antigravity) Resume(sessionID string, args []string) []string {
	return append(append(a.start(args), "--conversation", sessionID), args...)
}

// start is agy and the flags that come before everything else.
func (a Antigravity) start(args []string) []string {
	argv := []string{a.Path}
	if a.RemoteControl && !hasRemoteControl(args) {
		argv = append(argv, remoteControlFlag)
	}
	return argv
}

// PromptArgs implements Prompter. The value is joined to the flag: agy reads
// `-i -x` as two flags, so a prompt that starts with a dash would fail.
func (Antigravity) PromptArgs(prompt string) []string { return []string{"-i=" + prompt} }

// StatusSource implements Harness: agy calls acmd's hooks.
func (Antigravity) StatusSource() StatusSource { return StatusHooks }
