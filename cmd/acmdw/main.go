// Command acmdw is the Windows logon task's launcher. It's built with
// -H=windowsgui, so Task Scheduler opens no console window for it, and starts
// `acmd.exe daemon` with no window either (internal/service.Launch).
package main

import (
	"os"

	"github.com/brettmostert/agent-command/internal/service"
)

func main() {
	if err := service.Launch(os.Args[1:]); err != nil {
		service.LaunchFailed(os.Args[1:], err)
		os.Exit(1)
	}
}
