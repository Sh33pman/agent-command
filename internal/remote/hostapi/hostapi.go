// Package hostapi serves the host API (internal/remote/api) to Hosting's
// authorized clients: a filtered subset of the daemon, limited to its Remote
// sessions that aren't archived. The daemon supplies its sessions and carries
// out changes through Backend; everything a client may see is decided here.
package hostapi

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// State is the daemon as the host API reads it.
type State struct {
	Sessions []*core.Session // every session, archived ones too
	Inbox    []core.InboxItem
	Notices  []ipc.Notice // the recent notifications, oldest first
}

// Backend is the daemon, for the host API.
type Backend interface {
	// State reads the sessions, the open inbox items and the recent notices.
	State(ctx context.Context) (State, error)
	// Changes is the daemon's revision, and a channel closed when it next
	// changes.
	Changes() (rev uint64, next <-chan struct{})
	// NewSession starts a session as `acmd new` does (req.Remote is set).
	NewSession(ctx context.Context, req ipc.NewRequest) (*core.Session, error)
	Rename(ctx context.Context, id, name string) error
	// End archives a session, or with finish stops its agent and keeps it.
	End(ctx context.Context, id string, finish bool) error
	// HostSocket is where a session's terminal listens.
	HostSocket(id string) string
	// Harnesses are what's installed, and the default ("" when not chosen).
	Harnesses() (installed []string, def string)
	// Config is the settings, and SetConfig changes them (config.set).
	Config() ipc.ConfigState
	SetConfig(values map[string]any) (ipc.ConfigState, error)
	// Call runs a daemon op as a client on its socket would, and answers its
	// data as the socket sends it. Callable has already allowed op; an
	// error with a code (a *wire.CallError) keeps it.
	Call(ctx context.Context, op string, data json.RawMessage) (json.RawMessage, error)
	// Subscribe sends the daemon's event stream until send fails or ctx ends.
	Subscribe(ctx context.Context, req ipc.SubscribeRequest, send func(ipc.Event) error) error
}

// Registrar is Hosting (*host.Host): what serves ops to its clients, and
// keeps track of who is attached to which session.
type Registrar interface {
	Handle(op string, fn wire.Handler)
	HandleStream(op string, fn wire.StreamHandler)
	Attach(c *wire.Conn, session string, stream io.Closer) (release func())
}

// Options configure the API.
type Options struct {
	Home string // the host user's home folder: "~" and relative paths start there (default: $HOME)
}

// API is the host API, served through a Registrar.
type API struct {
	b    Backend
	r    Registrar
	home string
}

// Register serves the host API's ops on r.
func Register(r Registrar, b Backend, o Options) *API {
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	a := &API{b: b, r: r, home: o.Home}
	r.Handle(api.OpSessionsList, a.list)
	r.HandleStream(api.OpSessionAttach, a.attach)
	r.Handle(api.OpSessionNew, a.newSession)
	r.Handle(api.OpSessionRename, a.rename)
	r.Handle(api.OpSessionEnd, a.end)
	r.Handle(api.OpFoldersRecent, a.recentFolders)
	r.Handle(api.OpFoldersList, a.listFolders)
	r.Handle(api.OpFoldersMkdir, a.mkdir)
	r.Handle(api.OpHarnessesList, a.harnesses)
	r.HandleStream(api.OpEvents, a.events)
	r.Handle(api.OpConfigGet, a.configGet)
	r.Handle(api.OpConfigSet, a.configSet)
	r.Handle(api.OpCall, a.call)
	r.HandleStream(api.OpSubscribe, a.subscribe)
	return a
}

// Visible says whether clients may see s: Remote, and not archived.
func Visible(s *core.Session) bool {
	return s != nil && s.Remote && s.Status != core.StatusArchived
}

// view is what clients see of a State: the visible sessions, by id, and
// their open inbox items.
type view struct {
	sessions map[string]api.Session
	inbox    map[string][]api.InboxItem
}

func newView(st State) view {
	v := view{sessions: map[string]api.Session{}, inbox: map[string][]api.InboxItem{}}
	for _, s := range st.Sessions {
		if Visible(s) {
			v.sessions[s.ID] = api.Session{}
		}
	}
	for _, it := range st.Inbox {
		if _, ok := v.sessions[it.SessionID]; ok && it.State == core.InboxOpen {
			v.inbox[it.SessionID] = append(v.inbox[it.SessionID], inboxItem(it))
		}
	}
	for _, s := range st.Sessions {
		if Visible(s) {
			v.sessions[s.ID] = summary(s, v.inbox[s.ID])
		}
	}
	return v
}

// list is the visible sessions, the most recently active first.
func (v view) list() []api.Session {
	out := make([]api.Session, 0, len(v.sessions))
	for _, s := range v.sessions {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b api.Session) int {
		return cmp.Or(b.LastActivity.Compare(a.LastActivity), strings.Compare(a.ID, b.ID))
	})
	return out
}

