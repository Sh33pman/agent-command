package cli

import (
	"runtime"

	"github.com/brettmostert/agent-command/internal/paths"
)

// clientOnly is a machine whose daemon runs no sessions of its own: it keeps
// known hosts, connects to them and passes on their sessions (Windows,
// daemon.Config.ClientOnly). Hosting sessions there isn't planned.
var clientOnly = runtime.GOOS == "windows"

// othersCanUse is how doctor tells whether others can use acmd's files: by
// their access lists on Windows, where the mode bits say nothing; nil reads
// the mode bits elsewhere.
var othersCanUse = func() func(string) (string, string, error) {
	if runtime.GOOS == "windows" {
		return paths.OthersCanRead
	}
	return nil
}()
