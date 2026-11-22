package ipc

import (
	"encoding/json"
	"time"
)

// Remote hosts: the other acmds this machine uses Remote sessions on, as a
// client. The daemon connects to them; the apps and the CLI use these ops,
// and attach to an added session's socket as to a local one.
const (
	OpRemoteHosts      = "remote.hosts"        // the known hosts and their connections → []RemoteHost
	OpRemoteHostAdd    = "remote.hosts.add"    // adds a known host to config.toml (RemoteHostEdit) → RemoteHost
	OpRemoteHostEdit   = "remote.hosts.edit"   // changes one (RemoteHostEdit: Host names it) → RemoteHost
	OpRemoteHostRemove = "remote.hosts.remove" // forgets one; its sessions go from here (RemoteHostRef.Host)
	OpRemoteHostRetry  = "remote.hosts.retry"  // connects now, without waiting out the backoff (RemoteHostRef.Host) → RemoteHost
	// OpRemoteBrowse connects to a host and lists its own sessions, as it has
	// them now (RemoteHostRef.Host → RemoteBrowse).
	OpRemoteBrowse = "remote.browse"
	// OpRemoteNew starts a session on a host, in its folder and group there
	// (RemoteNewRequest → Session, the host's: it shows in the host's tree).
	OpRemoteNew = "remote.new"
	// The host's folders and harnesses, for the Where picker.
	OpRemoteFoldersRecent = "remote.folders.recent" // RemoteHostRef.Host → api.FolderList
	OpRemoteFoldersList   = "remote.folders.list"   // RemoteFolderRequest → api.FolderList
	OpRemoteFoldersMkdir  = "remote.folders.mkdir"  // RemoteFolderRequest → api.Folder
	OpRemoteHarnesses     = "remote.harnesses"      // RemoteHostRef.Host → api.Harnesses
	// OpRemoteCall runs one of a host's daemon ops on it (RemoteCall → the
	// op's answer): what acmd --host does. The host decides which ops a paired
	// device may run (internal/remote/api's OpCall).
	OpRemoteCall = "remote.call"
)

// RemoteCall is an op to run on a host: its name and argument as the host's
// socket takes them (a change is Op "mutate" with the Mutation as Data).
type RemoteCall struct {
	Host string          `json:"host"`
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Remote host states (RemoteHost.State).
const (
	HostIdle         = "idle"         // nothing needs it: no added sessions, and nobody is browsing it
	HostConnecting   = "connecting"   // dialing, or the TLS handshake and hello
	HostConnected    = "connected"    // in use
	HostUnreachable  = "unreachable"  // no answer, or the connection dropped: retrying
	HostPending      = "pending"      // waiting for the host's user to approve this client (OurFingerprint)
	HostUnauthorized = "unauthorized" // the host refused this client: retrying slowly
	HostVersion      = "version"      // the two speak different protocols: retrying slowly
	// HostFingerprint is a host whose fingerprint isn't the pinned one. It's
	// never retried until the host's entry changes.
	HostFingerprint = "fingerprint"
	HostError       = "error" // something here: the client identity, or the host's cert file
)

// RemoteHost is a known host ([[remote.hosts]]) and its connection.
type RemoteHost struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`    // pinned
	Cert        string `json:"cert,omitempty"` // the cert file it was pinned from
	State       string `json:"state"`          // Host*
	// Error says what went wrong, for every state but idle, connecting and
	// connected.
	Error string `json:"error,omitempty"`
	// Since is when it got to State, and RetryAt when it tries again.
	Since   time.Time `json:"since,omitzero"`
	RetryAt time.Time `json:"retry_at,omitzero"`
	// OurFingerprint is this client's: what the host's user approves.
	OurFingerprint string `json:"our_fingerprint,omitempty"`
	// Seen is the host's fingerprint as it showed it, when it isn't the
	// pinned one (HostFingerprint).
	Seen string `json:"seen,omitempty"`
	// What the host said in its hello, once connected.
	HostName    string `json:"host_name,omitempty"`
	HostVersion string `json:"host_version,omitempty"`
	Sessions    int    `json:"sessions"` // added here, archived ones aside
	// InConfig is a host config.toml lists: it's changed by editing the
	// file, not by ops.
	InConfig bool `json:"in_config,omitempty"`
}

// RemoteHostEdit adds or edits a known host. For an add, Name and Address
// are needed, and Fingerprint or Cert (a cert file, whose fingerprint is
// pinned). For an edit, Host names the entry, and fields left empty stay as
// they are.
type RemoteHostEdit struct {
	Host        string `json:"host,omitempty"`
	Name        string `json:"name,omitempty"`
	Address     string `json:"address,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Cert        string `json:"cert,omitempty"`
}

// RemoteHostRef names a known host, and for some ops a session: the host's
// id (remote.sessions.add) or the local one (remote.sessions.remove).
type RemoteHostRef struct {
	Host string `json:"host,omitempty"`
	ID   string `json:"id,omitempty"`
}

// RemoteBrowse is a host's own sessions, each with its host.
type RemoteBrowse struct {
	Host     string     `json:"host"`
	Sessions []*Session `json:"sessions"`
}

// RemoteNewRequest starts a session on a host: in Dir there ("~" and
// relative paths from its home), in GroupID, one of the host's groups ("" =
// its Default), or in a set with SetWith, one of its sessions.
type RemoteNewRequest struct {
	Host    string   `json:"host"`
	Dir     string   `json:"dir"`
	Harness string   `json:"harness,omitempty"`
	Name    string   `json:"name,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Args    []string `json:"args,omitempty"`
	GroupID string   `json:"group_id,omitempty"`
	// SetWith puts it in a set with a session added from the same host, as
	// NewRequest.SetWith.
	SetWith string `json:"set_with,omitempty"`
}

// RemoteFolderRequest is a folder on a host (api.FolderPath).
type RemoteFolderRequest struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}
