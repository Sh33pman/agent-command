import Foundation

// Remote hosts, the client side: the other Agent Commands this Mac uses Remote
// sessions on. The daemon connects to them; the app only reads what it says
// (internal/ipc/remotehosts.go). Missing fields are their defaults, so an
// older or newer acmd still reads.

private extension KeyedDecodingContainer {
    func value<T: Decodable>(_ key: Key, _ fallback: T) -> T {
        (try? decodeIfPresent(T.self, forKey: key)) ?? fallback
    }
}

/// A known host ([[remote.hosts]]) and its connection (internal/ipc.RemoteHost).
struct RemoteHost: Decodable, Equatable, Identifiable {
    /// What the daemon says about the connection (ipc.Host*).
    enum State: String, CaseIterable {
        case idle, connecting, connected, unreachable, pending, unauthorized, version, fingerprint, error
    }

    var name = ""
    var address = ""
    /// The pinned fingerprint, and the cert file it came from, if one did.
    var fingerprint = ""
    var cert = ""
    var stateName = "idle"
    var error = ""
    var since = ""
    var retryAt = ""
    /// This Mac's fingerprint: what the host's owner approves.
    var ourFingerprint = ""
    /// The fingerprint the host showed, when it isn't the pinned one.
    var seen = ""
    var hostName = ""
    var hostVersion = ""
    /// Sessions added here from it, archived ones aside.
    var sessions = 0
    /// Listed in config.toml: edited there, not here.
    var inConfig = false

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, address, fingerprint, cert, error, since, seen, sessions
        case inConfig = "in_config"
        case stateName = "state", retryAt = "retry_at", ourFingerprint = "our_fingerprint"
        case hostName = "host_name", hostVersion = "host_version"
    }

    init(name: String, address: String = "", state: State = .idle, error: String = "") {
        (self.name, self.address, self.stateName, self.error) = (name, address, state.rawValue, error)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.value(.name, "")
        address = c.value(.address, "")
        fingerprint = c.value(.fingerprint, "")
        cert = c.value(.cert, "")
        stateName = c.value(.stateName, "idle")
        error = c.value(.error, "")
        since = c.value(.since, "")
        retryAt = c.value(.retryAt, "")
        ourFingerprint = c.value(.ourFingerprint, "")
        seen = c.value(.seen, "")
        hostName = c.value(.hostName, "")
        hostVersion = c.value(.hostVersion, "")
        sessions = c.value(.sessions, 0)
        inConfig = c.value(.inConfig, false)
    }

    /// An unknown state reads as an error, which says what the daemon said.
    var state: State { State(rawValue: stateName) ?? .error }
    var isConnected: Bool { state == .connected }

    /// A few words for a list: "Connected", "Waiting for approval".
    var stateTitle: String {
        switch state {
        case .idle: "Not connected"
        case .connecting: "Connecting…"
        case .connected: "Connected"
        case .unreachable: "Can't reach it"
        case .pending: "Waiting for approval"
        case .unauthorized: "Not authorized"
        case .version: "Version mismatch"
        case .fingerprint: "Fingerprint mismatch"
        case .error: "Error"
        }
    }

    /// Whether the state is a problem to show in red.
    var isFailing: Bool { ![.idle, .connecting, .connected, .pending].contains(state) }

    /// Whether Retry can help: not while it's connecting or connected.
    var canRetry: Bool { state != .connecting && state != .connected }

    /// What a row of one of its sessions says while it isn't connected: nil when it is.
    var offlineNote: String? {
        switch state {
        case .connected: nil
        case .idle, .connecting, .unreachable: "reconnecting…"
        case .pending: "waiting for approval"
        case .unauthorized: "not authorized"
        case .version: "version mismatch"
        case .fingerprint: "fingerprint mismatch"
        case .error: "can't connect"
        }
    }

    /// What it says about the connection, with what to do about it.
    func message(now: Date = Date()) -> RemoteHostMessage {
        let reason = error.isEmpty ? "" : " (\(error))"
        let retry = RemoteTime.until(retryAt, now: now).map { " Trying again \($0)." } ?? " Trying again soon."
        switch state {
        case .idle:
            return RemoteHostMessage(text: "Not connected. It connects when you browse it or open one of its sessions.")
        case .connecting:
            return RemoteHostMessage(text: "Connecting to \(address)…")
        case .connected:
            let version = hostVersion.isEmpty ? "" : ", Agent Command \(hostVersion)"
            let called = hostName.isEmpty || hostName == name ? "" : " It calls itself \(hostName)."
            return RemoteHostMessage(text: "Connected to \(address)\(version).\(called)")
        case .unreachable:
            return RemoteHostMessage(
                text: "Can't reach \(address)\(reason). Check it's on, Hosting is on there, and the address and port are right.\(retry)")
        case .pending:
            return RemoteHostMessage(
                text: "Waiting for \(name) to approve this Mac. Your fingerprint is \(ourFingerprint). Check it matches the request on \(name).",
                copy: ourFingerprint)
        case .unauthorized:
            return RemoteHostMessage(
                text: "\(name) refused this Mac\(reason). Ask its owner to authorize your fingerprint, \(ourFingerprint).\(retry)",
                copy: ourFingerprint)
        case .version:
            return RemoteHostMessage(
                text: "\(name) runs a version of Agent Command that this one can't talk to\(reason). Update both to the same version.")
        case .fingerprint:
            return RemoteHostMessage(
                text: "\(name) showed a different fingerprint from the one you added. If its identity was regenerated, edit the host with its new fingerprint. If not, something may be in between: don't connect.",
                expected: fingerprint, seen: seen)
        case .error:
            return RemoteHostMessage(text: error.isEmpty ? "Something went wrong on this Mac." : error)
        }
    }
}

