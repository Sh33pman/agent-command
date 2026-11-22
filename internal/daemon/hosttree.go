package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// The known hosts' trees (design 6.14, D14): each host's sessions, groups and
// sets show here as the host has them, with their own ids and their host.
// An op on one of them names its host ({host, id}, design 7.4), and goes to
// it: the host makes the change, and its tree follows here. A host is never
// found from an id: ids are unique only within their host, and every host's
// Default group is "default". Structure never crosses hosts: a host's
// sessions stay in its own groups and sets.

// forwardToHost runs req on the host it names, and says whether it did.
func (d *Daemon) forwardToHost(ctx context.Context, req ipc.Request) (any, bool, error) {
	var host string
	switch req.Op {
	case ipc.OpSessionScreen:
		r, err := decode[ipc.ScreenRequest](req.Data)
		if err != nil {
			return nil, false, nil // the usual path says what's wrong
		}
		host, r.Host = r.Host, ""
		req.Data, _ = json.Marshal(r)
	default:
		return nil, false, nil
	}
	if host == "" {
		return nil, false, nil
	}
	out, err := d.callHost(ctx, host, req.Op, req.Data)
	return out, true, err
}

// callHost runs op on a known host. A host that isn't known is not_found.
func (d *Daemon) callHost(ctx context.Context, host, op string, data json.RawMessage) (json.RawMessage, error) {
	if !d.client.Knows(host) {
		return nil, &codedError{code: ipc.CodeNotFound, err: errors.New("there's no Remote host named " + quote(host))}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteCallTimeout)
	defer cancel()
	raw, err := d.client.Call(ctx, host, op, data)
	if err != nil {
		return nil, unreachable(err)
	}
	return raw, nil
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// refersToStructure says whether m's kind changes sessions within a group,
// set or order, so every target must be on the host those are on.
func refersToStructure(m ipc.Mutation) bool {
	return m.GroupID != "" || m.SetID != "" || m.After != "" || m.Before != "" ||
		isSetMutation(m.Kind) || m.Kind == ipc.MutMove
}

// routeMutation runs m where its targets are: this machine's part here, each
// host's part on that host. A kind that refers to a group, set or order runs
// on one host only; the others are split per host, and their results merged
// in the order asked, each naming its host.
func (d *Daemon) routeMutation(ctx context.Context, m ipc.Mutation) (any, error) {
	if len(m.Targets) == 0 && len(m.Items) == 0 {
		if m.Host == "" {
			return d.mutate(ctx, m)
		}
		return d.mutateOn(ctx, m.Host, m)
	}
	hosts := targetHosts(m)
	if len(hosts) == 1 {
		host := hosts[0]
		if refersToStructure(m) && host != m.Host {
			return nil, invalid("%s isn't on %s: a session moves or joins only within its own host", where(host), where(m.Host))
		}
		if host == "" {
			return d.mutate(ctx, here(m))
		}
		return d.mutateOn(ctx, host, m)
	}
	if refersToStructure(m) {
		return nil, invalid("these are on different hosts: a session moves or joins only within its own host")
	}
	if len(m.Items) > 0 {
		var firstErr error
		for _, host := range hosts {
			part := m
			part.Items = slices.DeleteFunc(slices.Clone(m.Items), func(t ipc.ItemTarget) bool { return t.Host != host })
			if _, err := d.runPart(ctx, host, part); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return nil, firstErr
	}
	if !slices.Contains(batchKinds, m.Kind) {
		return nil, invalid("%s takes one session at a time", m.Kind)
	}
	results := map[ipc.Target]ipc.Result{}
	for _, host := range hosts {
		part := m
		part.Targets = slices.DeleteFunc(slices.Clone(m.Targets), func(t ipc.Target) bool { return t.Host != host })
		out, err := d.runPart(ctx, host, part)
		var res ipc.Results
		if err == nil {
			res, err = asResults(out)
		}
		if err != nil {
			for _, t := range part.Targets {
				r := resultOf(t.ID, err)
				r.Host = host
				results[t] = r
			}
			continue
		}
		for _, r := range res.Results {
			r.Host = host
			results[ipc.Target{Host: host, ID: r.ID}] = r
		}
	}
	out := ipc.Results{Results: []ipc.Result{}}
	seen := map[ipc.Target]bool{}
	for _, t := range m.Targets {
		if r, ok := results[t]; ok && !seen[t] {
			seen[t] = true
			out.Results = append(out.Results, r)
		}
	}
	return out, nil
}

// runPart runs the part of a mutation on one host ("" for here).
func (d *Daemon) runPart(ctx context.Context, host string, part ipc.Mutation) (any, error) {
	if host == "" {
		return d.mutate(ctx, here(part))
	}
	return d.mutateOn(ctx, host, part)
}

// mutateOn runs m on host, where its targets are the host's own: their host
// is "" there. The results say which host they're from.
func (d *Daemon) mutateOn(ctx context.Context, host string, m ipc.Mutation) (any, error) {
	m.Host = ""
	for i := range m.Targets {
		m.Targets[i].Host = ""
	}
	for i := range m.Items {
		m.Items[i].Host = ""
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	raw, err := d.callHost(ctx, host, ipc.OpMutate, data)
	if err != nil {
		return nil, err
	}
	if slices.Contains(batchKinds, m.Kind) {
		res, err := asResults(raw)
		if err != nil {
			return raw, nil
		}
		for i := range res.Results {
			res.Results[i].Host = host
		}
		// One session that couldn't change is an error, as here.
		if len(m.Targets) == 1 && len(res.Results) == 1 && !res.Results[0].OK {
			r := res.Results[0]
			return nil, &codedError{code: r.Code, err: errors.New(r.Error)}
		}
		return res, nil
	}
	return raw, nil
}

// asResults reads a batch's results, from here or from a host.
func asResults(out any) (ipc.Results, error) {
	switch v := out.(type) {
	case ipc.Results:
		return v, nil
	case json.RawMessage:
		var res ipc.Results
		err := json.Unmarshal(v, &res)
		return res, err
	}
	return ipc.Results{}, errors.New("no results")
}

// targetHosts are the hosts m's targets and items are on, in the order they
// first appear ("" for here).
func targetHosts(m ipc.Mutation) []string {
	var hosts []string
	add := func(h string) {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	for _, t := range m.Targets {
		add(t.Host)
	}
	for _, t := range m.Items {
		add(t.Host)
	}
	return hosts
}

// here is m as this machine runs it: its targets become its own ids.
func here(m ipc.Mutation) ipc.Mutation {
	m.LocalIDs = nil
	for _, t := range m.Targets {
		m.LocalIDs = append(m.LocalIDs, t.ID)
	}
	if len(m.LocalIDs) == 1 {
		m.LocalID = m.LocalIDs[0]
	}
	m.LocalItemIDs = nil
	for _, t := range m.Items {
		m.LocalItemIDs = append(m.LocalItemIDs, t.ID)
	}
	return m
}

func where(host string) string {
	if host == "" {
		return "this machine"
	}
	return host
}

// mustJSON is v as JSON, for a request this package builds.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
