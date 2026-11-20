package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/client"
	"github.com/brettmostert/agent-command/internal/store"
)

// Remote hosts: this machine as a client of other acmds' Hosting. A session
// added from a host is a row like any other, with Host and RemoteID set, so
// groups, panes, the inbox and the sidebar take it as they are. Its socket
// is where a local session's host listens (Paths.HostSocket), and relays to
// the host (internal/remote/client). Nothing here runs it: the reconciler,
// the foreground and agent checks and the transcript tail leave it alone,
// and its host's events say how it is.

// remoteCallTimeout bounds an op that goes to a host.
var remoteCallTimeout = 15 * time.Second

func (d *Daemon) startRemoteHosts(ctx context.Context) {
	d.client = client.New(client.Options{
		ConfigFile:  d.cfg.ConfigFile,
		IdentityDir: d.cfg.Paths.RemoteIdentityDir(),
		Version:     d.cfg.Version,
		Log:         d.cfg.Log,
		Now:         d.cfg.Now,
		Socket:      d.cfg.Paths.HostSocket,
	}, clientBackend{d})
	if err := d.client.Start(ctx); err != nil {
		d.cfg.Log.Printf("remote hosts: %v", err)
	}
}

func isRemoteHostsOp(op string) bool {
	switch op {
	case ipc.OpRemoteHosts, ipc.OpRemoteHostAdd, ipc.OpRemoteHostEdit, ipc.OpRemoteHostRemove, ipc.OpRemoteHostRetry,
		ipc.OpRemoteBrowse, ipc.OpRemoteSessionAdd, ipc.OpRemoteSessionRemove, ipc.OpRemoteNew,
		ipc.OpRemoteFoldersRecent, ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir, ipc.OpRemoteHarnesses:
		return true
	}
	return false
}