// allInbox is every visible session's open items, oldest first.
func (v view) allInbox() []api.InboxItem {
	var out []api.InboxItem
	for _, items := range v.inbox {
		out = append(out, items...)
	}
	slices.SortFunc(out, func(a, b api.InboxItem) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func summary(s *core.Session, inbox []api.InboxItem) api.Session {
	out := api.Session{ID: s.ID, Name: s.Name, Harness: cmp.Or(s.Harness, core.HarnessClaude), Dir: s.Cwd,
		Status: string(s.Status), StatusDetail: s.StatusDetail, Unseen: s.Unseen, Inbox: len(inbox),
		LastActivity: lastActive(s), Created: s.CreatedAt}
	for _, it := range inbox {
		switch it.Kind {
		case core.InboxQuestion, core.InboxPermission, core.InboxError:
			out.NeedsYou++
		}
	}
	return out
}

// lastActive is when s last did something: its last event, else when it was
// made.
func lastActive(s *core.Session) time.Time {
	if s.LastEventAt.IsZero() {
		return s.CreatedAt
	}
	return s.LastEventAt
}

func inboxItem(it core.InboxItem) api.InboxItem {
	return api.InboxItem{ID: it.ID, SessionID: it.SessionID, Kind: it.Kind, Text: it.Text, Options: it.Options,
		Multipart: it.Multipart, At: it.At, Read: it.Read}
}

func notice(n ipc.Notice) api.Notice {
	return api.Notice{Seq: n.Seq, SessionID: n.SessionID, Kind: n.Kind, Title: n.Title, Body: n.Body, Urgent: n.Urgent}
}

// notFound is the answer for a session clients may not see, whether or not
// it exists.
func notFound(id string) error {
	return wire.Errorf(wire.CodeNotFound, "no Remote session %q on this host", id)
}

// find is a visible session.
func (a *API) find(ctx context.Context, id string) (*core.Session, State, error) {
	st, err := a.b.State(ctx)
	if err != nil {
		return nil, st, err
	}
	for _, s := range st.Sessions {
		if s.ID == id {
			if !Visible(s) {
				break
			}
			return s, st, nil
		}
	}
	return nil, st, notFound(id)
}

// visibleSummary is a visible session as clients see it.
func (a *API) visibleSummary(ctx context.Context, id string) (api.Session, error) {
	st, err := a.b.State(ctx)
	if err != nil {
		return api.Session{}, err
	}
	s, ok := newView(st).sessions[id]
	if !ok {
		return api.Session{}, notFound(id)
	}
	return s, nil
}

func (a *API) list(ctx context.Context, _ *wire.Call) (any, error) {
	st, err := a.b.State(ctx)
	if err != nil {
		return nil, err
	}
	return api.SessionList{Sessions: newView(st).list()}, nil
}

func (a *API) rename(ctx context.Context, c *wire.Call) (any, error) {
	var r api.Rename
	if err := c.Decode(&r); err != nil {
		return nil, err
	}
	if _, _, err := a.find(ctx, r.ID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		return nil, wire.Errorf(wire.CodeBadRequest, "the name is empty")
	}
	if err := a.b.Rename(ctx, r.ID, name); err != nil {
		return nil, err
	}
	return a.visibleSummary(ctx, r.ID)
}

func (a *API) end(ctx context.Context, c *wire.Call) (any, error) {
	var e api.End
	if err := c.Decode(&e); err != nil {
		return nil, err
	}
	if _, _, err := a.find(ctx, e.ID); err != nil {
		return nil, err
	}
	return nil, a.b.End(ctx, e.ID, e.Finish)
}

func (a *API) newSession(ctx context.Context, c *wire.Call) (any, error) {
	var n api.NewSession
	if err := c.Decode(&n); err != nil {
		return nil, err
	}
	dir, err := a.folder(n.Dir)
	if err != nil {
		return nil, err
	}
	h, err := a.harness(n.Harness)
	if err != nil {
		return nil, err
	}
	remote := true
	s, err := a.b.NewSession(ctx, ipc.NewRequest{Cwd: dir, Harness: h, Name: strings.TrimSpace(n.Name), Prompt: n.Prompt,
		Args: n.Args, Remote: &remote})
	if err != nil {
		return nil, err
	}
	return summary(s, nil), nil
}

// harness is the harness a new session runs: name, or the host's default,
// as long as it's installed here.
func (a *API) harness(name string) (string, error) {
	installed, def := a.b.Harnesses()
	name = cmp.Or(strings.TrimSpace(name), def, core.HarnessClaude)
	if harnessTitle(name) == "" {
		return "", wire.Errorf(wire.CodeBadRequest, "unknown harness %q", name)
	}
	if !slices.Contains(installed, name) {
		return "", wire.Errorf(wire.CodeNotFound, "%s isn't installed on this host", harnessTitle(name))
	}
	return name, nil
}

func harnessTitle(name string) string {
	switch name {
	case core.HarnessClaude, core.HarnessAntigravity:
		return core.AgentApp(name)
	case core.HarnessTerminal:
		return "Terminal"
	}
	return ""
}

func (a *API) harnesses(context.Context, *wire.Call) (any, error) {
	installed, def := a.b.Harnesses()
	out := api.Harnesses{Harnesses: []api.Harness{}}
	for _, name := range installed {
		if t := harnessTitle(name); t != "" {
			out.Harnesses = append(out.Harnesses, api.Harness{Name: name, Title: t})
		}
	}
	if slices.Contains(installed, def) {
		out.Default = def
	}
	return out, nil
}
