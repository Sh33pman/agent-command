package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/popup"
)

func newPopupCmd() *cobra.Command {
	var demo bool
	cmd := &cobra.Command{
		Use:    "popup",
		Short:  "Run the reply popup (started by the daemon when the popup is on)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !popup.Supported() {
				return popup.ErrUnsupported
			}
			if demo {
				return runPopupDemo(cmd)
			}
			return runPopupAgent(cmd)
		},
	}
	cmd.Flags().BoolVar(&demo, "demo", false, "show sample items (development)")
	return cmd
}

// runPopupDemo shows three sample items, logs the panel's frame and every
// event, and quits after --demo-seconds (ACMD_POPUP_DEMO_SECONDS, default 0
// = stay).
func runPopupDemo(cmd *cobra.Command) error {
	out := cmd.ErrOrStderr()
	items := []popup.State{
		{Title: "auth work", Meta: "✓ done · ~/w/api", Body: "I refactored the auth middleware.\n\n" + longText(), CanReply: true, Placeholder: "Reply to auth work"},
		{Title: "flaky e2e", Meta: "! needs you · ~/w/web", Body: "Claude wants to run: npm test -- --runInBand", Options: []string{"Yes", "Yes, and don't ask again", "No"}},
		{Title: "docs", Meta: "✓ done · ~/w/docs", Body: "Updated the README.", CanReply: true},
	}
	i := 0
	show := func() {
		s := items[i]
		s.Visible = true
		s.Counter = fmt.Sprintf("%d of %d", i+1, len(items))
		popup.Update(s)
	}
	popup.SetBindings(defaultPopupBindings())
	if err := popup.RegisterHotKey(1, "cmd+shift+o"); err != nil {
		fmt.Fprintln(out, "hotkey:", err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		show()
		time.Sleep(time.Second)
		fmt.Fprintln(out, "frame:", popup.Frame())
		if secs := os.Getenv("ACMD_POPUP_DEMO_SECONDS"); secs != "" {
			var n int
			_, _ = fmt.Sscan(secs, &n)
			time.Sleep(time.Duration(n) * time.Second)
			fmt.Fprintln(out, "frame:", popup.Frame())
			os.Exit(0)
		}
	}()
	return popup.Run(func(e popup.Event) {
		fmt.Fprintf(out, "event: %+v\n", e)
		switch e.Kind {
		case "next":
			i = (i + 1) % len(items)
		case "prev":
			i = (i + len(items) - 1) % len(items)
		case "close":
			popup.Update(popup.State{})
			return
		case "hotkey":
		default:
			return
		}
		go show()
	})
}

func longText() string {
	s := ""
	for n := 1; n <= 40; n++ {
		s += fmt.Sprintf("Line %d of a long response, to scroll with the keyboard or the mouse.\n", n)
	}
	return s
}

// defaultPopupBindings are the panel's default keys (configurable, [popup.keys]).
func defaultPopupBindings() []popup.Binding {
	return []popup.Binding{
		{Action: "prev", Key: "left", Mods: []string{"cmd", "ctrl"}},
		{Action: "next", Key: "right", Mods: []string{"cmd", "ctrl"}},
		{Action: "play", Key: "p", Mods: []string{"cmd", "ctrl"}},
		{Action: "skip", Key: "k", Mods: []string{"cmd", "ctrl"}},
		{Action: "close", Key: "escape"},
	}
}

// runPopupAgent is the real popup (later step).
func runPopupAgent(*cobra.Command) error { return fmt.Errorf("the popup agent isn't built yet") }