func (d *Daemon) handleRemoteHosts(ctx context.Context, req ipc.Request) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
	defer cancel()
	c := d.client
	var ref ipc.RemoteHostRef
	switch req.Op {
	case ipc.OpRemoteHostAdd, ipc.OpRemoteHostEdit, ipc.OpRemoteNew, ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir:
	default:
		if len(req.Data) > 0 {
			var err error
			if ref, err = decode[ipc.RemoteHostRef](req.Data); err != nil {
				return nil, err
			}
		}
	}
	switch req.Op {
	case ipc.OpRemoteHosts:
		return c.Hosts(), nil
	case ipc.OpRemoteHostAdd:
		e, err := decode[ipc.RemoteHostEdit](req.Data)
		if err != nil {
			return nil, err
		}
		return c.AddHost(e)
	case ipc.OpRemoteHostEdit:
		e, err := decode[ipc.RemoteHostEdit](req.Data)
		if err != nil {
			return nil, err
		}
		return c.EditHost(ctx, e)
	case ipc.OpRemoteHostRemove:
		return nil, c.RemoveHost(ctx, ref.Host)
	case ipc.OpRemoteHostRetry:
		return c.Retry(ref.Host)
	case ipc.OpRemoteBrowse:
		return c.Browse(ctx, ref.Host)
	case ipc.OpRemoteSessionAdd:
		id, err := c.AddSession(ctx, ref.Host, ref.ID, "")
		if err != nil {
			return nil, err
		}
		return d.sessionByID(ctx, id)
	case ipc.OpRemoteSessionRemove:
		return nil, c.RemoveSession(ctx, ref.ID)
	case ipc.OpRemoteNew:
		r, err := decode[ipc.RemoteNewRequest](req.Data)
		if err != nil {
			return nil, err
		}
		id, err := c.NewSession(ctx, r)
		if err != nil {
			return nil, err
		}
		s, err := d.sessionByID(ctx, id)
		if err != nil {
			return nil, err
		}
		return d.joinSet(ctx, s, r.SetWith), nil
	case ipc.OpRemoteFoldersRecent:
		return c.RecentFolders(ctx, ref.Host)
	case ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir:
		r, err := decode[ipc.RemoteFolderRequest](req.Data)
		if err != nil {
			return nil, err
		}
		if req.Op == ipc.OpRemoteFoldersMkdir {
			return c.MakeFolder(ctx, r)
		}
		return c.ListFolders(ctx, r)
	case ipc.OpRemoteHarnesses:
		return c.Harnesses(ctx, ref.Host)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

func (d *Daemon) sessionByID(ctx context.Context, id string) (*core.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.store.Session(ctx, id)
}

// mutateOnHost carries out a mutation of a session added from a host: a
// rename or an end goes to the host. It reports whether m was one; the rest
// apply here as for any session. d.mu isn't held: the host may take a moment.
func (d *Daemon) mutateOnHost(ctx context.Context, m ipc.Mutation) (bool, error) {
	switch m.Kind {
	case ipc.MutRename, ipc.MutArchive, ipc.MutFinish, ipc.MutUnarchive, ipc.MutRemote:
	default:
		return false, nil
	}
	s, err := d.sessionByID(ctx, m.ID)
	if err != nil || !s.OnHost() {
		return false, nil // not found: the usual path says so
	}
	ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
	defer cancel()
	switch m.Kind {
	case ipc.MutRename:
		name := strings.TrimSpace(m.Text)
		if name == "" {
			return true, errors.New("name is empty")
		}
		return true, d.client.Rename(ctx, s.ID, name)
	case ipc.MutFinish:
		if s.HostGone {
			return true, fmt.Errorf("%s no longer offers this session: remove it", s.Host)
		}
		return true, d.client.End(ctx, s.ID, true)
	case ipc.MutArchive:
		if s.Status == core.StatusArchived {
			return true, nil
		}
		if !s.HostGone { // ended there, then here
			if err := d.client.End(ctx, s.ID, false); err != nil {
				return true, err
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return true, d.archive(ctx, s.ID)
	case ipc.MutUnarchive:
		if s.HostGone {
			return true, fmt.Errorf("%s no longer offers this session: remove it", s.Host)
		}
		d.mu.Lock()
		err := d.updateSession(ctx, s.ID, func(s *core.Session) error {
			core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
			return nil
		})
		d.mu.Unlock()
		if err == nil {
			d.client.SetArchived(s.ID, false)
		}
		return true, err
	default: // MutRemote
		return true, fmt.Errorf("this session runs on %s: only %s can make it Remote", s.Host, s.Host)
	}
}

// splitInboxIDs parts inbox item ids into this machine's and the hosts'
// (negative).
func splitInboxIDs(ids []int64) (local, hosts []int64) {
	for _, id := range ids {
		if id < 0 {
			hosts = append(hosts, id)
		} else {
			local = append(local, id)
		}
	}
	return local, hosts
}

// answerOnHost types an answer to an item from a host into its session,
// through its socket, as for a local one.
func (d *Daemon) answerOnHost(ctx context.Context, a ipc.InboxAnswer) error {
	it, ok := d.client.InboxItem(a.ID)
	if !ok {
		return errors.New("this is no longer waiting for an answer")
	}
	s, err := d.sessionByID(ctx, it.SessionID)
	if err != nil {
		return err
	}
	keys, err := answerKeys(&it, s, a)
	if err != nil {
		return err
	}
	if err := d.typeKeys(it.SessionID, keys); err != nil {
		return err
	}
	d.client.HandleInbox([]int64{a.ID})
	return nil
}

// withHostInbox is the queue with the hosts' items, in arrival order.
func (d *Daemon) withHostInbox(queue []core.InboxItem) []core.InboxItem {
	if d.client == nil {
		return queue
	}
	theirs := d.client.Inbox()
	if len(theirs) == 0 {
		return queue
	}
	out := append(slices.Clone(queue), theirs...)
	slices.SortStableFunc(out, func(a, b core.InboxItem) int { return a.At.Compare(b.At) })
	return out
}

func (d *Daemon) remoteHosts() []ipc.RemoteHost {
	if d.client == nil {
		return nil
	}
	return d.client.Hosts()
}

// clientBackend keeps the sessions added from hosts for the client.
type clientBackend struct{ d *Daemon }

var _ client.Backend = clientBackend{}

func (b clientBackend) Added(ctx context.Context) ([]client.Added, error) {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, true)
	if err != nil {
		return nil, err
	}
	var out []client.Added
	for _, s := range sessions {
		if s.OnHost() {
			out = append(out, client.Added{ID: s.ID, Host: s.Host, RemoteID: s.RemoteID, Archived: s.Status == core.StatusArchived})
		}
	}
	return out, nil
}

func (b clientBackend) Add(ctx context.Context, host string, rs api.Session, group string) (*core.Session, error) {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		if s.Host == host && s.RemoteID == rs.ID {
			if s.Status == core.StatusArchived { // added again: back from the archive
				client.Apply(s, rs)
				s.Status, s.ArchivedAt = core.Status(rs.Status), time.Time{}
				if err := d.store.UpdateSession(ctx, s); err != nil {
					return nil, err
				}
				d.changes.bump()
			}
			return s, nil
		}
	}
	s := client.Local(host, rs)
	s.GroupID = group
	if err := d.store.CreateSession(ctx, s); err != nil {
		return nil, err
	}
	if err := d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, Kind: string(core.EventCreated), Detail: "added from " + host}); err != nil {
		return nil, err
	}
	d.changes.bump()
	return s, nil
}

