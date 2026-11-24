//go:build linux

package main

import (
	"os"

	"github.com/brettmostert/agent-command/internal/linuxui"
)

func main() {
	os.Exit(linuxui.Run(linuxui.Options{}))
}
