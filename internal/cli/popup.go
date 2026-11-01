package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/popup"
	"github.com/brettmostert/agent-command/internal/procenv"
)

func newPopupCmd() *cobra.Command {
	var demo, selftest bool
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
				return runPopupDemo(cmd, selftest)
			}
			return runPopupAgent(cmd)
		},
	}
	cmd.Flags().BoolVar(&demo, "demo", false, "show sample items (development)")
	cmd.Flags().BoolVar(&selftest, "selftest", false, "with --demo: check copy and paste, print the result and quit")
	return cmd
}

// runPopupDemo shows three sample items, logs the panel's frame and every
// event, and quits after --demo-seconds (ACMD_POPUP_DEMO_SECONDS, default 0
// = stay).
func runPopupDemo(cmd *cobra.Command, selftest bool) error {
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
	popup.SetBindings(popupBindings(config.Default().Popup.Keys))
	if err := popup.RegisterHotKey(1, "cmd+shift+o"); err != nil {
		fmt.Fprintln(out, "hotkey:", err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		show()
		time.Sleep(time.Second)
		fmt.Fprintln(out, "frame:", popup.Frame())
		if selftest {
			fmt.Fprintln(out, "selftest:", popup.SelfTest(func() { time.Sleep(200 * time.Millisecond) }))
			os.Exit(0)
		}
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

// reexecPopup replaces this popup with the installed acmd (swappable in
// tests).
var reexecPopup = func() error { return reexec("popup") }

// popupBindings turns [popup.keys] into the panel's bindings.
func popupBindings(k config.PopupKeys) []popup.Binding {
	var out []popup.Binding
	for _, b := range []struct{ action, key string }{
		{"prev", k.Prev}, {"next", k.Next}, {"play", k.Play}, {"skip", k.Skip}, {"close", k.Close},
	} {
		if b.key == "" {
			continue
		}
		parts := strings.Split(strings.ToLower(b.key), "+")
		var mods []string
		for _, m := range parts[:len(parts)-1] {
			switch m {
			case "cmd", "super", "command":
				m = "cmd"
			case "control":
				m = "ctrl"
			case "opt", "option":
				m = "alt"
			}
			mods = append(mods, m)
		}
		key := parts[len(parts)-1]
		if key == "esc" {
			key = "escape"
		}
		out = append(out, popup.Binding{Action: b.action, Key: key, Mods: mods})
	}
	return out
}

// runPopupAgent is the reply popup (SPEC §8.8): it runs the panel, follows
// the daemon's inbox, opens for new items while the user isn't in acmd,
// and answers sessions. It must run on the main goroutine (AppKit).
func runPopupAgent(cmd *cobra.Command) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	unlock, err := lockFile(filepath.Join(p.RuntimeDir, "popup.lock"))
	if err != nil {
		return nil // another popup runs: fine
	}
	defer unlock()
	cfg, err := config.Load(p.Config())
	if err != nil {
		return err
	}
	a := &popupAgent{p: p, cfg: cfg, vc: &viewClient{paths: p}, b: &popup.Inbox{Home: homeDir()},
		events: make(chan popup.Event, 32), log: cmd.ErrOrStderr()}
	popup.SetBindings(popupBindings(cfg.Popup.Keys))
	if cfg.Popup.Hotkey != "" {
		if err := popup.RegisterHotKey(hotkeyPopup, cfg.Popup.Hotkey); err != nil {
			fmt.Fprintln(a.log, "acmd popup:", err)
		}
	}
	if cfg.Popup.ReadHotkey != "" {
		if err := popup.RegisterHotKey(hotkeyRead, cfg.Popup.ReadHotkey); err != nil {
			fmt.Fprintln(a.log, "acmd popup:", err)
		}
	}
	go a.follow(context.Background())
	go a.handle(context.Background())
	return popup.Run(func(e popup.Event) {
		select {
		case a.events <- e:
		default: // never block AppKit's thread
		}
	})
}

type popupAgent struct {
	p      paths.Paths
	cfg    config.Config
	vc     *viewClient
	log    io.Writer
	events chan popup.Event

	mu     sync.Mutex
	b      *popup.Inbox
	stop   context.CancelFunc // stops playback
	played string             // the item auto-played last (session|time)
	latest popup.Latest       // what to read aloud per session (hotkeyRead)
}

// Hotkey ids: the popup's own, and reading the current session aloud.
const (
	hotkeyPopup = 1
	hotkeyRead  = 2
)

func (a *popupAgent) render() {
	a.mu.Lock()
	s := a.b.State()
	a.mu.Unlock()
	popup.Update(s)
}

// follow keeps the inbox in step with the daemon and opens the panel for
// new items while the user isn't in acmd.
func (a *popupAgent) follow(ctx context.Context) {
	var rev uint64
	for ctx.Err() == nil {
		w, err := a.vc.Watch(ctx, ipc.WatchRequest{Rev: rev, WaitMS: 25000, Passive: true})
		if errors.Is(err, errNewerDaemon) {
			// acmd was upgraded: become the installed version (its popup
			// lock is released on exec and taken again).
			if err := reexecPopup(); err != nil {
				fmt.Fprintln(a.log, "acmd popup: upgrade:", err)
			}
		}
		if err != nil {
			time.Sleep(2 * time.Second) // the daemon restarts on demand
			continue
		}
		if w.Rev == rev && rev != 0 {
			continue
		}
		rev = w.Rev
		snap, err := a.vc.Snapshot(ctx)
		if err != nil {
			continue
		}
		sessions := map[string]*core.Session{}
		for _, s := range snap.Sessions {
			sessions[s.ID] = s
		}
		a.mu.Lock()
		fresh := a.b.Set(snap.Inbox, sessions)
		a.latest.Observe(snap.Inbox)
		a.mu.Unlock()
		if len(fresh) > 0 {
			inAcmd, looking := userInAcmd(ctx, snap)
			var show string
			for _, id := range fresh {
				if id == looking {
					// The user is looking at that session: nothing to queue.
					_ = a.vc.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutInboxSkip, ID: id}, nil)
					a.mu.Lock()
					a.b.Remove(id)
					a.mu.Unlock()
					continue
				}
				show = id
			}
			if show != "" && !inAcmd {
				a.mu.Lock()
				a.b.Show(show)
				a.mu.Unlock()
			}
		}
		a.render()
		a.autoplay()
	}
}