/// What the Remote Hosts browser says about a host's state.
struct RemoteHostMessage: Equatable {
    let text: String
    /// A fingerprint to offer a Copy button for.
    var copy: String? = nil
    /// For a fingerprint mismatch: the pinned one and the one the host showed.
    var expected: String? = nil
    var seen: String? = nil
}

extension RemoteTime {
    /// "in 12 s", "in 3 min"; nil when it's past or can't be read.
    static func until(_ text: String, now: Date = Date()) -> String? {
        guard let date = parse(text) else { return nil }
        let seconds = Int(date.timeIntervalSince(now).rounded(.up))
        guard seconds > 0 else { return nil }
        return seconds < 60 ? "in \(seconds) s" : "in \(seconds / 60) min"
    }
}

/// One of a host's Remote sessions, from remote.browse: api.Session, and
/// whether it's added here.
struct RemoteSession: Decodable, Equatable, Identifiable {
    var id = ""
    var name = ""
    var harness = ""
    var dir = ""
    var status = ""
    var statusDetail = ""
    var unseen = false
    var needsYou = 0
    var inbox = 0
    var lastActivity = ""
    var created = ""
    var added = false
    /// The session here, once it's added.
    var localID = ""

    enum CodingKeys: String, CodingKey {
        case id, name, harness, dir, status, unseen, inbox, created, added
        case statusDetail = "status_detail", needsYou = "needs_you", lastActivity = "last_activity", localID = "local_id"
    }

    init(id: String, name: String = "", added: Bool = false, localID: String = "") {
        (self.id, self.name, self.added, self.localID) = (id, name, added, localID)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.value(.id, "")
        name = c.value(.name, "")
        harness = c.value(.harness, "")
        dir = c.value(.dir, "")
        status = c.value(.status, "")
        statusDetail = c.value(.statusDetail, "")
        unseen = c.value(.unseen, false)
        needsYou = c.value(.needsYou, 0)
        inbox = c.value(.inbox, 0)
        lastActivity = c.value(.lastActivity, "")
        created = c.value(.created, "")
        added = c.value(.added, false)
        localID = c.value(.localID, "")
    }

    var displayName: String { name.isEmpty ? (dir as NSString).lastPathComponent : name }
    var harnessTitle: String { Harness(session: harness)?.title ?? (harness.isEmpty ? "Agent" : harness) }
    /// "waiting · permission: Write".
    var statusLine: String { statusDetail.isEmpty ? status : "\(status) · \(statusDetail)" }
}

/// A host's Remote sessions (internal/ipc.RemoteBrowse).
struct RemoteBrowse: Decodable, Equatable {
    var host = ""
    var sessions: [RemoteSession] = []

    enum CodingKeys: String, CodingKey { case host, sessions }

    init(host: String, sessions: [RemoteSession]) { (self.host, self.sessions) = (host, sessions) }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        host = c.value(.host, "")
        sessions = c.value(.sessions, [])
    }
}

/// A folder on a host (api.Folder).
struct RemoteFolder: Decodable, Equatable, Identifiable {
    var name = ""
    /// Absolute, on the host.
    var path = ""
    var hidden = false

    var id: String { path }

