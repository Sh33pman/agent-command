import Foundation

/// The parts of the daemon's state the app shows (internal/ipc, internal/core).
nonisolated struct Snapshot: Decodable {
    let sessions: [Session]?
    let notices: [Notice]?
    let inbox: [InboxItem]?
    let jobs: [Job]?
    let groups: [SessionGroup]?
    let wake: WakeState?
    /// Hosting, for Remote sessions; nil from a daemon without it.
    var remote: RemoteStatus? = nil
    /// The Remote hosts this Mac uses sessions on, and their connections.
    var remoteHosts: [RemoteHost]? = nil
    /// The sets: sessions made together by splitting. Their sessions name them
    /// as their parent.
    var sets: [SessionSetEntity]? = nil
    /// This Mac's settings in effect, by key: config.get says what each is.
    var settings: [String: SettingValue]? = nil
    /// What's wrong with the host, or worth knowing.
    var health: [HealthItem]? = nil
    /// The health ids this app dismissed (it sends its client id).
    var dismissed: [String]? = nil
    var features: HostFeatures? = nil
    var update: UpdateState? = nil
    /// How reading aloud stands on this Mac.
    var speech: SpeechState? = nil
    /// Entities of types with no list of their own, which the app may not
    /// know (design 7.2): kept for their children's sake.
    var others: [EntityRef]? = nil
    /// This Mac's harnesses, in list order.
    var harnesses: [Harness]? = nil

    enum CodingKeys: String, CodingKey {
        case sessions, notices, inbox, jobs, groups, wake, remote, sets, settings, health, dismissed, features, update, speech, others, harnesses
        case remoteHosts = "remote_hosts"
    }

    /// The sessions, each with its group: a session that names none (in a
    /// container of a type the app doesn't know) goes in its nearest
    /// ancestor that's a group, following parents through sets and unknown
    /// entities on its own host (design 7.2). One with no such ancestor has
    /// none, and shows in its host's Default.
    func sessionsInGroups() -> [Session] {
        let groups = Set((self.groups ?? []).map { SessionGroup.key($0.host, $0.id) })
        var parents: [String: String] = [:]
        for set in sets ?? [] { parents[SessionGroup.key(set.host ?? "", set.id)] = set.parent }
        for other in others ?? [] where !other.parent.isEmpty { parents[SessionGroup.key(other.host, other.id)] = other.parent }
        return (sessions ?? []).map { session in
            guard session.groupID?.isEmpty ?? true, var at = session.parent else { return session }
            var placed = session
            for _ in 0..<16 { // an ancestor chain this deep is a loop
                let key = SessionGroup.key(session.hostName, at)
                if groups.contains(key) {
                    placed.groupID = at
                    break
                }
                guard let up = parents[key] else { break }
                at = up
            }
            return placed
        }
    }
}

/// An entity as every type has it (design 7.1): its type, id, host and
/// parent. Enough to place what's inside one of a type the app doesn't know.
nonisolated struct EntityRef: Decodable, Equatable {
    var type = ""
    var id = ""
    var host = ""
    var parent = ""

    enum CodingKeys: String, CodingKey { case type, id, host, parent }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? ""
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
        parent = try c.decodeIfPresent(String.self, forKey: .parent) ?? ""
    }
}

/// Something wrong with the host (internal/ipc.Health), and how to fix it.
nonisolated struct HealthItem: Decodable, Equatable {
    let id: String
    var severity = "warning"
    let title: String
    var detail = ""
    var fix: Fix? = nil

    /// An op to call with data, or on this Mac an acmd command (one that asks for a
    /// password, say), with what a button for it says.
    nonisolated struct Fix: Decodable, Equatable {
        var op: String? = nil
        var data: [String: SettingValue]? = nil
        var command: [String]? = nil
        var step: String? = nil
        var title: String? = nil
    }

    enum CodingKeys: String, CodingKey { case id, severity, title, detail, fix }

    init(id: String, severity: String = "warning", title: String, detail: String = "", fix: Fix? = nil) {
        (self.id, self.severity, self.title, self.detail, self.fix) = (id, severity, title, detail, fix)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        severity = try c.decodeIfPresent(String.self, forKey: .severity) ?? "warning"
        title = try c.decode(String.self, forKey: .title)
        detail = try c.decodeIfPresent(String.self, forKey: .detail) ?? ""
        fix = try c.decodeIfPresent(Fix.self, forKey: .fix)
    }
}

