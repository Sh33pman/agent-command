package daemon

import (
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// knownHarnesses are the harnesses acmd can start, in the order clients list
// them.
var knownHarnesses = []ipc.Harness{
	{Name: core.HarnessClaude, Title: "Claude", Icon: "claude", Agent: true},
	{Name: core.HarnessAntigravity, Title: "Antigravity", Icon: "antigravity", Agent: true},
	{Name: core.HarnessTerminal, Title: "Terminal", Icon: "terminal"},
}

// harnesses are what's installed here (Config.Harnesses; all of them when
// it's nil), and the default for a plain new session.
func (d *Daemon) harnesses() ipc.Harnesses {
	installed := []string{core.HarnessClaude, core.HarnessAntigravity, core.HarnessTerminal}
	if d.cfg.Harnesses != nil {
		installed = d.cfg.Harnesses()
	}
	out := ipc.Harnesses{Harnesses: []ipc.Harness{}}
	for _, h := range knownHarnesses {
		if slices.Contains(installed, h.Name) {
			out.Harnesses = append(out.Harnesses, h)
		}
	}
	if def := d.config().DefaultHarness; slices.ContainsFunc(out.Harnesses, func(h ipc.Harness) bool { return h.Name == def && h.Agent }) {
		out.Default = def
	}
	return out
}