// autoplay reads the shown item aloud once, when auto-play is on.
func (a *popupAgent) autoplay() {
	if !a.cfg.Popup.AutoPlay {
		return
	}
	a.mu.Lock()
	it := a.b.Item()
	open := a.b.Open
	a.mu.Unlock()
	if it == nil || !open {
		return
	}
	key := it.SessionID + "|" + it.At.String()
	if key == a.played {
		return
	}
	a.played = key
	a.play(it.Text)
}

// play reads text aloud, stopping what's playing first.
func (a *popupAgent) play(text string) {
	a.stopPlaying()
	e, err := speechEngine()
	if err != nil {
		a.note(err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.stop, a.b.Playing = cancel, true
	a.mu.Unlock()
	a.render()
	go func() {
		err := e.Say(ctx, text)
		a.mu.Lock()
		a.b.Playing, a.stop = false, nil
		a.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			a.note("Speech: " + err.Error())
			return
		}
		a.render()
	}()
}

// readCurrent is the read-aloud hotkey: it stops what's playing; else with
// the panel open it plays the shown item (the same as the panel's play key,
// which the hotkey shadows); else it reads the latest response or question
// of the session the user is in (a focused view's, or the one in their own
// focused Kitty/Ghostty pane), and does nothing when they're in neither.
func (a *popupAgent) readCurrent(ctx context.Context, shown *core.InboxItem) {
	a.mu.Lock()
	playing, open := a.b.Playing, a.b.Open
	a.mu.Unlock()
	if playing {
		a.stopPlaying()
		a.render()
		return
	}
	if open && shown != nil {
		a.play(shown.Text)
		return
	}
	snap, err := a.vc.Snapshot(ctx)
	if err != nil {
		return
	}
	_, id := userInAcmd(ctx, snap)
	if id == "" {
		return
	}
	var sess *core.Session
	for _, s := range snap.Sessions {
		if s.ID == id {
			sess = s
		}
	}
	a.mu.Lock()
	text, ok := a.latest.Text(id, sess)
	a.mu.Unlock()
	if !ok {
		return
	}
	if e, err := speechEngine(); err == nil {
		if st := e.Status(); st != "" {
			speechNotice("Speech isn't ready (" + st + "). Run: acmd speech install")
			return
		}
	}
	a.play(text)
}

// speechNotice tells the user something when no panel is open.
var speechNotice = func(msg string) {
	// G204: msg is built from acmd's own fixed text and speech's status, and is quoted.
	_ = exec.Command("osascript", "-e", "display notification "+strconv.Quote(msg)+` with title "acmd"`).Run() //nolint:gosec // G204: msg is acmd's own text, quoted
}

func (a *popupAgent) stopPlaying() {
	a.mu.Lock()
	stop := a.stop
	a.stop, a.b.Playing = nil, false
	a.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (a *popupAgent) note(text string) {
	a.mu.Lock()
	a.b.Note = text
	a.mu.Unlock()
	a.render()
}

// handle acts on what the user does in the panel.
func (a *popupAgent) handle(ctx context.Context) {
	for e := range a.events {
		a.mu.Lock()
		it := a.b.Item()
		var cur core.InboxItem
		if it != nil {
			cur = *it
		}
		a.mu.Unlock()
		switch e.Kind {
		case "prev", "next":
			a.stopPlaying()
			a.mu.Lock()
			if e.Kind == "prev" {
				a.b.Move(-1)
			} else {
				a.b.Move(1)
			}
			a.mu.Unlock()
			a.render()
			a.autoplay()
		case "close":
			a.stopPlaying()
			a.mu.Lock()
			a.b.Open = false
			a.mu.Unlock()
			a.render()
		case "hotkey":
			if e.N == hotkeyRead {
				a.readCurrent(ctx, it)
				continue
			}
			a.mu.Lock()
			if a.b.Open {
				a.b.Open = false
			} else {
				a.b.Show("")
			}
			a.mu.Unlock()
			a.render()
			a.autoplay()
		case "play":
			a.mu.Lock()
			playing := a.b.Playing
			a.mu.Unlock()
			if playing {
				a.stopPlaying()
				a.render()
			} else if it != nil {
				a.play(cur.Text)
			}
		case "skip":
			if it == nil {
				continue
			}
			a.stopPlaying()
			_ = a.vc.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutInboxSkip, ID: cur.SessionID}, nil)
			a.mu.Lock()
			a.b.Remove(cur.SessionID)
			a.mu.Unlock()
			a.render()
			a.autoplay()
		case "send", "option":
			if it == nil {
				continue
			}
			in := replyText(e.Text)
			if e.Kind == "option" {
				var err error
				if in, err = optionIndex(&cur, e.N); err != nil {
					a.note(err.Error())
					continue
				}
			}
			a.answer(ctx, cur, in)
		}
	}
}