/// Waking the Mac for jobs (internal/ipc.WakeState): whether acmd may (its
/// sudoers rule is in place), whether every job wakes it, and the wake it set.
nonisolated struct WakeState: Decodable, Equatable {
    var allowed = false
    var all = false
    var next = ""
    var error = ""

    enum CodingKeys: String, CodingKey { case allowed, all, next, error }

    init(allowed: Bool = false, all: Bool = false, next: String = "", error: String = "") {
        (self.allowed, self.all, self.next, self.error) = (allowed, all, next, error)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        allowed = try c.decodeIfPresent(Bool.self, forKey: .allowed) ?? false
        all = try c.decodeIfPresent(Bool.self, forKey: .all) ?? false
        next = try c.decodeIfPresent(String.self, forKey: .next) ?? ""
        error = try c.decodeIfPresent(String.self, forKey: .error) ?? ""
    }
}

/// A group of sessions (internal/ipc.Group): a named, coloured container you arrange.
nonisolated struct SessionGroup: Decodable, Identifiable, Equatable {
    /// Its id on its host: every host's Default is "default", so a group is
    /// told apart by key.
    let id: String
    let name: String
    let isDefault: Bool
    let archivedAt: String
    /// One of the daemon's colour names (blue, magenta, cyan, yellow, green, red).
    var color = ""
    /// Its place among the groups you arranged; Default has none and comes last.
    var order = 0
    /// The Remote host it's on, for a host's tree; "" for this Mac's.
    var host = ""

    enum CodingKeys: String, CodingKey {
        case id, name, color, order, host
        case isDefault = "is_default", archivedAt = "archived_at"
    }

    init(id: String, name: String, isDefault: Bool = false, archivedAt: String = "", color: String = "", order: Int = 0, host: String = "") {
        (self.id, self.name, self.isDefault, self.archivedAt) = (id, name, isDefault, archivedAt)
        (self.color, self.order, self.host) = (color, order, host)
    }

    /// It told apart from other hosts' groups of the same id.
    var key: String { Self.key(host, id) }

    /// The key of group id on host.
    static func key(_ host: String, _ id: String) -> String { host.isEmpty ? id : host + "\u{1F}" + id }

    /// Its name in the sidebar: a host's groups say their host, and a host's
    /// Default is the host's name.
    var title: String {
        guard !host.isEmpty else { return name }
        return isDefault ? host : "\(name) · \(host)"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        archivedAt = try c.decodeIfPresent(String.self, forKey: .archivedAt) ?? ""
        color = try c.decodeIfPresent(String.self, forKey: .color) ?? ""
        order = try c.decodeIfPresent(Int.self, forKey: .order) ?? 0
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
    }

    /// The colours a group can have, in the daemon's order.
    static let colors = ["blue", "magenta", "cyan", "yellow", "green", "red"]

    /// The daemon leaves the time out for a group that isn't archived.
    var isArchived: Bool { !archivedAt.isEmpty }
}

/// An Inbox item named on its host (design 7.4): ids are unique only within
/// their host.
nonisolated struct InboxKey: Hashable {
    let host: String
    let id: Int64
}

/// Something a session sent you (internal/core.InboxItem).
nonisolated struct InboxItem: Decodable, Identifiable, Equatable {
    /// Its id on its host.
    let itemID: Int64
    let sessionID: String
    let kind: String
    let text: String
    let options: [String]
    let multipart: Bool
    let at: String
    let read: Bool
    let state: String
    /// The Remote host of an item from one of its sessions; "" for this Mac's.
    let host: String
    /// It on its host: what an op on it names.
    var id: InboxKey { InboxKey(host: host, id: itemID) }

    private enum CodingKeys: String, CodingKey {
        case itemID = "id", kind, text, options, multipart, at, read, state, host
        case sessionID = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        itemID = try c.decode(Int64.self, forKey: .itemID)
        sessionID = try c.decode(String.self, forKey: .sessionID)
        kind = try c.decode(String.self, forKey: .kind)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        options = try c.decodeIfPresent([String].self, forKey: .options) ?? []
        multipart = try c.decodeIfPresent(Bool.self, forKey: .multipart) ?? false
        at = try c.decodeIfPresent(String.self, forKey: .at) ?? ""
        read = try c.decodeIfPresent(Bool.self, forKey: .read) ?? false
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? "open"
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
    }

    var isOpen: Bool { state == "open" }
    /// Answered by picking an option, not by typing.
    var needsOption: Bool { (kind == "question" || kind == "permission") && !multipart }

    var kindLabel: String {
        switch kind {
        case "question": "Question"
        case "permission": "Permission"
        case "error": "Error"
        default: "Finished"
        }
    }

    var stateLabel: String {
        switch state {
        case "responded": "Responded"
        case "dismissed": "Dismissed"
        case "stale": "No longer relevant"
        default: "Open"
        }
    }

    /// Claude's own words, which are Markdown: a finished turn or a question. A
    /// permission or an error is acmd's plain text, often a command, shown as it is.
    var isMarkdown: Bool { kind == "done" || kind == "question" }

    /// The first line, without its Markdown.
    var preview: String {
        let line = text.split(whereSeparator: \.isNewline).first.map(String.init) ?? ""
        guard isMarkdown else { return line }
        return MarkdownBlock.parse(line).first?.plainText ?? line
    }

    /// Links, bold and inline code, with the lines as they are.
    var rendered: AttributedString {
        let options = AttributedString.MarkdownParsingOptions(
            interpretedSyntax: .inlineOnlyPreservingWhitespace)
        return (try? AttributedString(markdown: text, options: options)) ?? AttributedString(text)
    }

    /// How long ago it arrived, in a few characters.
    var age: String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        guard let date = formatter.date(from: at) ?? plain.date(from: at) else { return "" }
        let seconds = max(0, Int(Date().timeIntervalSince(date)))
        switch seconds {
        case ..<60: return "now"
        case ..<3600: return "\(seconds / 60)m"
        case ..<86400: return "\(seconds / 3600)h"
        default: return "\(seconds / 86400)d"
        }
    }
}

