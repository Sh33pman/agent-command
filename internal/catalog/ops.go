package catalog

import (
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
)

// Local is the catalog of the daemon's local ops: the unix socket's, and
// later every transport's (D1).
var Local = mustNew(localOps()...)

// LocalProto is the range of protocol versions the local socket speaks.
var LocalProto = Range{Min: ipc.Proto, Max: ipc.Proto}

func mustNew(ops ...Op) *Catalog {
	c, err := New(ops...)
	if err != nil {
		panic(err)
	}
	return c
}

// Shorthands, so each entry below reads as one line of scope, kind and grant.
func read(name, desc string, grant Grant, in, out any) Op {
	return Op{Name: name, Description: desc, Input: in, Output: out, Scope: ScopeRead, Kind: KindRead, Grant: grant}
}

func act(name, desc string, grant Grant, in, out any) Op {
	return Op{Name: name, Description: desc, Input: in, Output: out, Scope: ScopeAct, Kind: KindChange, Grant: grant}
}

func admin(name, desc string, grant Grant, in, out any) Op {
	return Op{Name: name, Description: desc, Input: in, Output: out, Scope: ScopeAdmin, Kind: KindChange, Grant: grant}
}

// mut is a change protocol 1 sends through mutate.
func mut(o Op) Op {
	o.Mutation = true
	if o.Input == nil {
		o.Input = ipc.Mutation{}
	}
	return o
}

func (o Op) destructive() Op { o.Kind = KindDestructive; return o }
func (o Op) hostOnly() Op    { o.HostOnly = true; return o }
func (o Op) runsCode() Op    { o.RunsCode = true; return o }

