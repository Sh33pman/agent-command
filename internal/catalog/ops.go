package catalog

import (
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
)

// Local is the catalog of the daemon's local ops: the unix socket's, and
// later every transport's (D1).
var Local = mustNew(withSessions(withOwnHosts(localOps()))...)

// ownHostOps are this machine's ops on its own Remote hosts.
var ownHostOps = []string{ipc.OpRemoteHosts, ipc.OpRemoteHostAdd, ipc.OpRemoteHostEdit, ipc.OpRemoteHostRemove,
	ipc.OpRemoteHostRetry, ipc.OpRemoteBrowse, ipc.OpRemoteNew, ipc.OpRemoteFoldersRecent, ipc.OpRemoteFoldersList,
	ipc.OpRemoteFoldersMkdir, ipc.OpRemoteHarnesses, ipc.OpRemoteCall, ipc.OpRemoteConfigGet, ipc.OpRemoteConfigSet}

func withOwnHosts(ops []Op) []Op {
	for i := range ops {
		ops[i].OwnHosts = slices.Contains(ownHostOps, ops[i].Name)
	}
	return ops
}

// sessionOps need this machine to run sessions, or what goes with them:
// starting one here, hooks, jobs and the folders they start in, the
// conversations to resume, harnesses' hooks, Hosting, waking, speech and the
// editor. A client-only daemon (Windows) answers not_supported for them. Ops
// on sessions already here (rename, attach, the Inbox) aren't listed: on a
// client-only daemon the only sessions are a host's.
var sessionOps = []string{ipc.OpEvent, ipc.OpNew, ipc.OpConversations,
	ipc.OpJobSave, ipc.OpJobCheck, ipc.OpJobPromptRead, ipc.OpJobPromptWrite, ipc.OpJobDelete, ipc.OpJobPause,
	ipc.OpJobRun, ipc.OpJobRuns, ipc.OpHarnessSettings, ipc.OpWakeSet,
	ipc.OpFoldersRecent, ipc.OpFoldersList, ipc.OpFoldersMkdir,
	ipc.OpHarnessAdd, ipc.OpHarnessUpdate, ipc.OpHarnessRemove,
	ipc.OpRemoteStatus, ipc.OpRemoteHosting, ipc.OpRemoteReload, ipc.OpRemoteClients, ipc.OpRemoteRequests,
	ipc.OpRemoteApprove, ipc.OpRemoteDeny, ipc.OpRemoteAuthorize, ipc.OpRemoteRevoke, ipc.OpRemoteDisconnect,
	ipc.OpRemoteRename, ipc.OpRemoteScope,
	ipc.OpSpeechSay, ipc.OpSpeechStop, ipc.OpSpeechStatus, ipc.OpSpeechInstall,
	ipc.OpEditorOpen}

func withSessions(ops []Op) []Op {
	for i := range ops {
		if slices.Contains(sessionOps, ops[i].Name) {
			ops[i].NeedsSessions = true
		}
	}
	return ops
}

// LocalProto is the range of protocol versions the local socket speaks.
var LocalProto = Range{Min: ipc.ProtoMin, Max: ipc.Proto}

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
func (o Op) stream() Op      { o.Stream = true; return o }
func (o Op) retryable() Op   { o.Retryable = true; return o }