/// Something the daemon decided to notify about.
nonisolated struct Notice: Decodable {
    let seq: UInt64
    let sessionID: String
    let kind: String
    let title: String
    let body: String
    let sound: String
    /// The clients the daemon chose to show it, by id (notify.route).
    let to: [String]
    /// The Remote host, for a session added from one.
    let host: String

    /// The banner's title, naming the host for a session added from one. The
    /// daemon names it already; an older one doesn't.
    var displayTitle: String {
        guard !host.isEmpty, !title.hasSuffix(" · \(host)") else { return title }
        return title.isEmpty ? host : "\(title) · \(host)"
    }

    var isWithdrawal: Bool { kind == "withdraw" }
    /// Whether the daemon chose the client with this id to show it.
    func isFor(_ client: String) -> Bool { to.contains(client) }

    private enum CodingKeys: String, CodingKey {
        case seq, kind, title, body, sound, to, host
        case sessionID = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        seq = try c.decode(UInt64.self, forKey: .seq)
        sessionID = try c.decode(String.self, forKey: .sessionID)
        kind = try c.decode(String.self, forKey: .kind)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        body = try c.decodeIfPresent(String.self, forKey: .body) ?? ""
        sound = try c.decodeIfPresent(String.self, forKey: .sound) ?? ""
        to = try c.decodeIfPresent([String].self, forKey: .to) ?? []
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
    }
}

/// Why a session needs you, from the daemon: rank 1 is the most urgent, and
/// since is when it started. A reason this app doesn't know still sorts by rank.
nonisolated struct SessionAttention: Decodable, Equatable {
    let rank: Int
    let reason: String
    var since: String = ""

    /// since as a date; the distant past when it can't be read.
    var sinceDate: Date { Timestamps.date(since) ?? .distantPast }
}

/// Reads the daemon's RFC 3339 times, with or without fractional seconds.
nonisolated enum Timestamps {
    static func date(_ text: String) -> Date? {
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return withFraction.date(from: text) ?? ISO8601DateFormatter().date(from: text)
    }
}

/// A set as the daemon sends it: sessions made together by splitting, all in
/// one group (its parent) and on one host.
nonisolated struct SessionSetEntity: Decodable, Equatable {
    let type: String
    let id: String
    let parent: String
    var host: String?
}