func (b clientBackend) Remove(ctx context.Context, id string) error {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.store.DeleteSession(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	d.forget(id)
	d.changes.bump()
	return nil
}

func (b clientBackend) RenameHost(ctx context.Context, old, name string) error {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.store.RenameHost(ctx, old, name); err != nil {
		return err
	}
	d.changes.bump()
	return nil
}

func (b clientBackend) Update(ctx context.Context, id string, u client.Update) error {
	return b.edit(ctx, id, func(s *core.Session) {
		client.Apply(s, u.Session)
		if u.Unseen != nil {
			s.Unseen = *u.Unseen
		}
	})
}

func (b clientBackend) Gone(ctx context.Context, id string) error {
	return b.edit(ctx, id, func(s *core.Session) { s.HostGone = true })
}

// edit changes an added session, and wakes the watchers when it changed.
func (b clientBackend) edit(ctx context.Context, id string, fn func(*core.Session)) error {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	s, err := d.store.Session(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil // removed meanwhile
	}
	if err != nil {
		return err
	}
	before := *s
	fn(s)
	if reflect.DeepEqual(before, *s) {
		return nil
	}
	if err := d.store.UpdateSession(ctx, s); err != nil {
		return err
	}
	d.changes.bump()
	return nil
}

// Notice shows a host's notification about an added session, as this
// machine's settings allow, naming the host.
func (b clientBackend) Notice(id, host string, n api.Notice) {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refreshNotify()
	no := d.notify
	if no == nil {
		return
	}
	if n.Kind != "withdraw" {
		on := map[string]bool{"waiting": no.cfg.OnWaiting, "error": no.cfg.OnError, "done": no.cfg.OnDone, "paused": no.cfg.OnPaused}
		if !no.cfg.Enabled || !on[n.Kind] {
			return
		}
	}
	out := ipc.Notice{SessionID: id, Kind: n.Kind, Title: n.Title, Body: n.Body, Urgent: n.Urgent, Host: host}
	if n.Kind != "withdraw" {
		out.Sound = no.cfg.Sound
		out.Title = cmp.Or(n.Title, "Remote session") + " · " + host
	}
	d.addNotice(out)
	d.changes.bump()
}

func (b clientBackend) Changed() { b.d.changes.bump() }
