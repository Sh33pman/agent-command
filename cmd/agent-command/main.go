// Command agent-command (alias acmd) tracks Claude Code sessions and shows
// them in a sidebar in Kitty and Ghostty.
package main

import (
	"errors"
	"os"

	"github.com/brettmostert/agent-command/internal/cli"
)

func main() {
	if err := cli.Execute(os.Args[0], os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, cli.ErrSilent) {
			cli.WriteError(os.Stderr, err)
		}
		os.Exit(cli.ExitCode(err)) // acmd --help lists the codes
	}
}