nonisolated struct Session: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let cwd: String
    let status: String
    let kind: String
    let unseen: Bool
    /// claude, antigravity or terminal, and the harness it runs in ("" for a terminal).
    let harnessKind: String
    let harnessID: String
    let claudeSessionID: String
    /// What it's doing or waiting on, such as "Bash" or "permission: Write".
    var statusDetail: String?
    var description: String?
    /// Clients of this Mac's Hosting may see and use it (a Remote session).
    /// Older daemons don't send it: nil, a local session.
    var remote: Bool?
    /// The Remote host it was added from, and its id there; nil or "" for a
    /// session on this Mac.
    var host: String?
    var remoteID: String?
    /// The host no longer has it: it can only be removed here.
    var hostGone: Bool?
    /// The scheduled job that started it; nil or "" if none did.
    var jobID: String?
    /// What contains it: its set if it's in one, else its group. order is its
    /// place in its set, from 1.
    var parent: String?
    var order: Int?
    /// Its group, the time of its last activity, and why it needs you, if it does.
    var groupID: String?
    var lastEventAt: String?
    var attention: SessionAttention?
    /// What runs in it now, as the daemon says: claude, antigravity, other, none, or
    /// one the app doesn't know (shown generically).
    var agent: String?
    /// Its name, or its folder's.
    var display: String?
    /// What can be done with it now. The daemon decides; the app offers only these,
    /// and ignores ones it doesn't know.
    var capabilities: [String]?

    enum CodingKeys: String, CodingKey {
        case agent, capabilities
        case display = "display_name"
        case id, name, cwd, status, kind, unseen, description, remote, host, parent, order, attention
        case harnessKind = "harness_kind", harnessID = "harness_id"
        case claudeSessionID = "claude_session_id", statusDetail = "status_detail", remoteID = "remote_id"
        case hostGone = "host_gone", jobID = "job_id", groupID = "group_id", lastEventAt = "last_event_at"
    }

    /// The daemon leaves out what isn't set (internal/ipc.Session).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        cwd = try c.decodeIfPresent(String.self, forKey: .cwd) ?? ""
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? ""
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? ""
        unseen = try c.decodeIfPresent(Bool.self, forKey: .unseen) ?? false
        harnessKind = try c.decodeIfPresent(String.self, forKey: .harnessKind) ?? ""
        harnessID = try c.decodeIfPresent(String.self, forKey: .harnessID) ?? ""
        claudeSessionID = try c.decodeIfPresent(String.self, forKey: .claudeSessionID) ?? ""
        statusDetail = try c.decodeIfPresent(String.self, forKey: .statusDetail)
        description = try c.decodeIfPresent(String.self, forKey: .description)
        remote = try c.decodeIfPresent(Bool.self, forKey: .remote)
        host = try c.decodeIfPresent(String.self, forKey: .host)
        remoteID = try c.decodeIfPresent(String.self, forKey: .remoteID)
        hostGone = try c.decodeIfPresent(Bool.self, forKey: .hostGone)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID)
        parent = try c.decodeIfPresent(String.self, forKey: .parent)
        order = try c.decodeIfPresent(Int.self, forKey: .order)
        groupID = try c.decodeIfPresent(String.self, forKey: .groupID)
        lastEventAt = try c.decodeIfPresent(String.self, forKey: .lastEventAt)
        attention = try c.decodeIfPresent(SessionAttention.self, forKey: .attention)
        agent = try c.decodeIfPresent(String.self, forKey: .agent)
        display = try c.decodeIfPresent(String.self, forKey: .display)
        capabilities = try c.decodeIfPresent([String].self, forKey: .capabilities)
    }

    var isRemote: Bool { remote == true }
    /// Its Remote host's name, or "" for this Mac.
    var hostName: String { host ?? "" }
    /// Its group's key (SessionGroup.key): the group on its own host.
    var groupKey: String? { groupID.map { SessionGroup.key(hostName, $0) } }
    /// It as an op names it: on its host.
    var target: Target { Target(host: hostName, id: id) }
    /// It runs on a Remote host, added here from it.
    var isOnHost: Bool { !hostName.isEmpty }
    var isHostGone: Bool { isOnHost && hostGone == true }

    /// The status with its detail, as "waiting · permission: Write".
    var statusLine: String {
        guard let detail = statusDetail, !detail.isEmpty else { return status }
        return "\(status) · \(detail)"
    }

    /// Whether the daemon says this can be done with it now.
    func can(_ capability: SessionCapability) -> Bool {
        capabilities?.contains(capability.rawValue) == true
    }

    /// The agent that runs in it now; nil for a terminal with none.
    var runningAgent: Harness? {
        guard let agent, agent != "none", !agent.isEmpty else { return nil }
        return Harness.agent(agent)
    }

    /// The row's tooltip: everything about the session on a few lines.
    var details: String {
        var lines = [displayName, statusLine]
        if let description, !description.isEmpty { lines.append(description) }
        if !cwd.isEmpty { lines.append((cwd as NSString).abbreviatingWithTildeInPath) }
        if !claudeSessionID.isEmpty { lines.append("\((runningAgent ?? .claude).conversationTitle) \(claudeSessionID)") }
        return lines.joined(separator: "\n")
    }

    var isTerminal: Bool { harnessKind == "terminal" }
    /// The harness it runs in, or its kind when that was removed; a terminal.
    /// A host's session runs in one of the host's harnesses: it shows its kind.
    @MainActor var harness: Harness { isOnHost ? Harness(kind: harnessKind.isEmpty ? "claude" : harnessKind) : Harness.resolve(id: harnessID, kind: harnessKind) }
    /// Its harness's name ("Claude", "Work"), or "Terminal".
    @MainActor var harnessTitle: String { harness.title }
    var isArchived: Bool { status == "archived" }

    var displayName: String {
        if let display, !display.isEmpty { return display }
        return name.isEmpty ? (cwd as NSString).lastPathComponent : name
    }
}

/// What can be done with a session (internal/core's capabilities). Ones the app
/// doesn't know yet are ignored.
nonisolated enum SessionCapability: String {
    case resume, attach, archive, unarchive, purge
    case continueInJob = "continue_in_job"
    case openInEditor = "open_in_editor", setRemote = "set_remote"
}