    enum CodingKeys: String, CodingKey { case name, path, hidden }

    init(name: String, path: String, hidden: Bool = false) { (self.name, self.path, self.hidden) = (name, path, hidden) }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.value(.name, "")
        path = c.value(.path, "")
        hidden = c.value(.hidden, false)
    }
}

/// A folder's subfolders on a host, or its recent folders (api.FolderList).
struct RemoteFolderList: Decodable, Equatable {
    /// The folder listed: absolute. "" for recent folders.
    var path = ""
    var parent = ""
    /// The host's home folder.
    var home = ""
    var folders: [RemoteFolder] = []

    enum CodingKeys: String, CodingKey { case path, parent, home, folders }

    init(path: String = "", parent: String = "", home: String = "", folders: [RemoteFolder] = []) {
        (self.path, self.parent, self.home, self.folders) = (path, parent, home, folders)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = c.value(.path, "")
        parent = c.value(.parent, "")
        home = c.value(.home, "")
        folders = c.value(.folders, [])
    }
}

/// What a host can run (api.Harnesses): its default is "" when its owner
/// hasn't chosen one.
struct RemoteHarnesses: Decodable, Equatable {
    struct Entry: Decodable, Equatable {
        var name = ""
        var title = ""

        enum CodingKeys: String, CodingKey { case name, title }

        init(name: String, title: String) { (self.name, self.title) = (name, title) }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = c.value(.name, "")
            title = c.value(.title, "")
        }
    }

    var harnesses: [Entry] = []
    var defaultName = ""

    enum CodingKeys: String, CodingKey {
        case harnesses
        case defaultName = "default"
    }

    init(_ names: [String], default name: String = "") {
        harnesses = names.map { Entry(name: $0, title: $0) }
        defaultName = name
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        harnesses = c.value(.harnesses, [])
        defaultName = c.value(.defaultName, "")
    }

    /// The ones the app knows, in the host's order.
    var known: [Harness] { harnesses.compactMap { Harness(rawValue: $0.name) } }
    var defaultHarness: Harness? { Harness(rawValue: defaultName).flatMap { known.contains($0) ? $0 : nil } }
}

// MARK: Sessions in the sidebar

/// What a sidebar row says about where a session runs: a host's badge, or
/// that it's shared from this Mac, and whether it can be used now.
struct SessionPlace: Equatable {
    /// "↗ studio-mac" for a session added from a host.
    var hostBadge: String?
    /// A Remote session on this Mac: "shared", or "shared · 2" with clients attached.
    var shared: String?
    /// Greyed out: its host can't be reached, or it's gone from there.
    var dimmed = false
    /// Said instead of its state: "reconnecting…", "no longer available".
    var note: String?
    /// Added from a host: Remove from This Mac takes it off here, and the host keeps it.
    var canRemove = false
    /// Archive ends it, on its host too. One gone from its host can only be removed.
    var canArchive = true
    /// Remote can be turned on or off: only for this Mac's sessions.
    var canToggleRemote = true

    /// hosts are the known hosts, and clients the connections attached to it.
    init(_ session: Session, hosts: [RemoteHost], clients: Int = 0) {
        guard session.isOnHost else {
            if session.isRemote { shared = clients > 0 ? "shared · \(clients)" : "shared" }
            return
        }
        canToggleRemote = false
        canRemove = true
        hostBadge = "↗ \(session.hostName)"
        if session.isHostGone {
            dimmed = true
            note = "no longer available"
            canArchive = false
            return
        }
        // A host that's gone from the list is as good as unreachable.
        let host = hosts.first { $0.name == session.hostName } ?? RemoteHost(name: session.hostName, state: .unreachable)
        if let offline = host.offlineNote {
            dimmed = true
            note = offline
        }
    }

    /// The row's tooltip line.
    var help: String? {
        if let hostBadge {
            let on = "On \(hostBadge.dropFirst(2))"
            return note.map { "\(on): \($0)" } ?? on
        }
        guard let shared else { return nil }
        return shared == "shared" ? "Remote: clients of Hosting can use it" : "Remote: \(shared.dropFirst(9)) attached now"
    }
}

/// Which sessions the sidebar lists.
enum SidebarFilter: Hashable, Identifiable {
    case all
    /// Sessions on this Mac, shared or not.
    case local
    /// Sessions added from any host.
    case remote
    case host(String)

    var id: String { rawValue }

    var rawValue: String {
        switch self {
        case .all: "all"
        case .local: "local"
        case .remote: "remote"
        case .host(let name): "host:" + name
        }
    }

