package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/client"
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
		Settings:    func() config.Remote { return d.config().Remote },
		Data:        knownHostData{d},
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
		ipc.OpRemoteBrowse, ipc.OpRemoteNew,
		ipc.OpRemoteFoldersRecent, ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir, ipc.OpRemoteHarnesses, ipc.OpRemoteCall:
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
	case ipc.OpRemoteHostAdd, ipc.OpRemoteHostEdit, ipc.OpRemoteNew, ipc.OpRemoteFoldersList, ipc.OpRemoteFoldersMkdir, ipc.OpRemoteCall:
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
	case ipc.OpRemoteNew:
		r, err := decode[ipc.RemoteNewRequest](req.Data)
		if err != nil {
			return nil, err
		}
		return c.NewSession(ctx, r)
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
	case ipc.OpRemoteCall:
		r, err := decode[ipc.RemoteCall](req.Data)
		if err != nil {
			return nil, err
		}
		return c.Call(ctx, r.Host, r.Op, r.Data)
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

func (d *Daemon) sessionByID(ctx context.Context, id string) (*core.Session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.store.Session(ctx, id)
}

// answerOnHost answers one of a host's items on its host, by the host's own
// id. This machine's guardrail applies first: the host sees a paired
// device, not who asked here.
func (d *Daemon) answerOnHost(ctx context.Context, a ipc.InboxAnswer) error {
	it, ok := d.client.InboxItem(a.Host, a.ID)
	if !ok {
		return &codedError{code: ipc.CodeNotFound, err: errors.New("this is no longer waiting for an answer")}
	}
	if err := d.mayAnswer(ctx, it.Kind); err != nil {
		return err
	}
	host := a.Host
	a.Host = ""
	_, err := d.callHost(ctx, host, ipc.OpInboxAnswer, mustJSON(a))
	return err
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

// clientBackend is the daemon for the client of Remote hosts: their notices
// show here, and their changes reach the apps.
type clientBackend struct{ d *Daemon }

var _ client.Backend = clientBackend{}

// Notice shows a host's notification about one of its sessions, as this
// machine's settings allow, naming the host.
func (b clientBackend) Notice(id, host string, n ipc.Notice) {
	d := b.d
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refreshNotify()
	no := d.notify
	if no == nil {
		return
	}
	if n.Kind != "withdraw" {
		// The host routes its notices to the machine you're using, as to its
		// own UIs: one it routed elsewhere isn't this machine's to show.
		if !slices.Contains(n.To, d.client.Fingerprint()) {
			return
		}
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