/// An earlier agent conversation that can be resumed (internal/ipc.Conversation).
nonisolated struct Conversation: Decodable, Identifiable, Hashable {
    let id: String
    /// The harness whose history it's in, and its kind.
    var harness_id: String? = nil
    var harness_kind: String? = nil
    let cwd: String
    let title: String?
    let branch: String?
    let updated_at: String
    let running: Bool?
    let session_id: String?

    var displayTitle: String { title ?? "Untitled" }
    /// The harness it's from.
    @MainActor var harness: Harness { Harness.resolve(id: harness_id ?? "", kind: harness_kind ?? "claude") }
    var isRunning: Bool { running == true }

    /// The time, as far as RFC 3339 can be parsed.
    var updated: Date? {
        let plain = ISO8601DateFormatter()
        let precise = ISO8601DateFormatter()
        precise.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return precise.date(from: updated_at) ?? plain.date(from: updated_at)
    }

    @MainActor func matches(_ search: String) -> Bool {
        let query = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else { return true }
        return [displayTitle, cwd, branch ?? "", id, harness.title].contains { $0.lowercased().contains(query) }
    }
}

nonisolated struct DaemonError: LocalizedError {
    let message: String
    /// What kind of error, as the daemon says (internal/errcode): decide from
    /// it, never from the message. "" for one the app made itself.
    var code = ""
    /// The daemon answered, saying no: the connection is still good.
    var answered = false
    var errorDescription: String? { message }
}

/// Where acmd keeps things, the same way internal/paths resolves them.
nonisolated enum DaemonPaths {
    static var socket: String { socket(ProcessInfo.processInfo.environment) }

    static func socket(_ env: [String: String]) -> String {
        if let dir = env["ACMD_RUNTIME_DIR"], !dir.isEmpty {
            return dir + "/daemon.sock"
        }
        let tmp = (env["TMPDIR"] ?? "/tmp") as NSString
        let dir = tmp.appendingPathComponent("agent-command-\(getuid())")
        let path = (dir as NSString).appendingPathComponent("daemon.sock")
        // macOS limits unix socket paths to 104 bytes. Like internal/paths, a
        // TMPDIR too long for one falls back to the user's own state folder,
        // not a shared one like /tmp, where another user could make it first.
        return path.utf8.count >= 104 ? stateDir(env) + "/run/daemon.sock" : path
    }

    /// internal/paths' StateDir.
    static func stateDir(_ env: [String: String]) -> String {
        if let dir = env["ACMD_STATE_DIR"], !dir.isEmpty { return dir }
        return NSString(string: "~/.local/state/agent-command").expandingTildeInPath
    }

    /// Where Homebrew's acmd formula links acmd. The cask installs the formula.
    static let brewAcmd = "/opt/homebrew/bin/acmd"

    /// The acmd the app runs: Homebrew's, or ACMD_BIN (a dev build, or the acmd
    /// that opened the app).
    static var acmd: String { acmd(ProcessInfo.processInfo.environment) }

    static func acmd(_ env: [String: String]) -> String {
        if let bin = env["ACMD_BIN"], !bin.isEmpty { return bin }
        return brewAcmd
    }

    /// What to say when acmd isn't there to run, nil when it is.
    static func acmdMissing(_ path: String = acmd,
                            executable: (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }) -> String? {
        if executable(path) { return nil }
        if path == brewAcmd {
            return "acmd isn't installed. Install it with: brew install brettmostert/tap/acmd"
        }
        return "acmd isn't at \(path) (ACMD_BIN). Install it with: brew install brettmostert/tap/acmd"
    }
}