func localOps() []Op {
	const (
		none     = GrantNone
		host     = GrantHost
		entities = GrantEntities
	)
	var count int
	return []Op{
		// The connection.
		read(ipc.OpHello, "Exchanges protocol and binary versions. Sent first on every connection, in both directions.", none, ipc.Hello{}, ipc.Hello{}),
		read(ipc.OpPing, "Checks that the daemon is alive.", none, nil, nil),
		admin(ipc.OpShutdown, "Asks the daemon to exit, so a newer acmd can replace one left over from before an upgrade.", host, nil, nil).hostOnly(),
		read(ipc.OpStatus, "Returns counts of sessions and connections, for doctor and tests.", host, nil, ipc.Status{}),
		act(ipc.OpEvent, "Delivers an agent's hook event from `acmd hook`.", host, ipc.HookEvent{}, nil).hostOnly(),

		// State.
		read(ipc.OpSnapshot, "Returns the sessions, groups, view state, notices, Inbox, jobs, wake state and Remote state.", entities, nil, ipc.Snapshot{}),
		read(ipc.OpWatch, "Waits until something changes, or a timeout passes. A view's heartbeat while it's open.", entities, ipc.WatchRequest{}, ipc.WatchResponse{}),
		read(ipc.OpLayout, "Returns a client's saved pane layout.", host, ipc.Layout{}, ipc.Layout{}),
		act(ipc.OpSetLayout, "Saves a client's pane layout.", host, ipc.Layout{}, nil),

		// Sessions.
		act(ipc.OpNew, "Starts a session in the background, in a folder, with a harness.", entities, ipc.NewRequest{}, core.Session{}).runsCode(),
		act(ipc.OpResume, "Resumes a paused background session.", entities, ipc.Mutation{}, core.Session{}).runsCode(),
		read(ipc.OpConversations, "Lists earlier Claude and Antigravity conversations that can be resumed.", host, nil, []ipc.Conversation{}),
		mut(act(ipc.MutRename, "Renames a session.", entities, nil, nil)),
		mut(act(ipc.MutSeen, "Marks a session as seen: you've looked at it.", entities, nil, nil)),
		mut(act(ipc.MutDescribe, "Sets a session's description.", entities, nil, nil)),
		mut(act(ipc.MutArchive, "Archives a session, stopping its agent.", entities, nil, nil)),
		mut(act(ipc.MutFinish, "Stops a session's agent and marks it finished.", entities, nil, nil)),
		mut(act(ipc.MutUnarchive, "Unarchives a session.", entities, nil, nil)),
		mut(act(ipc.MutMove, "Moves sessions to a group.", entities, nil, nil)),
		mut(Op{Name: ipc.MutPurge, Description: "Deletes archived sessions for good, with their conversations. Returns how many.",
			Scope: ScopeAdmin, Kind: KindDestructive, Grant: entities, Output: count}),
		mut(admin(ipc.MutRemote, "Turns a session's Remote switch on or off: whether clients of Hosting may see and use it.", entities, nil, nil)),

		// Groups.
		mut(act(ipc.MutGroupCreate, "Creates a group, with a name and a color.", entities, nil, core.Group{})),
		mut(act(ipc.MutGroupRename, "Renames a group.", entities, nil, nil)),
		mut(act(ipc.MutGroupColor, "Sets a group's color.", entities, nil, nil)),
		mut(act(ipc.MutGroupDelete, "Deletes a group. Its sessions move to the Default group.", entities, nil, nil).destructive()),
		mut(act(ipc.MutGroupArchive, "Archives a group and its sessions.", entities, nil, nil)),
		mut(act(ipc.MutGroupUnarchive, "Unarchives a group, and optionally its sessions.", entities, nil, nil)),
		mut(act(ipc.MutGroupCollapse, "Collapses or expands a group, or every group.", entities, nil, nil)),
		mut(act(ipc.MutGroupMove, "Moves a group to just after or just before another one.", entities, nil, nil)),

		// Sets: sessions made together by splitting panes.
		mut(act(ipc.MutSetCreate, "Makes a set of sessions, in order. They move to the first one's group.", entities, nil, ipc.Set{})),
		mut(act(ipc.MutSetAdd, "Adds a session to a set, after one of its members or at the end. It moves to the set's group.", entities, nil, nil)),
		mut(act(ipc.MutSetRemove, "Takes a session out of its set. The set ends if one session is left.", entities, nil, nil)),
		mut(act(ipc.MutSetEnd, "Ends a set. Its sessions stay where they are, on their own.", entities, nil, nil)),

		// The CLI's view.
		mut(act(ipc.MutViewMode, "Shows sessions in groups or as one list.", host, nil, nil)),
		mut(act(ipc.MutViewSort, "Sets the sort key and direction.", host, nil, nil)),
		mut(act(ipc.MutViewFocus, "Shows only one group, or every group again.", host, nil, nil)),
		mut(act(ipc.MutViewFilter, "Sets the text filter.", host, nil, nil)),
		mut(act(ipc.MutViewArchived, "Shows or hides archived sessions.", host, nil, nil)),

		// Inbox.
		read(ipc.OpInbox, "Lists Inbox items, newest first: one session's, or everyone's.", entities, ipc.InboxRequest{}, []core.InboxItem{}),
		act(ipc.OpInboxAnswer, "Answers an Inbox item by typing into its session.", entities, ipc.InboxAnswer{}, nil).runsCode(),
		mut(act(ipc.MutInboxRead, "Marks Inbox items read or unread.", entities, nil, nil)),
		mut(act(ipc.MutInboxDismiss, "Takes Inbox items out of the queue.", entities, nil, nil)),

		// Scheduled jobs. Jobs run agents on the host, so they need the whole
		// host, never a share.
		act(ipc.OpJobSave, "Adds or edits a scheduled job.", host, core.Job{}, core.Job{}).runsCode(),
		read(ipc.OpJobCheck, "Checks a job without saving it: its schedule in words and its next runs.", host, core.Job{}, ipc.JobCheck{}),
		act(ipc.OpJobDelete, "Deletes a scheduled job.", host, ipc.JobRef{}, nil).destructive(),
		act(ipc.OpJobPause, "Pauses or resumes a scheduled job.", host, ipc.JobRef{}, nil),
		act(ipc.OpJobRun, "Runs a scheduled job now.", host, ipc.JobRef{}, core.JobRun{}).runsCode(),
		read(ipc.OpJobRuns, "Lists a job's runs, newest first.", host, ipc.JobRef{}, []core.JobRun{}),
		read(ipc.OpAgentSettings, "Lists an agent's settings for jobs.", host, ipc.JobRef{}, []harness.Setting{}),
		admin(ipc.OpWakeSet, "Turns waking the Mac for jobs on or off, checking the sudoers rule again.", host, ipc.WakeRef{}, ipc.WakeState{}).hostOnly(),

		// Hosting: this machine's listener for other acmds.
		read(ipc.OpRemoteStatus, "Returns Hosting's state: whether it's on, where it listens, and why not.", host, nil, ipc.RemoteStatus{}),
		admin(ipc.OpRemoteHosting, "Turns Hosting on or off.", host, ipc.RemoteRef{}, ipc.RemoteStatus{}),
		admin(ipc.OpRemoteReload, "Reads the Hosting settings again after editing config.toml.", host, nil, ipc.RemoteStatus{}),
		{Name: ipc.OpRemoteClients, Description: "Lists the authorized clients and their connections.",
			Scope: ScopeAdmin, Kind: KindRead, Grant: host, Output: []ipc.RemoteClient{}},
		{Name: ipc.OpRemoteRequests, Description: "Lists requests from unknown clients waiting for approval.",
			Scope: ScopeAdmin, Kind: KindRead, Grant: host, Output: []ipc.RemoteRequest{}},
		admin(ipc.OpRemoteApprove, "Authorizes a pending request's client.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteDeny, "Drops a pending request.", host, ipc.RemoteRef{}, nil),
		admin(ipc.OpRemoteAuthorize, "Authorizes a client by its fingerprint.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteRevoke, "Un-authorizes a client and drops its connections.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteDisconnect, "Drops a client's connections, or one connection.", host, ipc.RemoteRef{}, ipc.RemoteDisconnected{}),

		// Remote hosts: the other acmds this machine uses sessions on.
		read(ipc.OpRemoteHosts, "Lists the known hosts and their connections.", host, nil, []ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostAdd, "Adds a known host.", host, ipc.RemoteHostEdit{}, ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostEdit, "Changes a known host.", host, ipc.RemoteHostEdit{}, ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostRemove, "Forgets a known host, and the sessions added from it here.", host, ipc.RemoteHostRef{}, nil).destructive(),
		act(ipc.OpRemoteHostRetry, "Connects to a known host now, without waiting out the backoff.", host, ipc.RemoteHostRef{}, ipc.RemoteHost{}),
		read(ipc.OpRemoteBrowse, "Lists a host's Remote sessions, and which are added here.", host, ipc.RemoteHostRef{}, ipc.RemoteBrowse{}),
		act(ipc.OpRemoteSessionAdd, "Adds a host's session here.", host, ipc.RemoteHostRef{}, core.Session{}),
		act(ipc.OpRemoteSessionRemove, "Removes a session added from a host. The session on the host isn't touched.", entities, ipc.RemoteHostRef{}, nil),
		act(ipc.OpRemoteNew, "Starts a session on a host and adds it here.", host, ipc.RemoteNewRequest{}, core.Session{}).runsCode(),
		read(ipc.OpRemoteFoldersRecent, "Lists a host's recent folders.", host, ipc.RemoteHostRef{}, api.FolderList{}),
		read(ipc.OpRemoteFoldersList, "Lists a folder on a host.", host, ipc.RemoteFolderRequest{}, api.FolderList{}),
		act(ipc.OpRemoteFoldersMkdir, "Makes a folder on a host.", host, ipc.RemoteFolderRequest{}, api.Folder{}),
		read(ipc.OpRemoteHarnesses, "Lists the harnesses a host can start.", host, ipc.RemoteHostRef{}, api.Harnesses{}),
	}
}
