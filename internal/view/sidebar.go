package view

import (
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/repo"
)

// NewFolder is where a shortcut starts a session (SPEC §6.1 step 2): the
// shown session's folder, else the most recently active session's, else
// home. A worktree is never returned: it's its repository's folder.
func NewFolder(snap ipc.Snapshot, v *core.View, home string) string {
	var recent *core.Session
	for _, s := range snap.Sessions {
		if s.ID == v.Selected && s.Cwd != "" {
			return repo.Root(s.Cwd)
		}
		if s.Cwd != "" && (recent == nil || s.LastEventAt.After(recent.LastEventAt)) {
			recent = s
		}
	}
	if recent != nil {
		return repo.Root(recent.Cwd)
	}
	return home
}