/// One connection to the daemon: newline-delimited JSON over its unix socket.
/// Calls block, so use it off the main thread: DaemonClient does.
///
/// This must never ask the daemon to shut down. The CLI does that on a version
/// mismatch, but the app can meet a daemon of another version and only needs
/// the same protocol.
nonisolated final class DaemonConnection {
    /// The protocol versions the app speaks (internal/ipc.ProtoMin, Proto): it
    /// uses the highest it shares with the daemon (design 8.2).
    static let protoMin = 2
    static let proto = 2
    /// The app's own version, in its hello: the daemon warns when acmd is
    /// older (they upgrade separately). A dev build has none, and says "app".
    static let appVersion = (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String) ?? "app"
    private let fd: Int32
    private var timeout: TimeInterval?
    /// The daemon's hello: the version agreed, and its ops.
    private(set) var daemon = DaemonHello()
    private var pending = Data()

    /// Once fd is open, a throw below leaves closing it to deinit, which
    /// runs for an init that throws: closing it here too would close it
    /// twice, and the second time the number can be another socket's.
    /// timeout is the longest a call waits for its reply (nil: as long as it
    /// takes, as the event stream does).
    init(socket path: String, timeout: TimeInterval? = nil) throws {
        fd = Foundation.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw DaemonError(message: "socket failed") }
        // A daemon that goes away is an error to report. Without this, writing to it
        // ends the whole app with SIGPIPE.
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        setTimeout(timeout)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            throw DaemonError(message: "socket path too long")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: bytes) }
        let status = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard status == 0 else {
            throw DaemonError(message: "daemon not reachable")
        }
        nonisolated struct Hello: Encodable {
            let proto: Int
            let proto_min: Int
            let proto_max: Int
            let version: String
            var client: ClientHello? = nil
        }
        // A daemon with no version in common refuses the hello, saying which
        // to update (proto_unsupported).
        daemon = try call("hello", Hello(proto: Self.proto, proto_min: Self.protoMin, proto_max: Self.proto,
                                         version: Self.appVersion, client: ClientIdentity.hello()))
        guard (Self.protoMin...Self.proto).contains(daemon.proto) else {
            throw DaemonError(message: "acmd's daemon chose protocol \(daemon.proto), which the app doesn't speak: update the app",
                              code: "proto_unsupported")
        }
    }

    /// Sets the longest a call waits for its reply (nil: as long as it takes).
    func setTimeout(_ timeout: TimeInterval?) {
        self.timeout = timeout
        let t = timeout ?? 0 // 0: no timeout
        var tv = timeval(tv_sec: Int(t), tv_usec: Int32((t - t.rounded(.down)) * 1_000_000))
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
    }

    /// Whether the daemon has op: the app offers what it can do.
    func has(_ op: String) -> Bool { daemon.ops.contains(op) }

    deinit { close(fd) }

    func call<Out: Decodable>(_ op: String, _ input: some Encodable) throws -> Out {
        try decodeReply(try exchange(try JSONEncoder().encode(Request(op: op, data: input))))
    }

    func call<Out: Decodable>(_ op: String) throws -> Out {
        try decodeReply(try exchange(try JSONEncoder().encode(Request<Empty>(op: op, data: nil))))
    }

    /// A call with its input and its reply as JSON.
    func raw(_ op: String, _ input: Data) throws -> Data {
        var line = Data(#"{"op":"#.utf8)
        line.append(try JSONEncoder().encode(op))
        line.append(Data(#","data":"#.utf8))
        line.append(input)
        line.append(Data("}".utf8))
        let reply = try JSONSerialization.jsonObject(with: try exchange(line)) as? [String: Any]
        guard reply?["ok"] as? Bool == true else { throw Self.refusal(reply) }
        return try JSONSerialization.data(withJSONObject: reply?["data"] ?? [String: Any](), options: .fragmentsAllowed)
    }

    /// A call whose reply has no data to read.
    func send(_ op: String, _ input: some Encodable) throws {
        let _: Ignored = try call(op, input)
    }

    /// Whether the connection can take another call: the daemon hasn't closed
    /// it (a restart), and nothing is left unread from an earlier one.
    var isOpen: Bool {
        guard pending.isEmpty else { return false }
        var byte: UInt8 = 0
        let n = recv(fd, &byte, 1, MSG_PEEK | MSG_DONTWAIT)
        return n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)
    }

    nonisolated private struct Request<In: Encodable>: Encodable {
        let op: String
        let data: In?
    }

    nonisolated private struct Empty: Encodable {}

    /// Any reply data, unread.
    nonisolated private struct Ignored: Decodable {
        init(from decoder: Decoder) throws {}
    }

    /// The daemon's reply: ok and its data, or why not.
    nonisolated private struct Reply<Out: Decodable>: Decodable {
        let ok: Bool
        let data: Out?
        let error: String?
        let code: String?
    }

    /// Decodes a reply once. A reply with no data decodes as {}, as an empty
    /// result does.
    private func decodeReply<Out: Decodable>(_ line: Data) throws -> Out {
        let reply = try JSONDecoder().decode(Reply<Out>.self, from: line)
        guard reply.ok else {
            throw DaemonError(message: reply.error ?? "daemon error", code: reply.code ?? "", answered: true)
        }
        if let data = reply.data { return data }
        return try JSONDecoder().decode(Out.self, from: Data("{}".utf8))
    }

    private static func refusal(_ reply: [String: Any]?) -> DaemonError {
        DaemonError(message: reply?["error"] as? String ?? "daemon error", code: reply?["code"] as? String ?? "", answered: true)
    }

    /// Writes one request line and reads its reply line.
    private func exchange(_ request: Data) throws -> Data {
        var line = request
        line.append(0x0A)
        try line.withUnsafeBytes { raw in
            var offset = 0
            while offset < raw.count {
                let n = write(fd, raw.baseAddress! + offset, raw.count - offset)
                guard n > 0 else { throw lost() }
                offset += n
            }
        }
        return try readLine()
    }

    /// Why a read or write failed: the timeout, or the daemon gone.
    private func lost() -> DaemonError {
        if errno == EAGAIN || errno == EWOULDBLOCK, let timeout {
            return DaemonError(message: "acmd's daemon didn't answer within \(Int(timeout)) s", code: "timeout")
        }
        return DaemonError(message: "daemon connection lost")
    }

    /// Starts the daemon's event stream on this connection (subscribe): read it
    /// with nextEvent. Nothing else can be called on it after.
    func subscribe(since: UInt64) throws {
        nonisolated struct Subscribe: Encodable { let since: UInt64 }
        try send("subscribe", Subscribe(since: since))
    }

    /// The stream's next event.
    func nextEvent() throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: try readLine()) as? [String: Any] ?? [:]
    }

    /// Whether another whole line is already read: more events came together.
    var hasLineWaiting: Bool { pending.contains(0x0A) }

    private func readLine() throws -> Data {
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            if let end = pending.firstIndex(of: 0x0A) {
                let line = pending[pending.startIndex..<end]
                pending = Data(pending[(end + 1)...])
                return Data(line)
            }
            let n = read(fd, &buffer, buffer.count)
            guard n > 0 else { throw n < 0 ? lost() : DaemonError(message: "daemon connection lost") }
            pending.append(contentsOf: buffer[0..<n])
        }
    }
}

