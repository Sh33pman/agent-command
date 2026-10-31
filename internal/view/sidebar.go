package view

import (
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// NewFolder is where a shortcut starts a session (SPEC §6.1 step 2): the
// shown session's folder, else the most recently active session's, else
// home.
func NewFolder(snap ipc.Snapshot, v *core.View, home string) string {
	var recent *core.Session
	for _, s := range snap.Sessions {
		if s.ID == v.Selected && s.Cwd != "" {
			return s.Cwd
		}
		if s.Cwd != "" && (recent == nil || s.LastEventAt.After(recent.LastEventAt)) {
			recent = s
		}
	}
	if recent != nil {
		return recent.Cwd
	}
	return home
}
