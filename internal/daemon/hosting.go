package daemon

import (
	"fmt"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/host"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
)

// Remote sessions: Hosting runs alongside the unix socket, and follows
// [remote] in config.toml. The apps and the CLI manage it with the remote.*
// ops; they never touch the network themselves.

func (d *Daemon) startRemote() {
	d.remote = host.New(host.Options{
		Settings:    func() config.Remote { return d.config().Remote },
		SetSettings: func(m map[string]any) error { _, err := d.setConfig(m); return err },
		Data:        hostData{d},
		IdentityDir: d.cfg.Paths.RemoteIdentityDir(),
		Version:     d.cfg.Version,
		Log:         d.cfg.Log,
		Now:         d.cfg.Now,
		OnChange:    d.changes.bump,
	})
	// What clients may do: the host API, on Remote sessions only.
	hostapi.Register(d.remote, hostBackend{d}, hostapi.Options{Home: d.cfg.Home})
	d.remote.Reload() // it logs what's wrong
	// A session turned local drops the clients attached to it at once.
	d.OnRemoteChanged(func(c RemoteChange) {
		if !c.Remote {
			d.remote.CloseSession(c.SessionID)
		}
	})
}

func isRemoteOp(op string) bool {
	return strings.HasPrefix(op, "remote.")
}

func (d *Daemon) handleRemote(req ipc.Request) (any, error) {
	switch req.Op {
	case ipc.OpRemoteIdentity:
		return d.remoteIdentity()
	case ipc.OpRemoteRegenerate:
		return d.regenerate()
	case ipc.OpRemoteCert:
		c, err := decode[ipc.RemoteCert](req.Data)
		if err != nil {
			return nil, err
		}
		return d.setCert(c)
	}
	var ref ipc.RemoteRef
	if len(req.Data) > 0 {
		var err error
		if ref, err = decode[ipc.RemoteRef](req.Data); err != nil {
			return nil, err
		}
	}
	h := d.remote
	switch req.Op {
	case ipc.OpRemoteStatus:
		return h.Status(), nil
	case ipc.OpRemoteHosting:
		return h.SetHosting(ref.Enabled)
	case ipc.OpRemoteReload:
		if d.settings().Reload() {
			d.changes.bump()
		}
		h.Reload()
		return h.Status(), nil
	case ipc.OpRemoteClients:
		return h.Clients(), nil
	case ipc.OpRemoteRequests:
		return h.Requests(), nil
	case ipc.OpRemoteApprove:
		return h.Approve(ref.ID, ref.Name)
	case ipc.OpRemoteDeny:
		return nil, h.Deny(ref.ID)
	case ipc.OpRemoteAuthorize:
		ref, err := clientByCert(ref)
		if err != nil {
			return nil, err
		}
		return h.Authorize(ref.Fingerprint, ref.Name)
	case ipc.OpRemoteRevoke:
		return h.Revoke(ref.Client)
	case ipc.OpRemoteRename:
		return h.Rename(ref.Client, ref.Name)
	case ipc.OpRemoteScope:
		return h.SetScope(ref.Client, ref.Scope)
	case ipc.OpRemoteDisconnect:
		n, err := h.Disconnect(ref.Client)
		return ipc.RemoteDisconnected{Closed: n}, err
	default:
		return nil, fmt.Errorf("unknown op %q", req.Op)
	}
}