    init(rawValue: String) {
        switch rawValue {
        case "local": self = .local
        case "remote": self = .remote
        case _ where rawValue.hasPrefix("host:") && rawValue.count > 5: self = .host(String(rawValue.dropFirst(5)))
        default: self = .all
        }
    }

    var title: String {
        switch self {
        case .all: "All"
        case .local: "Local"
        case .remote: "Remote"
        case .host(let name): "↗ " + name
        }
    }

    func includes(_ session: Session) -> Bool {
        switch self {
        case .all: true
        case .local: !session.isOnHost
        case .remote: session.isOnHost
        case .host(let name): session.hostName == name
        }
    }

    /// What can be chosen: All, Local, Remote and each host, known or with sessions here.
    static func choices(hosts: [String]) -> [SidebarFilter] {
        [.all, .local, .remote] + hosts.map { .host($0) }
    }

    /// What it shows with these hosts: a host that's no longer known shows everything.
    func effective(hosts: [String]) -> SidebarFilter {
        if case .host(let name) = self, !hosts.contains(name) { return .all }
        return self
    }

    /// Where the choice is kept. A missing or unreadable one is All.
    static let defaultsKey = "sidebarFilter"

    static func load(_ defaults: UserDefaults = .standard) -> SidebarFilter {
        SidebarFilter(rawValue: defaults.string(forKey: defaultsKey) ?? "")
    }

    func save(_ defaults: UserDefaults = .standard) {
        defaults.set(rawValue, forKey: Self.defaultsKey)
    }

    /// The hosts the filter offers: the known ones, then any that still have sessions here.
    static func hostNames(known: [RemoteHost], sessions: [Session]) -> [String] {
        var names = known.map(\.name)
        for session in sessions where session.isOnHost && !names.contains(session.hostName) {
            names.append(session.hostName)
        }
        return names
    }
}

// MARK: Hosting off

enum RemoteWarning {
    /// Remote sessions with Hosting off can't be reached by anyone: say so, with
    /// a way to turn it on. Nothing from a daemon without Hosting.
    static func check(sessions: [Session], hosting: RemoteStatus?, turnOn: @escaping () -> Void) -> AppWarning? {
        guard let hosting, !hosting.enabled else { return nil }
        let shared = sessions.filter { $0.isRemote && !$0.isOnHost && !$0.isArchived }
        guard !shared.isEmpty else { return nil }
        let which = shared.count == 1 ? "\(shared[0].displayName) is a Remote session" : "\(shared.count) sessions are Remote"
        return AppWarning(
            id: "remote-hosting",
            title: "Hosting is off",
            detail: "\(which), but Hosting is off, so no other Agent Command can use \(shared.count == 1 ? "it" : "them").",
            fix: "Turn on Hosting in Settings → Remote, or turn Remote off for \(shared.count == 1 ? "it" : "them").",
            actionTitle: "Turn On Hosting", action: turnOn)
    }
}

// MARK: New sessions

enum RemoteDefaults {
    /// The daemon's `remote.new_sessions_remote`: what ⌘N does, and where the
    /// Remote checkbox starts.
    static var newSessionsRemote: Bool { HostSettings.shared.bool("remote.new_sessions_remote") }
}

/// Connections attached to each session, from Hosting's clients: one client can
/// attach to a session more than once.
enum RemoteAttached {
    static func counts(_ clients: [RemoteClient]) -> [String: Int] {
        var counts: [String: Int] = [:]
        for conn in clients.flatMap(\.connections) {
            for id in Set(conn.sessions) { counts[id, default: 0] += 1 }
        }
        return counts
    }
}

/// New inbox items since the last look. The daemon's own ids count up; items
/// from hosts have negative ids that count down.
struct InboxNews: Equatable {
    var newest: Int64 = 0
    var newestRemote: Int64 = 0

    init(_ queue: [InboxItem]) {
        newest = max(0, queue.map(\.id).max() ?? 0)
        newestRemote = min(0, queue.map(\.id).min() ?? 0)
    }

    /// The items new since then, and what to remember for next time.
    func fresh(_ queue: [InboxItem]) -> (items: [InboxItem], next: InboxNews) {
        let items = queue.filter { $0.id > newest || $0.id < newestRemote }
        var next = InboxNews(queue)
        next.newest = max(next.newest, newest)
        next.newestRemote = min(next.newestRemote, newestRemote)
        return (items, next)
    }
}