// answer delivers in to the item's session in the background; the item
// goes when it's taken.
func (a *popupAgent) answer(ctx context.Context, it core.InboxItem, in replyInput) {
	a.stopPlaying()
	a.note("Sending…")
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		snap, err := a.vc.Snapshot(ctx)
		if err != nil {
			a.note(err.Error())
			return
		}
		var s *core.Session
		for _, x := range snap.Sessions {
			if x.ID == it.SessionID {
				s = x
			}
		}
		if s == nil {
			a.note("That session is gone.")
			return
		}
		call := func(op string, in, out any) error { return a.vc.call(ctx, op, in, out) }
		if err := deliverReply(ctx, call, s, in); err != nil {
			a.note(err.Error())
			return
		}
		_ = a.vc.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutInboxSkip, ID: it.SessionID}, nil)
		a.mu.Lock()
		a.b.Sent()
		a.b.Remove(it.SessionID)
		a.mu.Unlock()
		a.render()
		a.autoplay()
	}()
}

// userInAcmd reports whether the user is in one of acmd's views (the
// frontmost window is a view's pane, sidebar, header, bar or terminal), and
// the session they're looking at: the one that view shows, or a session
// running in the user's own pane that has focus (no popup for it either).
func userInAcmd(ctx context.Context, snap ipc.Snapshot) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch frontmostApp() {
	case "net.kovidgoyal.kitty":
		sockets := map[string]bool{}
		for _, v := range snap.Views {
			if v.Pane.KittySocket != "" {
				sockets[v.Pane.KittySocket] = true
			}
		}
		for _, s := range snap.Sessions {
			if x := s.External; s.Kind == core.KindExternal && x != nil && x.Terminal == "kitty" && x.KittySocket != "" {
				sockets[x.KittySocket] = true
			}
		}
		for sock := range sockets {
			focused := kittyFocused(ctx, sock)
			if focused == 0 {
				continue
			}
			for _, x := range snap.Views {
				sb := x.Sidebar
				if x.Pane.KittySocket == sock && (x.Pane.KittyWindow == focused || sb.KittyWindow == focused ||
					sb.HeaderWindow == focused || sb.BarWindow == focused || sb.TermWindow == focused) {
					return true, x.Selected
				}
			}
			if id := externalIn(snap, func(x *core.PaneLoc) bool {
				return x.Terminal == "kitty" && x.KittySocket == sock && x.KittyWindow == focused
			}); id != "" {
				return false, id
			}
		}
	case "com.mitchellh.ghostty":
		term, err := ghosttyApp().FocusedTerm(ctx)
		if err != nil || term == "" {
			return false, ""
		}
		for _, x := range snap.Views {
			sb := x.Sidebar
			if x.Pane.GhosttyTerm == term || sb.GhosttyTerm == term || sb.HeaderTerm == term || sb.TermTerm == term {
				return true, x.Selected
			}
		}
		if id := externalIn(snap, func(x *core.PaneLoc) bool {
			if x.Terminal != "ghostty" {
				return false
			}
			if x.GhosttyTerm != "" {
				return x.GhosttyTerm == term
			}
			t, err := ghosttyApp().TermForTTY(ctx, x.TTY)
			return x.TTY != "" && err == nil && t == term
		}); id != "" {
			return false, id
		}
	}
	return false, ""
}

