package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
)

// Browse lists a host's Remote sessions, and which are added here.
func (c *Client) Browse(ctx context.Context, host string) (ipc.RemoteBrowse, error) {
	var list api.SessionList
	if err := c.call(ctx, host, api.OpSessionsList, nil, &list); err != nil {
		return ipc.RemoteBrowse{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	local := map[string]string{}
	for _, a := range c.sortedAddedLocked(host) {
		local[a.RemoteID] = a.ID
	}
	out := ipc.RemoteBrowse{Host: host, Sessions: []ipc.RemoteBrowseSession{}}
	for _, s := range list.Sessions {
		id := local[s.ID]
		out.Sessions = append(out.Sessions, ipc.RemoteBrowseSession{Session: s, Added: id != "", LocalID: id})
	}
	return out, nil
}

// addedLocked is the session added from host with remoteID, if it is.
func (c *Client) addedLocked(host, remoteID string) *added {
	for _, a := range c.added {
		if a.Host == host && a.RemoteID == remoteID {
			return a
		}
	}
	return nil
}

// AddSession adds a host's session here (in group, "" = Default), and
// returns its local id. One already added is left as it is.
func (c *Client) AddSession(ctx context.Context, host, remoteID, group string) (string, error) {
	c.mu.Lock()
	a := c.addedLocked(host, remoteID)
	c.mu.Unlock()
	if a != nil {
		return a.ID, nil
	}
	var list api.SessionList
	if err := c.call(ctx, host, api.OpSessionsList, nil, &list); err != nil {
		return "", err
	}
	for _, s := range list.Sessions {
		if s.ID == remoteID {
			return c.add(ctx, host, s, group)
		}
	}
	return "", fmt.Errorf("%s has no Remote session %q", host, remoteID)
}

// add records s from host as added, and opens its socket.
func (c *Client) add(ctx context.Context, host string, s api.Session, group string) (string, error) {
	local, err := c.b.Add(ctx, host, s, group)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.added[local.ID]
	if a == nil {
		unseen := s.Unseen
		a = &added{Added: Added{ID: local.ID, Host: host, RemoteID: s.ID}, hostUnseen: &unseen}
		c.added[local.ID] = a
	}
	a.Archived = false
	c.openLocked(a)
	if h := c.hosts[host]; h != nil {
		c.inbox.set(a.ID, host, h.items[s.ID])
		h.wake()
		h.notifyLocked()
	}
	return local.ID, nil
}

// RemoveSession removes an added session here. The session on its host
// isn't touched.
func (c *Client) RemoveSession(ctx context.Context, id string) error {
	c.mu.Lock()
	a := c.added[id]
	c.mu.Unlock()
	if a == nil {
		return fmt.Errorf("%s isn't a session added from a Remote host", id)
	}
	if err := c.b.Remove(ctx, id); err != nil {
		return err
	}
	c.forget(id)
	return nil
}

// forget drops an added session the daemon no longer has: purged, or removed.
func (c *Client) forget(id string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.added[id]
	if a == nil {
		return
	}
	delete(c.added, id)
	c.closeProxyLocked(a)
	c.inbox.set(id, a.Host, nil)
	if h := c.hosts[a.Host]; h != nil {
		h.notifyLocked()
	}
}

// Forget is the daemon saying it no longer has an added session (purged).
func (c *Client) Forget(id string) { c.forget(id) }

// Is reports whether id is a session added from a host.
func (c *Client) Is(id string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.added[id] != nil
}

// SetArchived follows an added session ended (or brought back) here: an
// archived one has no socket, and doesn't keep its host connected.
func (c *Client) SetArchived(id string, archived bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.added[id]
	if a == nil || a.Archived == archived {
		return
	}
	a.Archived = archived
	if archived {
		c.closeProxyLocked(a)
		c.inbox.set(id, a.Host, nil)
	} else {
		c.openLocked(a)
	}
	if h := c.hosts[a.Host]; h != nil {
		h.wake()
		h.notifyLocked()
	}
}

// ref is an added session's host and its id there.
func (c *Client) ref(id string) (host, remoteID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.added[id]
	if a == nil {
		return "", "", fmt.Errorf("%s isn't a session added from a Remote host", id)
	}
	return a.Host, a.RemoteID, nil
}

// Rename renames an added session on its host, and here.
func (c *Client) Rename(ctx context.Context, id, name string) error {
	host, remoteID, err := c.ref(id)
	if err != nil {
		return err
	}
	var s api.Session
	if err := c.call(ctx, host, api.OpSessionRename, api.Rename{ID: remoteID, Name: name}, &s); err != nil {
		return err
	}
	return c.b.Update(ctx, id, Update{Session: s})
}

// End ends an added session on its host: archived there, or with finish its
// agent stopped and the session kept.
func (c *Client) End(ctx context.Context, id string, finish bool) error {
	host, remoteID, err := c.ref(id)
	if err != nil {
		return err
	}
	return c.call(ctx, host, api.OpSessionEnd, api.End{ID: remoteID, Finish: finish}, nil)
}

// NewSession starts a session on a host, and adds it here.
func (c *Client) NewSession(ctx context.Context, r ipc.RemoteNewRequest) (string, error) {
	var s api.Session
	err := c.call(ctx, r.Host, api.OpSessionNew,
		api.NewSession{Dir: r.Dir, Harness: r.Harness, Name: r.Name, Prompt: r.Prompt, Args: r.Args}, &s)
	if err != nil {
		return "", err
	}
	return c.add(ctx, r.Host, s, r.GroupID)
}

// RecentFolders are the folders of a host's recent sessions.
func (c *Client) RecentFolders(ctx context.Context, host string) (api.FolderList, error) {
	var out api.FolderList
	err := c.call(ctx, host, api.OpFoldersRecent, nil, &out)
	return out, err
}

// ListFolders lists a folder's subfolders on a host.
func (c *Client) ListFolders(ctx context.Context, r ipc.RemoteFolderRequest) (api.FolderList, error) {
	var out api.FolderList
	err := c.call(ctx, r.Host, api.OpFoldersList, api.FolderPath{Path: r.Path, Hidden: r.Hidden}, &out)
	return out, err
}

// MakeFolder makes a folder on a host.
func (c *Client) MakeFolder(ctx context.Context, r ipc.RemoteFolderRequest) (api.Folder, error) {
	var out api.Folder
	err := c.call(ctx, r.Host, api.OpFoldersMkdir, api.FolderPath{Path: r.Path}, &out)
	return out, err
}

// Call runs one of a host's daemon ops (api.OpCall), and answers its data as
// the host sent it.
func (c *Client) Call(ctx context.Context, host, op string, data json.RawMessage) (json.RawMessage, error) {
	if err := c.needs(ctx, host, api.OpCall); err != nil {
		return nil, err
	}
	var out json.RawMessage
	err := c.call(ctx, host, api.OpCall, api.CallRequest{Op: op, Data: data}, &out)
	return out, err
}

// Harnesses are what a host can run, and its default.
func (c *Client) Harnesses(ctx context.Context, host string) (api.Harnesses, error) {
	var out api.Harnesses
	err := c.call(ctx, host, api.OpHarnessesList, nil, &out)
	return out, err
}

// Config is a host's settings: its host ones, not its device ones.
func (c *Client) Config(ctx context.Context, host string) (ipc.ConfigState, error) {
	var out ipc.ConfigState
	err := c.call(ctx, host, api.OpConfigGet, nil, &out)
	return out, err
}

// SetConfig changes a host's settings, all or none.
func (c *Client) SetConfig(ctx context.Context, host string, values map[string]any) (ipc.ConfigState, error) {
	var out ipc.ConfigState
	err := c.call(ctx, host, api.OpConfigSet, ipc.ConfigSet{Values: values}, &out)
	return out, err
}

// Local is a fresh session for s from host, as Backend.Add records it: the
// fields the host says, the rest as for any hosted session.
func Local(host string, s api.Session) *core.Session {
	out := &core.Session{Kind: core.KindHosted, Host: host, RemoteID: s.ID, NameSource: core.NameAuto, CreatedAt: s.Created.UTC()}
	Apply(out, s)
	out.Unseen = s.Unseen
	return out
}

// Apply sets what the host says on a local session added from it: its
// name, folder, harness, agent state and last activity. It's offered again.
func Apply(local *core.Session, s api.Session) {
	local.Name, local.Cwd = s.Name, s.Dir
	if s.Name != "" {
		local.NameSource = core.NameUser // the host's: never replaced here
	}
	if s.Harness != "" {
		local.Harness = s.Harness
	}
	if local.Status != core.StatusArchived {
		local.Status = core.Status(s.Status)
	}
	local.StatusDetail = s.StatusDetail
	if !s.LastActivity.IsZero() {
		local.LastEventAt = s.LastActivity.UTC()
	}
	local.HostGone = false
}