/// Calls to the daemon, off the caller's thread, on connections kept open
/// between calls. A call takes an idle connection, or opens one when none is
/// free, so calls at the same time don't wait on each other (speech.stop
/// while speech.say is still talking). Each call waits at most callTimeout
/// for its reply, except the ops in untimed. The event stream has a
/// connection of its own.
nonisolated final class DaemonClient: @unchecked Sendable {
    /// The app's calls.
    static let shared = DaemonClient()
    /// Longer than the daemon waits for a Remote host (15 s).
    static let callTimeout: TimeInterval = 30
    /// Ops that answer when they're done, however long that takes: speech.say
    /// answers when the speech ends.
    static let untimed: Set<String> = ["speech.say"]
    /// The most idle connections kept.
    static let keep = 4

    private let socket: @Sendable () -> String
    private let timeout: TimeInterval
    private let lock = NSLock()
    private var idle: [DaemonConnection] = []

    init(socket: @escaping @Sendable () -> String = { DaemonPaths.socket }, timeout: TimeInterval = callTimeout) {
        self.socket = socket
        self.timeout = timeout
    }

    /// Runs work on a connection, off the caller's thread. op says which
    /// timeout applies.
    func with<T: Sendable>(_ op: String = "", _ work: @escaping @Sendable (DaemonConnection) throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { done in
            DispatchQueue.global(qos: .userInitiated).async {
                done.resume(with: Result { try self.withBlocking(op, work) })
            }
        }
    }

    func call<Out: Decodable>(_ op: String, _ input: some Encodable) async throws -> Out {
        let input = try JSONEncoder().encode(input)
        let data = try await raw(op, input)
        return try JSONDecoder().decode(Out.self, from: data)
    }

    func raw(_ op: String, _ input: Data) async throws -> Data {
        try await with(op) { try $0.raw(op, input) }
    }

    /// Runs work on a connection here, blocking. A connection goes back to the
    /// idle ones after the call, or after the daemon's own no; after anything
    /// else it's closed, since a reply could still be on its way.
    func withBlocking<T>(_ op: String, _ work: (DaemonConnection) throws -> T) throws -> T {
        let conn = try checkout()
        conn.setTimeout(Self.untimed.contains(op) ? nil : timeout)
        do {
            let result = try work(conn)
            checkin(conn)
            return result
        } catch let error as DaemonError where error.answered {
            checkin(conn)
            throw error
        }
    }

    /// An idle connection the daemon hasn't closed (a restart), or a new one.
    private func checkout() throws -> DaemonConnection {
        while let conn = lock.withLock({ idle.popLast() }) {
            if conn.isOpen { return conn }
        }
        return try DaemonConnection(socket: socket(), timeout: timeout)
    }

    private func checkin(_ conn: DaemonConnection) {
        lock.withLock {
            if idle.count < Self.keep { idle.append(conn) }
        }
    }
}

/// Runs the app's own acmd for a command, off the main thread.
nonisolated enum Acmd {
    /// Runs it with args, then done(succeeded, output) on the main actor. It has
    /// no terminal, so a command that needs an admin password asks through
    /// macOS's dialog.
    nonisolated static func run(_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) {
        let bin = DaemonPaths.acmd
        if bin.hasPrefix("/") {
            Command.run(bin, args, done)
        } else {
            Command.run("/usr/bin/env", [bin] + args, done)
        }
    }
}

/// Runs a program off the main thread, with no terminal.
nonisolated enum Command {
    /// Runs path with args, and env added to the app's environment, then
    /// done(succeeded, output) on the main actor. output is stdout and stderr.
    nonisolated static func run(_ path: String, _ args: [String], env: [String: String] = [:],
                                _ done: @escaping @MainActor (Bool, String) -> Void) {
        DispatchQueue.global().async {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = args
            if !env.isEmpty {
                process.environment = ProcessInfo.processInfo.environment.merging(env) { _, new in new }
            }
            process.standardInput = FileHandle.nullDevice
            let pipe = Pipe()
            process.standardOutput = pipe
            process.standardError = pipe
            let ok: Bool
            let text: String
            do {
                try process.run()
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                process.waitUntilExit()
                ok = process.terminationStatus == 0
                text = String(decoding: data, as: UTF8.self)
            } catch {
                ok = false
                text = error.localizedDescription
            }
            let output = text.trimmingCharacters(in: .whitespacesAndNewlines)
            Task { @MainActor in done(ok, output) }
        }
    }
}