// frontmostApp is the frontmost app's bundle id (swappable in tests).
var frontmostApp = popup.FrontmostApp

// kittyFocused is the focused window of the focused OS window of the Kitty
// on sock (0: none, or Kitty didn't answer).
func kittyFocused(ctx context.Context, sock string) int {
	ws, err := kittyAt(sock).LS(ctx)
	if err != nil {
		return 0
	}
	for _, ow := range ws {
		if !ow.IsFocused {
			continue
		}
		for _, t := range ow.Tabs {
			for _, w := range t.Windows {
				if w.IsFocused {
					return w.ID
				}
			}
		}
	}
	return 0
}

// externalIn is the live (not archived) external session whose pane
// matches, most recently active first.
func externalIn(snap ipc.Snapshot, match func(*core.PaneLoc) bool) string {
	var best *core.Session
	for _, s := range snap.Sessions {
		if s.Kind != core.KindExternal || s.External == nil || s.Status == core.StatusArchived || !match(s.External) {
			continue
		}
		if best == nil || s.LastEventAt.After(best.LastEventAt) {
			best = s
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}

// lockFile takes an exclusive, non-blocking lock on path.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// spawnPopup starts the popup agent, detached, when it isn't running
// (the daemon does this when [popup] enabled is on).
var spawnPopup = func(p paths.Paths) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Clean(p.DaemonLog()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c := exec.Command(exe, "popup") // #nosec G204 -- our own binary
	c.Env = procenv.Scrub(os.Environ())
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}