func localOps() []Op {
	const (
		none     = GrantNone
		host     = GrantHost
		entities = GrantEntities
	)
	return []Op{
		// The connection.
		read(ipc.OpHello, "Exchanges protocol and binary versions. Sent first on every connection, in both directions.", none, ipc.Hello{}, ipc.Hello{}),
		read(ipc.OpPing, "Checks that the daemon is alive.", none, nil, nil),
		admin(ipc.OpShutdown, "Asks the daemon to exit, so a newer acmd can replace one left over from before an upgrade.", host, nil, nil).hostOnly(),
		read(ipc.OpStatus, "Returns counts of sessions and connections, for doctor and tests.", host, nil, ipc.Status{}),
		act(ipc.OpEvent, "Delivers an agent's hook event from `acmd hook`.", host, ipc.HookEvent{}, nil).hostOnly(),

		// State.
		read(ipc.OpSnapshot, "Returns the sessions, groups, view state, notices, Inbox, jobs, wake state, Remote state, settings, health, features and updates. A client id says which warnings it dismissed.",
			entities, ipc.SnapshotRequest{}, ipc.Snapshot{}),
		read(ipc.OpSubscribe, "Streams the daemon's state: a snapshot, then each entity added, updated (whole) or removed, with a revision. Resumes from a revision, or sends a new snapshot when too far behind.", entities, ipc.SubscribeRequest{}, ipc.Event{}).stream(),
		read(ipc.OpWatch, "Waits until something changes, or a timeout passes. A view's heartbeat while it's open.", entities, ipc.WatchRequest{}, ipc.WatchResponse{}),
		read(ipc.OpLayout, "Returns a client's saved pane layout.", host, ipc.Layout{}, ipc.Layout{}),
		act(ipc.OpSetLayout, "Saves a client's pane layout.", host, ipc.Layout{}, nil),

		// Sessions.
		act(ipc.OpNew, "Starts a session in the background, in a folder, with a harness.", entities, ipc.NewRequest{}, ipc.Session{}).runsCode().retryable(),
		act(ipc.OpResume, "Resumes a paused background session.", entities, ipc.Mutation{}, ipc.Session{}).runsCode(),
		read(ipc.OpConversations, "Lists earlier Claude and Antigravity conversations that can be resumed.", host, ipc.Page{}, ipc.ConversationPage{}),
		// rename, archive, unarchive, move, purge, remote and remove_added take
		// many ids (IDs), and return a result for each.
		mut(act(ipc.MutRename, "Renames sessions: the name for one, numbered (\"api - 1\", \"api - 2\") for several.", entities, nil, ipc.Results{})),
		mut(act(ipc.MutSeen, "Marks a session as seen: you've looked at it.", entities, nil, nil)),
		mut(act(ipc.MutDescribe, "Sets a session's description.", entities, nil, nil)),
		mut(act(ipc.MutArchive, "Archives sessions, stopping their agents.", entities, nil, ipc.Results{})),
		mut(act(ipc.MutFinish, "Stops a session's agent and marks it finished.", entities, nil, nil)),
		mut(act(ipc.MutUnarchive, "Unarchives sessions.", entities, nil, ipc.Results{})),
		mut(act(ipc.MutMove, "Moves sessions to a group.", entities, nil, ipc.Results{})),
		mut(Op{Name: ipc.MutPurge, Description: "Deletes archived sessions for good, with their conversations.",
			Scope: ScopeAdmin, Kind: KindDestructive, Grant: entities, Output: ipc.Results{}}),
		mut(admin(ipc.MutRemote, "Turns sessions' Remote switch on or off: whether clients of Hosting may see and use them.", entities, nil, ipc.Results{})),

		// Groups.
		mut(act(ipc.MutGroupCreate, "Creates a group, with a name and a color.", entities, nil, ipc.Group{})).retryable(),
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
		read(ipc.OpInbox, "Lists Inbox items, newest first: one session's, or everyone's.", entities, ipc.InboxRequest{}, ipc.InboxPage{}),
		act(ipc.OpInboxAnswer, "Answers an Inbox item by typing into its session.", entities, ipc.InboxAnswer{}, nil).runsCode(),
		mut(act(ipc.MutInboxRead, "Marks Inbox items read or unread.", entities, nil, nil)),
		mut(act(ipc.MutInboxDismiss, "Takes Inbox items out of the queue.", entities, nil, nil)),

		// Scheduled jobs. Jobs run agents on the host, so they need the whole
		// host, never a share.
		act(ipc.OpJobSave, "Adds or edits a scheduled job.", host, core.Job{}, core.Job{}).runsCode().retryable(),
		read(ipc.OpJobCheck, "Checks a job without saving it: its schedule in words and its next runs.", host, ipc.JobCheckRequest{}, ipc.JobCheck{}),
		act(ipc.OpJobPromptRead, "Reads a job's own prompt file, by job id, or a draft's Markdown or text file.", host, ipc.PromptFileRef{}, ipc.PromptFile{}),
		act(ipc.OpJobPromptWrite, "Writes a job's own prompt file, by job id, or a draft's Markdown or text file.", host, ipc.PromptFileRef{}, ipc.PromptFile{}),
		read(ipc.OpFoldersRecent, "Lists the folders of recent sessions here.", host, nil, api.FolderList{}),
		read(ipc.OpFoldersList, "Lists a folder's subfolders here, and with files its Markdown and text files.", host, api.FolderPath{}, api.FolderList{}),
		act(ipc.OpFoldersMkdir, "Makes a folder here.", host, api.FolderPath{}, api.Folder{}),
		act(ipc.OpJobDelete, "Deletes a scheduled job.", host, ipc.JobRef{}, nil).destructive(),
		act(ipc.OpJobPause, "Pauses or resumes a scheduled job.", host, ipc.JobRef{}, nil),
		act(ipc.OpJobRun, "Runs a scheduled job now.", host, ipc.JobRef{}, core.JobRun{}).runsCode().retryable(),
		read(ipc.OpJobRuns, "Lists a job's runs, newest first.", host, ipc.JobRunsRequest{}, ipc.JobRunPage{}),
		read(ipc.OpHarnessSettings, "Lists a harness's settings for jobs.", host, ipc.HarnessRef{}, []harness.Setting{}),
		admin(ipc.OpWakeSet, "Turns waking the Mac for jobs on or off, checking the sudoers rule again.", host, ipc.WakeRef{}, ipc.WakeState{}).hostOnly(),

		// Hosting: this machine's listener for other acmds.
		read(ipc.OpRemoteStatus, "Returns Hosting's state: whether it's on, where it listens, and why not.", host, nil, ipc.RemoteStatus{}),
		admin(ipc.OpRemoteHosting, "Turns Hosting on or off.", host, ipc.RemoteRef{}, ipc.RemoteStatus{}),
		admin(ipc.OpRemoteReload, "Reads config.toml again now, and applies the Hosting settings.", host, nil, ipc.RemoteStatus{}),
		{Name: ipc.OpRemoteClients, Description: "Lists the authorized clients and their connections.",
			Scope: ScopeAdmin, Kind: KindRead, Grant: host, Output: []ipc.RemoteClient{}},
		{Name: ipc.OpRemoteRequests, Description: "Lists requests from unknown clients waiting for approval.",
			Scope: ScopeAdmin, Kind: KindRead, Grant: host, Output: []ipc.RemoteRequest{}},
		admin(ipc.OpRemoteApprove, "Authorizes a pending request's client.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteDeny, "Drops a pending request.", host, ipc.RemoteRef{}, nil),
		admin(ipc.OpRemoteAuthorize, "Authorizes a client by its fingerprint, or by its cert file.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteRevoke, "Un-authorizes a client and drops its connections.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteDisconnect, "Drops a client's connections, or one connection.", host, ipc.RemoteRef{}, ipc.RemoteDisconnected{}),
		admin(ipc.OpRemoteRename, "Renames an authorized client. It stays authorized and connected.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		admin(ipc.OpRemoteScope, "Changes what an authorized client may do here: read, act or admin.", host, ipc.RemoteRef{}, ipc.RemoteClient{}),
		{Name: ipc.OpRemoteIdentity, Description: "Returns this machine's identity: as a host, and as a client when that's another one.",
			Scope: ScopeAdmin, Kind: KindRead, Grant: host, Output: ipc.RemoteIdentity{}},
		admin(ipc.OpRemoteRegenerate, "Makes a new identity and uses it. Every peer that trusts this machine must trust the new fingerprint.",
			host, nil, ipc.RemoteIdentity{}).destructive().hostOnly(),
		admin(ipc.OpRemoteCert, "Uses a cert and key of your own for a role, once they're checked to belong together, or acmd's identity again.",
			host, ipc.RemoteCert{}, ipc.RemoteIdentity{}).hostOnly(),

		// Remote hosts: the other acmds this machine uses sessions on.
		read(ipc.OpRemoteHosts, "Lists the known hosts and their connections.", host, nil, []ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostAdd, "Adds a known host.", host, ipc.RemoteHostEdit{}, ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostEdit, "Changes a known host.", host, ipc.RemoteHostEdit{}, ipc.RemoteHost{}),
		admin(ipc.OpRemoteHostRemove, "Forgets a known host, and the sessions added from it here.", host, ipc.RemoteHostRef{}, nil).destructive(),
		act(ipc.OpRemoteHostRetry, "Connects to a known host now, without waiting out the backoff.", host, ipc.RemoteHostRef{}, ipc.RemoteHost{}),
		read(ipc.OpRemoteBrowse, "Connects to a host and lists its own sessions, as it has them now.", host, ipc.RemoteHostRef{}, ipc.RemoteBrowse{}),
		act(ipc.OpRemoteNew, "Starts a session on a host, in its folder and group there: it shows in the host's tree.", host, ipc.RemoteNewRequest{}, ipc.Session{}).runsCode().retryable(),
		read(ipc.OpRemoteFoldersRecent, "Lists a host's recent folders.", host, ipc.RemoteHostRef{}, api.FolderList{}),
		read(ipc.OpRemoteFoldersList, "Lists a folder on a host.", host, ipc.RemoteFolderRequest{}, api.FolderList{}),
		act(ipc.OpRemoteFoldersMkdir, "Makes a folder on a host.", host, ipc.RemoteFolderRequest{}, api.Folder{}),
		read(ipc.OpRemoteHarnesses, "Lists the harnesses a host can start.", host, ipc.RemoteHostRef{}, api.Harnesses{}),
		act(ipc.OpRemoteCall, "Runs one of a host's ops on it, as acmd --host does. The host decides which ops a paired device may run: not admin ones, nor ones for its own machine only.", host, ipc.RemoteCall{}, nil).runsCode(),

		read(ipc.OpClients, "Lists the clients that use this daemon: each one's kind, device, what it can do, and how it's used now.", host, nil, []ipc.Client{}),
		act(ipc.OpClientPresence, "Says how the calling client is used now (active, idle, background) and the session it shows. The client must name itself in its hello.", host, ipc.ClientPresence{}, nil),
		read(ipc.OpClientProfile, "Reads the calling client's notification profile: which notices it shows, and what opens its popup.", host, nil, ipc.ClientProfile{}),
		act(ipc.OpClientProfileSet, "Changes the calling client's notification profile.", host, ipc.ClientProfile{}, ipc.ClientProfile{}),
		act(ipc.OpKeysSet, "Registers the calling client's keyboard shortcuts, so acmd keys can list them.", host, map[string]any{}, nil),
		read(ipc.OpKeys, "Reads a client's keyboard shortcuts: the one named, or the desktop app's seen most recently.", host, ipc.KeysRequest{}, ipc.Keymap{}),
		read(ipc.OpNoticeGet, "Reads a recent notice by its id.", entities, ipc.NoticeRef{}, ipc.Notice{}),
		read(ipc.OpHarnesses, "Lists the harnesses, each with its kind, an icon key and whether it's installed, and the default.", host, nil, ipc.Harnesses{}),
		admin(ipc.OpHarnessAdd, "Adds a harness: a kind, a name (letters and single spaces, unique ignoring case), and its settings.", host, ipc.HarnessSave{}, ipc.Harness{}).runsCode(),
		admin(ipc.OpHarnessUpdate, "Renames a harness or changes its settings; its sessions and jobs stay with it.", host, ipc.HarnessSave{}, ipc.Harness{}).runsCode(),
		admin(ipc.OpHarnessRemove, "Removes a harness, leaving its folder. Refused while a scheduled job uses it.", host, ipc.HarnessRef{}, nil).destructive(),

		act(ipc.OpSessionAttach, "Attaches to a session's terminal: its output, then input and resizes, in a mode (stream, the only one now).",
			entities, nil, nil).stream().runsCode(),
		read(ipc.OpSessionScreen, "Returns a session's screen and recent history as plain text: all of it, or its last lines. Sessions on this machine only, for now.",
			entities, ipc.ScreenRequest{}, ipc.Screen{}),

		// Speech: device ops, on this machine only.
		act(ipc.OpSpeechSay, "Reads text aloud, and answers when it's done or stopped.", host, ipc.SpeechSay{}, nil).hostOnly(),
		act(ipc.OpSpeechStop, "Stops reading aloud, whatever started it.", host, nil, nil).hostOnly(),
		read(ipc.OpSpeechStatus, "Says how speech stands here: there, on, set up, setting up, speaking.", host, nil, ipc.SpeechState{}).hostOnly(),
		act(ipc.OpSpeechInstall, "Sets speech up for the voice, in the background.", host, nil, ipc.SpeechState{}).hostOnly(),

		// Health: what's wrong with the host, and putting it right.
		act(ipc.OpHealthDismiss, "Stops showing a warning to one client.", host, ipc.HealthRef{}, []string{}),
		act(ipc.OpHealthRestore, "Shows a client every warning it dismissed again.", host, ipc.HealthRef{}, []string{}),
		read(ipc.OpDoctor, "Returns the checks acmd doctor makes: acmd itself, the agents and their hooks, the editor, the background service.",
			host, nil, ipc.DoctorReport{}),
		act(ipc.OpEditorOpen, "Opens a session's folder, or a file at a line, in the editor chosen in Settings, on this machine.",
			host, ipc.EditorOpen{}, nil).hostOnly().runsCode(),
		admin(ipc.OpInit, "Sets acmd up as acmd init does: the agents' hooks, the settings, and the login shell's environment.",
			host, nil, ipc.InitResult{}).runsCode(),
		read(ipc.OpReportDetails, "Returns what a report adds about the host: its version, OS, and for a bug the end of its log, cleaned of secrets.",
			host, ipc.ReportRequest{}, ipc.ReportDetails{}),
		read(ipc.OpReportPage, "Makes the GitHub page for a report from its title and body, cleaned of secrets.",
			host, ipc.ReportRequest{}, ipc.ReportPage{}),

		// Settings: config.toml, which the daemon alone writes.
		read(ipc.OpConfigGet, "Describes every setting (type, default, allowed values, host or device, whether it runs code) and returns the values in effect.",
			host, nil, ipc.ConfigState{}),
		// Changing a setting that runs code needs admin; the rest need act.
		// Until scopes are checked per key (4.3), the op asks for admin.
		admin(ipc.OpConfigSet, "Changes settings by key, all or none, keeping config.toml's comments. Refused while config.toml has a mistake.",
			host, ipc.ConfigSet{}, ipc.ConfigState{}).runsCode(),
		read(ipc.OpRemoteConfigGet, "Returns a host's settings: its host ones, not its device ones.", host, ipc.RemoteHostRef{}, ipc.ConfigState{}),
		admin(ipc.OpRemoteConfigSet, "Changes a host's settings, all or none. Its device settings can't be changed from here.",
			host, ipc.RemoteConfigSet{}, ipc.ConfigState{}).runsCode(),
	}
}
