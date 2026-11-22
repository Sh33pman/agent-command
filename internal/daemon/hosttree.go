package daemon

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// The known hosts' trees (design 6.14, D14): each host's sessions, groups and
// sets show here as the host has them, with their own ids and their host.
// An op on one of them goes to its host, which makes the change; its tree
// follows here. Structure never crosses hosts: a host's sessions stay in its
// own groups and sets.

// forwardToHost runs req on a host when what it names is a host's, and says
// whether it did.
func (d *Daemon) forwardToHost(ctx context.Context, req ipc.Request) (any, bool, error) {
	if d.client == nil {
		return nil, false, nil
	}
	switch req.Op {
	case ipc.OpMutate:
		m, err := decode[ipc.Mutation](req.Data)
		if err != nil {
			return nil, false, nil // the usual path says what's wrong
		}
		return d.mutateAcross(ctx, m)
	case ipc.OpSessionScreen:
		r, err := decode[ipc.ScreenRequest](req.Data)
		if err != nil {
			return nil, false, nil
		}
		return d.onHostOf(ctx, req, r.ID)
	case ipc.OpResume:
		m, err := decode[ipc.Mutation](req.Data)
		if err != nil {
			return nil, false, nil
		}
		return d.onHostOf(ctx, req, m.ID)
	case ipc.OpNew:
		n, err := decode[ipc.NewRequest](req.Data)
		if err != nil {
			return nil, false, nil
		}
		return d.onHostOf(ctx, req, n.GroupID, n.SetWith)
	}
	return nil, false, nil
}

// hostOfAll is the one host every non-empty id is on: "" when none is a
// host's; an error when they're on different hosts, or some here and some
// on a host.
func (d *Daemon) hostOfAll(ids ...string) (string, error) {
	host, local := "", false
	for _, id := range ids {
		if id == "" {
			continue
		}
		h, ok := d.client.HostOf(id)
		switch {
		case !ok:
			local = true
		case host == "":
			host = h
		case host != h:
			return "", invalid("%s and %s are different hosts: each keeps its own sessions, groups and sets", host, h)
		}
	}
	if host != "" && local {
		return "", invalid("a session, group or set on %s stays on %s: move or join it within %s", host, host, host)
	}
	return host, nil
}

// onHostOf runs req on the host its ids are on.
func (d *Daemon) onHostOf(ctx context.Context, req ipc.Request, ids ...string) (any, bool, error) {
	host, err := d.hostOfAll(ids...)
	if err != nil {
		return nil, true, err
	}
	if host == "" {
		return nil, false, nil
	}
	out, err := d.callHost(ctx, host, req.Op, req.Data)
	return out, true, err
}

func (d *Daemon) callHost(ctx context.Context, host, op string, data json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
	defer cancel()
	raw, err := d.client.Call(ctx, host, op, data)
	if err != nil {
		return nil, unreachable(err)
	}
	return raw, nil
}

// mutateAcross runs a mutation on the host its ids are on (Mutation.Host
// names one for what has no id yet, as a new group). A change to many ids is
// split between this machine and each host, and their results merged in the
// order asked.
func (d *Daemon) mutateAcross(ctx context.Context, m ipc.Mutation) (any, bool, error) {
	if m.Host != "" {
		data, err := json.Marshal(withoutHost(m))
		if err != nil {
			return nil, true, err
		}
		out, err := d.callHost(ctx, m.Host, ipc.OpMutate, data)
		return out, true, err
	}
	refs := []string{m.ID, m.GroupID, m.SetID, m.After, m.Before}
	if len(m.IDs) == 0 {
		host, err := d.hostOfAll(refs...)
		if err != nil || host == "" {
			return nil, err != nil, err
		}
		data, _ := json.Marshal(m)
		out, err := d.callHost(ctx, host, ipc.OpMutate, data)
		return out, true, err
	}
	// Many ids: each host's go to it, the rest are this machine's.
	byHost := map[string][]string{}
	for _, id := range m.IDs {
		h, _ := d.client.HostOf(id)
		byHost[h] = append(byHost[h], id)
	}
	if len(byHost) == 1 && len(byHost[""]) > 0 {
		return nil, false, nil // all here
	}
	results := map[string]ipc.Result{}
	for host, ids := range byHost {
		part := m
		part.IDs = ids
		target, err := d.hostOfAll(append(refs, ids[0])...)
		if err != nil {
			for _, id := range ids {
				results[id] = resultOf(id, err)
			}
			continue
		}
		var res ipc.Results
		if host == "" {
			res, err = d.mutateMany(ctx, part)
		} else {
			var raw json.RawMessage
			data, _ := json.Marshal(part)
			if raw, err = d.callHost(ctx, target, ipc.OpMutate, data); err == nil {
				err = json.Unmarshal(raw, &res)
			}
		}
		if err != nil {
			for _, id := range ids {
				results[id] = resultOf(id, err)
			}
			continue
		}
		for _, r := range res.Results {
			results[r.ID] = r
		}
	}
	out := ipc.Results{Results: []ipc.Result{}}
	for _, id := range m.IDs {
		if r, ok := results[id]; ok && !slices.ContainsFunc(out.Results, func(x ipc.Result) bool { return x.ID == id }) {
			out.Results = append(out.Results, r)
		}
	}
	return out, true, nil
}

func withoutHost(m ipc.Mutation) ipc.Mutation {
	m.Host = ""
	return m
}

// treeSession is one of a host's sessions, as internal/core has it.
func (d *Daemon) treeSession(id string) (*core.Session, bool) {
	for _, s := range d.client.Tree().Sessions {
		if s.ID == id {
			return s.Core(), true
		}
	}
	return nil, false
}
