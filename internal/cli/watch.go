package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// `acmd watch`: notices as they come, one line each, with the terminal's bell.
// It's a terminal client while it runs, so notice routing can pick it.

// bell rings the terminal's bell.
const bell = "\a"

func newWatchCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Follow notices as they come, one line each, and ring the bell",
		Long: "Prints each notice meant for this machine's terminals as it comes, one line each, and rings the\n" +
			"terminal's bell. While it runs it counts as a terminal client, so notify.route can send notices\n" +
			"here. --all prints every notice, wherever it went (the bell only rings for this terminal's).\n" +
			"With --json, each notice is one event per line, as acmd events prints it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if targetHost != "" {
				return fmt.Errorf("acmd watch shows this machine's notices, a host's included: it doesn't take --host")
			}
			c, err := connectTerminal(cmd.Context())
			if err != nil {
				return err
			}
			defer c.close()
			go func() { // stopped: the stream ends
				<-cmd.Context().Done()
				c.close()
			}()
			// Only what comes from now: a resumed snapshot's old notices aren't news.
			if err := c.conn.Subscribe(ipc.SubscribeRequest{Types: []string{ipc.EntityNotice}}); err != nil {
				return err
			}
			w := watcher{out: jsonWriter(cmd), me: c.terminal, all: all, json: jsonOut, now: time.Now}
			for {
				ev, err := c.conn.NextEvent()
				if err != nil {
					if cmd.Context().Err() != nil {
						return nil
					}
					return fmt.Errorf("the stream ended: %w", err)
				}
				if err := w.event(ev); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "print every notice, not only this machine's terminals'")
	return cmd
}

// watcher prints the notices of an event stream.
type watcher struct {
	out  io.Writer
	me   string // this terminal client's id
	all  bool
	json bool
	now  func() time.Time
}

func (w watcher) event(ev ipc.Event) error {
	if ev.Type != ipc.EventAdded || ev.Entity != ipc.EntityNotice {
		return nil // the snapshot's notices and the ones taken back aren't news
	}
	var n ipc.Notice
	if err := json.Unmarshal(ev.Data, &n); err != nil || n.Kind == "withdraw" {
		return err
	}
	mine := slices.Contains(n.To, w.me)
	if !mine && !w.all {
		return nil
	}
	if w.json {
		return json.NewEncoder(w.out).Encode(ev)
	}
	ring := ""
	if mine {
		ring = bell
	}
	_, err := fmt.Fprintf(w.out, "%s%s %s\n", ring, w.now().Format("15:04"), noticeLine(n))
	return err
}

// noticeLine is a notice on one line: what happened, where.
func noticeLine(n ipc.Notice) string {
	s := cmpOr(n.Title, n.Kind)
	if n.Body != "" {
		s += ": " + oneLineText(n.Body, 100)
	}
	if n.Host != "" {
		s += " (on " + n.Host + ")"
	}
	return s
}
