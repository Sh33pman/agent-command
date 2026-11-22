package client

import (
	"context"
	"encoding/json"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
)

// Is reports whether id is one of a known host's sessions, with a socket
// here.
func (c *Client) Is(id string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.added[id] != nil
}

// Browse connects to a host and lists its own sessions, as it has them now.
func (c *Client) Browse(ctx context.Context, host string) (ipc.RemoteBrowse, error) {
	raw, err := c.Call(ctx, host, ipc.OpSnapshot, nil)
	if err != nil {
		return ipc.RemoteBrowse{}, err
	}
	var snap ipc.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return ipc.RemoteBrowse{}, err
	}
	out := ipc.RemoteBrowse{Host: host, Sessions: []*ipc.Session{}}
	for _, s := range snap.Sessions {
		if own(s) {
			s.Host, s.RemoteID = host, s.ID
			out.Sessions = append(out.Sessions, s)
		}
	}
	return out, nil
}

// NewSession starts a session on a host, in its own folder and group (as
// the host has them), and returns it: it shows here as the host's tree
// follows.
func (c *Client) NewSession(ctx context.Context, r ipc.RemoteNewRequest) (*ipc.Session, error) {
	req := ipc.NewRequest{Cwd: r.Dir, Harness: r.Harness, Name: r.Name, Prompt: r.Prompt, Args: r.Args, GroupID: r.GroupID,
		SetWith: r.SetWith}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	raw, err := c.Call(ctx, r.Host, ipc.OpNew, data)
	if err != nil {
		return nil, err
	}
	var s ipc.Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	s.Host, s.RemoteID = r.Host, s.ID
	return &s, nil
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
	// A request id goes along, so the host runs a retried creating op once.
	err := c.call(ctx, host, api.OpCall, api.CallRequest{Op: op, Data: data, RequestID: ipc.RequestIDOf(ctx)}, &out)
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
func (c *Client) SetConfig(ctx context.Context, host string, set ipc.ConfigSet) (ipc.ConfigState, error) {
	var out ipc.ConfigState
	err := c.call(ctx, host, api.OpConfigSet, set, &out)
	return out, err
}
