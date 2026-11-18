package ipc

import (
	"time"

	"github.com/brettmostert/agent-command/internal/remote/api"
)

// Remote hosts: the other acmds this machine uses Remote sessions on, as a
// client. The daemon connects to them; the apps and the CLI use these ops,
// and attach to an added session's socket as to a local one.
const (
	OpRemoteHosts      = "remote.hosts"        // the known hosts and their connections → []RemoteHost
	OpRemoteHostAdd    = "remote.hosts.add"    // adds a known host to config.toml (RemoteHostEdit) → RemoteHost
	OpRemoteHostEdit   = "remote.hosts.edit"   // changes one (RemoteHostEdit: Host names it) → RemoteHost
	OpRemoteHostRemove = "remote.hosts.remove" // forgets one, and the sessions added from it here (RemoteHostRef.Host)
	OpRemoteHostRetry  = "remote.hosts.retry"  // connects now, without waiting out the backoff (RemoteHostRef.Host) → RemoteHost
	// OpRemoteBrowse lists a host's Remote sessions, and which are added
	// here (RemoteHostRef.Host → RemoteBrowse).
	OpRemoteBrowse = "remote.browse"
	// OpRemoteSessionAdd adds a host's session here (RemoteHostRef.Host, and
	// ID the host's id) → *core.Session, the local session. Adding one that's
	// already added returns it.
	OpRemoteSessionAdd = "remote.sessions.add"
	// OpRemoteSessionRemove removes an added session here, whatever its state
	// (RemoteHostRef.ID, the local id). The session on the host isn't touched.
	OpRemoteSessionRemove = "remote.sessions.remove"
	// OpRemoteNew starts a session on a host and adds it here
	// (RemoteNewRequest → *core.Session, the local session).
	OpRemoteNew = "remote.new"
	// The host's folders and harnesses, for the Where picker.
	OpRemoteFoldersRecent = "remote.folders.recent" // RemoteHostRef.Host → api.FolderList
	OpRemoteFoldersList   = "remote.folders.list"   // RemoteFolderRequest → api.FolderList
	OpRemoteFoldersMkdir  = "remote.folders.mkdir"  // RemoteFolderRequest → api.Folder
	OpRemoteHarnesses     = "remote.harnesses"      // RemoteHostRef.Host → api.Harnesses
)

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

// RemoteBrowse is a host's Remote sessions.
type RemoteBrowse struct {
	Host     string                `json:"host"`
	Sessions []RemoteBrowseSession `json:"sessions"`
}

// RemoteBrowseSession is one of a host's Remote sessions; LocalID is the
// session here when it's added.
type RemoteBrowseSession struct {
	api.Session
	Added   bool   `json:"added"`
	LocalID string `json:"local_id,omitempty"`
}

// RemoteNewRequest starts a session on a host (api.NewSession), and puts the
// local session in GroupID ("" = Default).
type RemoteNewRequest struct {
	Host    string   `json:"host"`
	Dir     string   `json:"dir"`
	Harness string   `json:"harness,omitempty"`
	Name    string   `json:"name,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Args    []string `json:"args,omitempty"`
	GroupID string   `json:"group_id,omitempty"`
}

// RemoteFolderRequest is a folder on a host (api.FolderPath).
type RemoteFolderRequest struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden,omitempty"`
}
