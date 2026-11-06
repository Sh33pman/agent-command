package harness

// Terminal runs the user's login shell, with no agent.
type Terminal struct {
	Shell string // absolute path
}

// Name implements Harness.
func (Terminal) Name() string { return "terminal" }

// Command implements Harness.
func (t Terminal) Command(Spec) []string { return []string{t.Shell, "-l"} }

// Resume implements Harness: a shell can't be continued, so this is a new one.
func (t Terminal) Resume(string, []string) []string { return []string{t.Shell, "-l"} }

// StatusSource implements Harness.
func (Terminal) StatusSource() StatusSource { return StatusProcess }
