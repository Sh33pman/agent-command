package core

import (
	"path/filepath"
	"strconv"
	"strings"
)

// What a session is and what can be done with it, worked out here once for
// every client (design 6.6). These lists grow: a client shows an agent it
// doesn't know generically, and ignores a capability it doesn't know (8.6).

// Agents a session can run.
const (
	AgentClaude      = "claude"
	AgentAntigravity = "antigravity"
	AgentOther       = "other" // an agent acmd can't name more closely, typed in a terminal
	AgentNone        = "none"  // a terminal with no agent in it
)

// Capabilities: what a client may do with a session.
const (
	CanResume        = "resume"          // start it again where it stopped
	CanAttach        = "attach"          // show its terminal
	CanArchive       = "archive"         // end it, and keep it in the archive
	CanUnarchive     = "unarchive"       // bring it back from the archive
	CanPurge         = "purge"           // delete it from the archive
	CanContinueInJob = "continue_in_job" // a scheduled job can send it a prompt
	CanOpenInEditor  = "open_in_editor"  // its folder is on this machine
	CanSetRemote     = "set_remote"      // turn Remote on or off for it
)

// Agent is what runs in s now: its harness's agent, or in a terminal, the
// agent typed in it.
func (s *Session) Agent() string {
	switch s.Harness {
	case HarnessAntigravity:
		return AgentAntigravity
	case HarnessTerminal:
		if s.Status == StatusUnknown {
			switch s.StatusDetail {
			case "claude":
				return AgentClaude
			case "agy":
				return AgentAntigravity
			case "":
			default:
				return AgentOther
			}
		}
		if s.ClaudeSessionID != "" {
			return AgentClaude // its hooks gave a conversation: Claude typed in it
		}
		return AgentNone
	default: // HarnessClaude, or older sessions with none
		return AgentClaude
	}
}

// DisplayName is what clients call s: its name, or its folder's.
func (s *Session) DisplayName() string {
	if s.Name != "" || s.Cwd == "" {
		return s.Name
	}
	return filepath.Base(s.Cwd)
}

// Capabilities are what may be done with s now.
func (s *Session) Capabilities() []string {
	local := s.Host == ""
	archived := s.Status == StatusArchived
	stopped := s.Status == StatusPaused || s.Status == StatusFinished
	agent := s.Agent()
	var out []string
	add := func(ok bool, c string) {
		if ok {
			out = append(out, c)
		}
	}
	// A host resumes its own sessions; this machine only attaches to them.
	add(local && stopped, CanResume)
	add(!archived && !s.HostGone, CanAttach)
	add(!archived && !s.HostGone, CanArchive)
	// A terminal has no conversation to come back to.
	add(local && archived && s.Harness != HarnessTerminal, CanUnarchive)
	add(local && archived, CanPurge)
	// A Claude session, or a terminal Claude was typed in once its hooks gave
	// the conversation; Antigravity's once its first hook has given its id.
	conversation := s.ClaudeSessionID != ""
	continues := (agent == AgentClaude && (s.Harness != HarnessTerminal || conversation)) ||
		(agent == AgentAntigravity && conversation)
	add(local && !archived && continues, CanContinueInJob)
	add(local && s.Cwd != "", CanOpenInEditor)
	add(local && !archived, CanSetRemote)
	if out == nil {
		out = []string{}
	}
	return out
}

// RenameNames are the names for count sessions renamed together: base for
// one, "base - 1", "base - 2" and so on for several. A blank base is none.
func RenameNames(base string, count int) []string {
	base = strings.TrimSpace(base)
	if base == "" || count < 1 {
		return nil
	}
	if count == 1 {
		return []string{base}
	}
	out := make([]string, count)
	for i := range out {
		out[i] = base + " - " + strconv.Itoa(i+1)
	}
	return out
}