/// One page of a list op (internal/ipc/page.go): its items, and the cursor
/// for the next page, "" on the last.
extension Page: Sendable where Item: Sendable {}

nonisolated struct Page<Item: Decodable>: Decodable {
    var items: [Item] = []
    var nextCursor = ""

    /// The most items a page holds.
    static var max: Int { 500 }

    enum CodingKeys: String, CodingKey {
        case items
        case nextCursor = "next_cursor"
    }

    init(items: [Item] = [], nextCursor: String = "") {
        (self.items, self.nextCursor) = (items, nextCursor)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = try c.decodeIfPresent([Item].self, forKey: .items) ?? []
        nextCursor = try c.decodeIfPresent(String.self, forKey: .nextCursor) ?? ""
    }
}

/// Asks for one page of a list: "" is the first.
nonisolated struct PageRequest: Encodable {
    var limit: Int?
    var cursor: String?

    init(limit: Int? = nil, cursor: String = "") {
        self.limit = limit
        self.cursor = cursor.isEmpty ? nil : cursor
    }
}

/// A session, group or set as an op names it: its id on its host ("" for
/// this Mac), design 7.4.
nonisolated struct Target: Encodable, Equatable {
    var host: String?
    let id: String

    init(host: String = "", id: String) {
        self.host = host.isEmpty ? nil : host
        self.id = id
    }
}

/// An Inbox item as an op names it.
nonisolated struct ItemTarget: Encodable, Equatable {
    var host: String?
    let id: Int64

    init(_ key: InboxKey) {
        host = key.host.isEmpty ? nil : key.host
        id = key.id
    }
}

/// The daemon's hello (internal/ipc.Hello): the protocol version agreed, the
/// range it speaks, and its ops.
nonisolated struct DaemonHello: Decodable, Equatable {
    var proto = 0
    var protoMin = 0
    var protoMax = 0
    var version = ""
    var ops: Set<String> = []

    enum CodingKeys: String, CodingKey {
        case proto, version, ops
        case protoMin = "proto_min", protoMax = "proto_max"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        proto = try c.decodeIfPresent(Int.self, forKey: .proto) ?? 0
        protoMin = try c.decodeIfPresent(Int.self, forKey: .protoMin) ?? proto
        protoMax = try c.decodeIfPresent(Int.self, forKey: .protoMax) ?? proto
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? ""
        ops = Set(try c.decodeIfPresent([String].self, forKey: .ops) ?? [])
    }
}

/// Opens something in the editor chosen in Settings, on this Mac
/// (internal/ipc.EditorOpen): a session's folder, or a file at a line.
nonisolated struct EditorOpen: Encodable, Equatable {
    var id: String?
    var path: String?
    var `in`: String?
    var line: Int?
    var column: Int?
}

/// What acmd doctor finds (internal/ipc.DoctorReport).
nonisolated struct DoctorReport: Decodable, Equatable {
    nonisolated struct Check: Decodable, Equatable {
        var section = ""
        var status = "ok"
        var text = ""
        var fix: String?
    }

    var checks: [Check] = []

    /// The report as acmd doctor prints it: each section's checks, a fix
    /// under each problem, then what it comes to.
    var text: String {
        var sections: [String] = []
        var bySection: [String: [Check]] = [:]
        for check in checks {
            if bySection[check.section] == nil { sections.append(check.section) }
            bySection[check.section, default: []].append(check)
        }
        let marks = ["ok": "✓", "info": "·", "warn": "!", "fail": "✗"]
        var lines: [String] = []
        for (i, section) in sections.enumerated() {
            if i > 0 { lines.append("") }
            lines.append(section)
            for check in bySection[section] ?? [] {
                lines.append("  \(marks[check.status] ?? "·") \(check.text)")
                if let fix = check.fix, !fix.isEmpty, check.status == "warn" || check.status == "fail" {
                    lines.append("      fix: \(fix)")
                }
            }
        }
        let fails = checks.count { $0.status == "fail" }, warns = checks.count { $0.status == "warn" }
        lines.append("")
        if fails == 0 && warns == 0 {
            lines.append("Everything looks good.")
        } else {
            func plural(_ n: Int, _ word: String) -> String { n == 1 ? "1 \(word)" : "\(n) \(word)s" }
            lines.append("\(plural(fails, "problem")), \(plural(warns, "warning")).")
        }
        return lines.joined(separator: "\n")
    }
}
