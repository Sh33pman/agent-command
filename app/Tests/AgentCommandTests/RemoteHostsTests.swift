import Foundation
import Testing

@testable import AgentCommand

private func decode<T: Decodable>(_ json: String) throws -> T {
    try JSONDecoder().decode(T.self, from: Data(json.utf8))
}

private let fp = "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde"
private let seenFP = "SHA256:ZzZzEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde"

/// A session as the daemon lists it, here or added from a host.
func makeRemoteSession(
    id: String, host: String = "", gone: Bool = false, remote: Bool = false, status: String = "done"
) throws -> Session {
    var fields: [String: Any] = [
        "ID": id, "Name": id, "Cwd": "/w/\(id)", "Status": status, "Kind": "hosted", "Unseen": false,
        "Harness": "claude", "ClaudeSessionID": "", "Remote": remote,
    ]
    if !host.isEmpty { fields["Host"] = host; fields["RemoteID"] = "r-\(id)"; fields["HostGone"] = gone }
    if status == "waiting" { fields["attention"] = ["rank": 1, "reason": "waiting", "since": "2026-11-20T09:00:00Z"] }
    return try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
}

// MARK: Decoding

@Test func remoteHostDecodes() throws {
    let h: RemoteHost = try decode("""
        {"name": "studio", "address": "studio.local:7433", "fingerprint": "\(fp)", "cert": "/h.pem", "state": "fingerprint",
         "error": "it showed another", "since": "2026-11-18T03:00:00Z", "retry_at": "2026-11-18T03:01:00Z",
         "our_fingerprint": "SHA256:me", "seen": "\(seenFP)", "host_name": "Studio", "host_version": "2.1.0", "sessions": 2}
        """)
    #expect(h.name == "studio" && h.id == "studio" && h.address == "studio.local:7433" && h.cert == "/h.pem")
    #expect(h.state == .fingerprint && h.error == "it showed another" && h.since.hasPrefix("2026") && h.retryAt.hasPrefix("2026"))
    #expect(h.ourFingerprint == "SHA256:me" && h.seen == seenFP && h.hostName == "Studio" && h.hostVersion == "2.1.0")
    #expect(h.sessions == 2 && h.fingerprint == fp)

    let empty: RemoteHost = try decode("{}")
    #expect(empty.state == .idle && empty.name.isEmpty && empty.sessions == 0)
    let odd: RemoteHost = try decode(#"{"state": "sideways"}"#)
    #expect(odd.state == .error, "a state this app doesn't know reads as an error")

    let snap: Snapshot = try decode(#"{"remote_hosts": [{"name": "a", "state": "connected"}]}"#)
    #expect(snap.remoteHosts?.first?.isConnected == true)
    let old: Snapshot = try decode("{}")
    #expect(old.remoteHosts == nil)
}

@Test func sessionsFromHostsDecode() throws {
    let added = try makeRemoteSession(id: "a", host: "studio", gone: true)
    #expect(added.isOnHost && added.hostName == "studio" && added.remoteID == "r-a" && added.isHostGone)
    let local = try makeRemoteSession(id: "b", remote: true)
    #expect(!local.isOnHost && local.hostName.isEmpty && !local.isHostGone && local.isRemote)
    let still = try makeRemoteSession(id: "c", host: "studio")
    #expect(still.isOnHost && !still.isHostGone)
}

@Test func inboxItemsAndNoticesNameTheirHost() throws {
    let item: InboxItem = try decode(#"{"id": -3, "session_id": "a", "kind": "done", "host": "studio"}"#)
    #expect(item.host == "studio" && item.id == -3)
    let mine: InboxItem = try decode(#"{"id": 3, "session_id": "a", "kind": "done"}"#)
    #expect(mine.host.isEmpty)

    let named: Notice = try decode(#"{"seq": 1, "session_id": "a", "kind": "done", "title": "api · studio", "host": "studio"}"#)
    #expect(named.displayTitle == "api · studio", "the daemon named it already")
    let unnamed: Notice = try decode(#"{"seq": 1, "session_id": "a", "kind": "done", "title": "api", "host": "studio"}"#)
    #expect(unnamed.displayTitle == "api · studio")
    let bare: Notice = try decode(#"{"seq": 1, "session_id": "a", "kind": "done", "host": "studio"}"#)
    #expect(bare.displayTitle == "studio")
    let local: Notice = try decode(#"{"seq": 1, "session_id": "a", "kind": "done", "title": "api"}"#)
    #expect(local.displayTitle == "api")
}

@Test func browseFoldersAndHarnessesDecode() throws {
    let b: RemoteBrowse = try decode("""
        {"host": "studio", "sessions": [{"id": "s1", "name": "", "harness": "antigravity", "dir": "/w/api", "status": "waiting",
         "status_detail": "permission: Write", "unseen": true, "needs_you": 1, "inbox": 2,
         "last_activity": "2026-11-18T03:00:00Z", "created": "2026-11-18T02:00:00Z", "added": true, "local_id": "L1"}]}
        """)
    let s = try #require(b.sessions.first)
    #expect(b.host == "studio" && s.id == "s1" && s.displayName == "api" && s.harnessTitle == "Antigravity")
    #expect(s.statusLine == "waiting · permission: Write" && s.unseen && s.needsYou == 1 && s.inbox == 2)
    #expect(s.added && s.localID == "L1" && s.lastActivity.hasPrefix("2026") && s.created.hasPrefix("2026"))
    let plain: RemoteSession = try decode(#"{"id": "x", "name": "n", "status": "busy", "harness": "zsh"}"#)
    #expect(plain.statusLine == "busy" && plain.harnessTitle == "zsh" && plain.displayName == "n")
    #expect(RemoteSession(id: "y").harnessTitle == "Claude", "no harness is Claude's, as for local sessions")
    let empty: RemoteBrowse = try decode("{}")
    #expect(empty.sessions.isEmpty && empty.host.isEmpty)

    let list: RemoteFolderList = try decode("""
        {"path": "/Users/me/src", "parent": "/Users/me", "home": "/Users/me",
         "folders": [{"name": "api", "path": "/Users/me/src/api"}, {"name": ".git", "path": "/Users/me/src/.git", "hidden": true}]}
        """)
    #expect(list.path == "/Users/me/src" && list.parent == "/Users/me" && list.home == "/Users/me")
    #expect(list.folders.map(\.name) == ["api", ".git"] && list.folders[1].hidden && list.folders[0].id == "/Users/me/src/api")
    let none: RemoteFolderList = try decode(#"{"home": "/h"}"#)
    #expect(none.folders.isEmpty && none.path.isEmpty)
    let folder: RemoteFolder = try decode(#"{"name": "n", "path": "/n"}"#)
    #expect(folder == RemoteFolder(name: "n", path: "/n"))

    let h: RemoteHarnesses = try decode("""
        {"harnesses": [{"name": "claude", "title": "Claude Code"}, {"name": "codex", "title": "Codex"}, {"name": "terminal", "title": "Terminal"}],
         "default": "terminal"}
        """)
    #expect(h.known == [.claude, .terminal], "what the app can't start is left out")
    #expect(h.defaultHarness == .terminal)
    #expect(RemoteHarnesses(["claude"], default: "codex").defaultHarness == nil)
    let bare: RemoteHarnesses = try decode("{}")
    #expect(bare.known.isEmpty && bare.defaultHarness == nil)
}

// MARK: Host states

@Test func eachHostStateSaysWhatsGoingOnAndWhatToDo() throws {
    let now = try #require(RemoteTime.parse("2026-11-18T03:00:00Z"))
    func host(_ state: RemoteHost.State, error: String = "", retry: String = "") -> RemoteHost {
        var h = RemoteHost(name: "studio", address: "studio.local:7433", state: state, error: error)
        (h.ourFingerprint, h.fingerprint, h.seen, h.retryAt) = ("SHA256:me", fp, seenFP, retry)
        return h
    }

    #expect(host(.idle).message(now: now).text.hasPrefix("Not connected."))
    #expect(host(.connecting).message(now: now).text == "Connecting to studio.local:7433…")
    var connected = host(.connected)
    #expect(connected.message(now: now).text == "Connected to studio.local:7433.")
    (connected.hostVersion, connected.hostName) = ("2.1.0", "Studio Mac")
    #expect(connected.message(now: now).text == "Connected to studio.local:7433, Agent Command 2.1.0. It calls itself Studio Mac.")

    let unreachable = host(.unreachable, error: "connection refused", retry: "2026-11-18T03:00:12Z").message(now: now)
    #expect(unreachable.text.contains("Can't reach studio.local:7433 (connection refused)"))
    #expect(unreachable.text.hasSuffix("Trying again in 12 s."))
    #expect(host(.unreachable).message(now: now).text.hasSuffix("Trying again soon."))

    let pending = host(.pending).message(now: now)
    #expect(pending.text.hasPrefix("Waiting for studio to approve this Mac. Your fingerprint is SHA256:me."))
    #expect(pending.copy == "SHA256:me")

    let refused = host(.unauthorized, error: "not authorized", retry: "2026-11-18T03:02:00Z").message(now: now)
    #expect(refused.text.contains("studio refused this Mac (not authorized)") && refused.text.hasSuffix("in 2 min."))
    #expect(refused.copy == "SHA256:me")

    #expect(host(.version, error: "api 2, this 1").message(now: now).text.contains("can't talk to (api 2, this 1)"))

    let mismatch = host(.fingerprint).message(now: now)
    #expect(mismatch.expected == fp && mismatch.seen == seenFP && mismatch.copy == nil)
    #expect(mismatch.text.contains("different fingerprint"))

    #expect(host(.error, error: "the client identity can't be read").message(now: now).text == "the client identity can't be read")
    #expect(host(.error).message(now: now).text == "Something went wrong on this Mac.")
}

@Test func hostStatesForListsAndRows() {
    let titles = RemoteHost.State.allCases.map { RemoteHost(name: "h", state: $0).stateTitle }
    #expect(titles == [
        "Not connected", "Connecting…", "Connected", "Can't reach it", "Waiting for approval", "Not authorized",
        "Version mismatch", "Fingerprint mismatch", "Error",
    ])
    let notes = RemoteHost.State.allCases.map { RemoteHost(name: "h", state: $0).offlineNote }
    #expect(notes == [
        "reconnecting…", "reconnecting…", nil, "reconnecting…", "waiting for approval", "not authorized",
        "version mismatch", "fingerprint mismatch", "can't connect",
    ])
    let failing = RemoteHost.State.allCases.filter { RemoteHost(name: "h", state: $0).isFailing }
    #expect(failing == [.unreachable, .unauthorized, .version, .fingerprint, .error])
    let retries = RemoteHost.State.allCases.filter { RemoteHost(name: "h", state: $0).canRetry }
    #expect(!retries.contains(.connecting) && !retries.contains(.connected) && retries.count == 7)
}

@Test func timeUntilARetry() throws {
    let now = try #require(RemoteTime.parse("2026-11-18T03:00:00Z"))
    #expect(RemoteTime.until("2026-11-18T03:00:30Z", now: now) == "in 30 s")
    #expect(RemoteTime.until("2026-11-18T03:05:00Z", now: now) == "in 5 min")
    #expect(RemoteTime.until("2026-11-18T02:59:00Z", now: now) == nil, "past")
    #expect(RemoteTime.until("", now: now) == nil)
}

// MARK: The sidebar

@Test func aRowSaysWhereItsSessionRuns() throws {
    let hosts = [RemoteHost(name: "studio", state: .connected), RemoteHost(name: "laptop", state: .unreachable),
                 RemoteHost(name: "new", state: .pending)]

    let local = SessionPlace(try makeRemoteSession(id: "a"), hosts: hosts)
    #expect(local.hostBadge == nil && local.shared == nil && !local.dimmed && local.note == nil)
    #expect(local.canToggleRemote && !local.canRemove && local.canArchive && local.help == nil)

    let shared = SessionPlace(try makeRemoteSession(id: "b", remote: true), hosts: hosts)
    #expect(shared.shared == "shared" && shared.help == "Remote: clients of Hosting can use it" && shared.canToggleRemote)
    let watched = SessionPlace(try makeRemoteSession(id: "b", remote: true), hosts: hosts, clients: 2)
    #expect(watched.shared == "shared · 2" && watched.help == "Remote: 2 attached now")

    let up = SessionPlace(try makeRemoteSession(id: "c", host: "studio"), hosts: hosts)
    #expect(up.hostBadge == "↗ studio" && !up.dimmed && up.note == nil && !up.canToggleRemote && up.help == "On studio")
    #expect(up.canRemove && up.canArchive, "a live added session can be removed here, or archived (ending it there)")

    let down = SessionPlace(try makeRemoteSession(id: "d", host: "laptop"), hosts: hosts)
    #expect(down.dimmed && down.note == "reconnecting…" && down.help == "On laptop: reconnecting…" && down.canRemove && down.canArchive)

    let waiting = SessionPlace(try makeRemoteSession(id: "e", host: "new"), hosts: hosts)
    #expect(waiting.dimmed && waiting.note == "waiting for approval")

    let unknown = SessionPlace(try makeRemoteSession(id: "f", host: "gone-host"), hosts: hosts)
    #expect(unknown.dimmed && unknown.note == "reconnecting…", "a host missing from the list counts as unreachable")

    let gone = SessionPlace(try makeRemoteSession(id: "g", host: "studio", gone: true), hosts: hosts)
    #expect(gone.dimmed && gone.note == "no longer available" && gone.canRemove && !gone.canToggleRemote)
    #expect(!gone.canArchive, "gone from its host: it can only be removed")
    #expect(!shared.canRemove && shared.canArchive, "a session shared from this Mac is this Mac's")
}

@Test func theSidebarFilterKeepsItsChoice() throws {
    for filter in [SidebarFilter.all, .local, .remote, .host("studio")] {
        #expect(SidebarFilter(rawValue: filter.rawValue) == filter)
        #expect(filter.id == filter.rawValue)
    }
    #expect(SidebarFilter(rawValue: "") == .all && SidebarFilter(rawValue: "host:") == .all && SidebarFilter(rawValue: "junk") == .all)
    #expect([SidebarFilter.all, .local, .remote, .host("s")].map(\.title) == ["All", "Local", "Remote", "↗ s"])

    let defaults = try #require(UserDefaults(suiteName: "acmd.tests.filter.\(UUID().uuidString)"))
    #expect(SidebarFilter.load(defaults) == .all, "nothing kept is All")
    SidebarFilter.host("studio").save(defaults)
    #expect(SidebarFilter.load(defaults) == .host("studio"))
    defaults.set(42, forKey: SidebarFilter.defaultsKey)
    #expect(SidebarFilter.load(defaults) == .all, "something unreadable is All")
}

@Test func theSidebarFilterPicksSessions() throws {
    let sessions = [
        try makeRemoteSession(id: "here"), try makeRemoteSession(id: "shared", remote: true),
        try makeRemoteSession(id: "s1", host: "studio"), try makeRemoteSession(id: "l1", host: "laptop"),
    ]
    func ids(_ filter: SidebarFilter) -> [String] { sessions.filter(filter.includes).map(\.id) }
    #expect(ids(.all) == ["here", "shared", "s1", "l1"])
    #expect(ids(.local) == ["here", "shared"], "a session shared from this Mac is still this Mac's")
    #expect(ids(.remote) == ["s1", "l1"])
    #expect(ids(.host("studio")) == ["s1"])
    #expect(ids(.host("nowhere")).isEmpty)

    let names = SidebarFilter.hostNames(known: [RemoteHost(name: "studio"), RemoteHost(name: "spare")], sessions: sessions)
    #expect(names == ["studio", "spare", "laptop"], "known hosts, then any with sessions here")
    #expect(SidebarFilter.choices(hosts: names).map(\.rawValue) == ["all", "local", "remote", "host:studio", "host:spare", "host:laptop"])
    #expect(SidebarFilter.host("gone").effective(hosts: names) == .all)
    #expect(SidebarFilter.host("studio").effective(hosts: names) == .host("studio"))
    #expect(SidebarFilter.local.effective(hosts: []) == .local)
}

// MARK: Hosting off

@Test func remoteSessionsWithHostingOffWarn() throws {
    var hosting = RemoteStatus()
    var turnedOn = false
    let one = [try makeRemoteSession(id: "api", remote: true), try makeRemoteSession(id: "plain")]
    let warning = try #require(RemoteWarning.check(sessions: one, hosting: hosting) { turnedOn = true })
    #expect(warning.id == "remote-hosting" && warning.title == "Hosting is off" && warning.actionTitle == "Turn On Hosting")
    #expect(warning.detail == "api is a Remote session, but Hosting is off, so no other Agent Command can use it.")
    warning.action()
    #expect(turnedOn)

    let two = one + [try makeRemoteSession(id: "web", remote: true)]
    #expect(RemoteWarning.check(sessions: two, hosting: hosting) {}?.detail.hasPrefix("2 sessions are Remote") == true)

    let archived = [try makeRemoteSession(id: "old", remote: true, status: "archived")]
    #expect(RemoteWarning.check(sessions: archived, hosting: hosting) {} == nil, "archived sessions don't count")
    let fromHost = [try makeRemoteSession(id: "x", host: "studio")]
    #expect(RemoteWarning.check(sessions: fromHost, hosting: hosting) {} == nil, "another host's sessions aren't this Mac's to share")
    #expect(RemoteWarning.check(sessions: one, hosting: nil) {} == nil, "a daemon without Hosting")
    hosting.enabled = true
    #expect(RemoteWarning.check(sessions: one, hosting: hosting) {} == nil)
}

// MARK: New sessions

@Test func attachedCountsAreConnectionsBySession() {
    let clients = [
        RemoteClient(name: "a", fingerprint: "1", connections: [
            RemoteConnection(id: "c1", sessions: ["s1", "s2", "s1"]), RemoteConnection(id: "c2", sessions: ["s1"]),
        ]),
        RemoteClient(name: "b", fingerprint: "2", connections: [RemoteConnection(id: "c3", sessions: ["s2"])]),
        RemoteClient(name: "c", fingerprint: "3"),
    ]
    #expect(RemoteAttached.counts(clients) == ["s1": 2, "s2": 2])
    #expect(RemoteAttached.counts([]).isEmpty)
}

@Test func inboxNewsCountsBothWays() throws {
    let start = InboxNews([try makeItem(4, session: "a"), try makeItem(-2, session: "b")])
    #expect(start.newest == 4 && start.newestRemote == -2)
    let (fresh, next) = start.fresh([
        try makeItem(4, session: "a"), try makeItem(5, session: "a"), try makeItem(-2, session: "b"), try makeItem(-3, session: "b"),
    ])
    #expect(fresh.map(\.id) == [5, -3], "new from this Mac counts up, new from hosts counts down")
    #expect(next.newest == 5 && next.newestRemote == -3)
    let (again, kept) = next.fresh([])
    #expect(again.isEmpty && kept.newest == 5 && kept.newestRemote == -3, "an empty queue forgets nothing")
    #expect(InboxNews([]).newest == 0 && InboxNews([]).newestRemote == 0)
}
