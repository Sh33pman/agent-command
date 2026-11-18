import Foundation

/// The parts of the daemon's state the app shows (internal/ipc, internal/core).
struct Snapshot: Decodable {
    let sessions: [Session]?
    let notices: [Notice]?
    let inbox: [InboxItem]?
    let jobs: [Job]?
    let groups: [SessionGroup]?
    let wake: WakeState?
}

/// Waking the Mac for jobs (internal/ipc.WakeState): whether acmd may (its
/// sudoers rule is in place), whether every job wakes it, and the wake it set.
struct WakeState: Decodable, Equatable {
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

/// A group of sessions (internal/core.Group), for choosing a job's group.
struct SessionGroup: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let isDefault: Bool
    let archivedAt: String

    enum CodingKeys: String, CodingKey {
        case id = "ID", name = "Name", isDefault = "IsDefault", archivedAt = "ArchivedAt"
    }

    init(id: String, name: String, isDefault: Bool = false, archivedAt: String = "") {
        (self.id, self.name, self.isDefault, self.archivedAt) = (id, name, isDefault, archivedAt)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        isDefault = try c.decodeIfPresent(Bool.self, forKey: .isDefault) ?? false
        archivedAt = try c.decodeIfPresent(String.self, forKey: .archivedAt) ?? ""
    }

    /// Go's zero time is "0001-01-01T00:00:00Z".
    var isArchived: Bool { !archivedAt.isEmpty && !archivedAt.hasPrefix("0001-") }
}

/// Something a session sent you (internal/core.InboxItem).
struct InboxItem: Decodable, Identifiable, Equatable {
    let id: Int64
    let sessionID: String
    let kind: String
    let text: String
    let options: [String]
    let multipart: Bool
    let at: String
    let read: Bool
    let state: String

    private enum CodingKeys: String, CodingKey {
        case id, kind, text, options, multipart, at, read, state
        case sessionID = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        sessionID = try c.decode(String.self, forKey: .sessionID)
        kind = try c.decode(String.self, forKey: .kind)
        text = try c.decodeIfPresent(String.self, forKey: .text) ?? ""
        options = try c.decodeIfPresent([String].self, forKey: .options) ?? []
        multipart = try c.decodeIfPresent(Bool.self, forKey: .multipart) ?? false
        at = try c.decodeIfPresent(String.self, forKey: .at) ?? ""
        read = try c.decodeIfPresent(Bool.self, forKey: .read) ?? false
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? "open"
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
struct Notice: Decodable {
    let seq: UInt64
    let sessionID: String
    let kind: String
    let title: String
    let body: String
    let sound: String
    let emitter: String

    var isWithdrawal: Bool { kind == "withdraw" }
    var isForApp: Bool { emitter == "app" }

    private enum CodingKeys: String, CodingKey {
        case seq, kind, title, body, sound, emitter
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
        emitter = try c.decodeIfPresent(String.self, forKey: .emitter) ?? ""
    }
}

struct Session: Decodable, Identifiable, Equatable {
    let id: String
    let name: String
    let cwd: String
    let status: String
    let kind: String
    let unseen: Bool
    let harness: String
    let claudeSessionID: String
    /// What it's doing or waiting on, such as "Bash" or "permission: Write".
    var statusDetail: String?
    var description: String?
    /// Clients of this Mac's Hosting may see and use it (a Remote session).
    /// Older daemons don't send it: nil, a local session.
    var remote: Bool?

    enum CodingKeys: String, CodingKey {
        case id = "ID", name = "Name", cwd = "Cwd", status = "Status", kind = "Kind", unseen = "Unseen"
        case harness = "Harness", claudeSessionID = "ClaudeSessionID"
        case statusDetail = "StatusDetail", description = "Description", remote = "Remote"
    }

    var isRemote: Bool { remote == true }

    /// The status with its detail, as "waiting · permission: Write".
    var statusLine: String {
        guard let detail = statusDetail, !detail.isEmpty else { return status }
        return "\(status) · \(detail)"
    }

    /// Claude runs in it: a Claude session, or Claude typed in a terminal (with
    /// or without hooks). Not Antigravity, whose conversation id is kept in the same place.
    var runsClaude: Bool {
        isClaude || (!isAntigravity && (!claudeSessionID.isEmpty || (status == "unknown" && statusDetail == "claude")))
    }

    /// Whose conversation claudeSessionID is: Antigravity's, or else Claude's.
    var conversationHarness: Harness { isAntigravity ? .antigravity : .claude }

    /// The row's tooltip: everything about the session on a few lines.
    var details: String {
        var lines = [displayName, statusLine]
        if let description, !description.isEmpty { lines.append(description) }
        if !cwd.isEmpty { lines.append((cwd as NSString).abbreviatingWithTildeInPath) }
        if !claudeSessionID.isEmpty { lines.append("\(conversationHarness.conversationTitle) \(claudeSessionID)") }
        return lines.joined(separator: "\n")
    }

    var isTerminal: Bool { harness == "terminal" }
    /// What it was started with, if the app knows it.
    var harnessKind: Harness? { Harness(session: harness) }
    /// Started as Claude: what Claude's own features (its session id, resume) need.
    var isClaude: Bool { harnessKind == .claude }
    var isAntigravity: Bool { harnessKind == .antigravity }
    /// "Claude", "Antigravity" or "Terminal"; "Agent" for one the app can't name.
    var harnessTitle: String { harnessKind?.title ?? "Agent" }
    /// A terminal counts once an agent has started in it.
    var isAgent: Bool { !isTerminal || !claudeSessionID.isEmpty }
    var isArchived: Bool { status == "archived" }

    /// Stopped by itself or by a reboot: activating it resumes it.
    var isStopped: Bool { status == "paused" || status == "finished" }

    var displayName: String {
        name.isEmpty ? (cwd as NSString).lastPathComponent : name
    }
}

/// An earlier agent conversation that can be resumed (internal/ipc.Conversation).
struct Conversation: Decodable, Identifiable, Hashable {
    let id: String
    /// "antigravity" for Antigravity's; "" or "claude" for Claude's.
    var harness: String? = nil
    let cwd: String
    let title: String?
    let branch: String?
    let updated_at: String
    let running: Bool?
    let session_id: String?

    var displayTitle: String { title ?? "Untitled" }
    /// The agent it's from. Older daemons send no harness: those are Claude's.
    var harnessKind: Harness { harness == Harness.antigravity.rawValue ? .antigravity : .claude }
    var isRunning: Bool { running == true }

    /// The time, as far as RFC 3339 can be parsed.
    var updated: Date? {
        let plain = ISO8601DateFormatter()
        let precise = ISO8601DateFormatter()
        precise.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return precise.date(from: updated_at) ?? plain.date(from: updated_at)
    }

    func matches(_ search: String) -> Bool {
        let query = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else { return true }
        return [displayTitle, cwd, branch ?? "", id, harnessKind.title].contains { $0.lowercased().contains(query) }
    }
}

/// What launch does about the agents' hooks: nothing, refresh them quietly with
/// `acmd init` (the app has moved), or offer to add the missing ones.
enum SetupNeed: Equatable {
    case none, refresh
    /// Ask first. claude: Claude Code's hooks are missing; antigravity: agy is
    /// installed and its hook is missing.
    case offer(claude: Bool, antigravity: Bool)

    init(claudeHooked: Bool, claudeCurrent: Bool, agyInstalled: Bool, agyHooked: Bool, agyCurrent: Bool) {
        let agyMissing = agyInstalled && !agyHooked
        if !claudeHooked || agyMissing {
            self = .offer(claude: !claudeHooked, antigravity: agyMissing)
        } else if !claudeCurrent || (agyInstalled && !agyCurrent) {
            self = .refresh
        } else {
            self = .none
        }
    }

    /// The offer's words: where the hooks go.
    var offerText: String {
        guard case .offer(let claude, let antigravity) = self else { return "" }
        let places = [claude ? "your Claude Code settings" : nil, antigravity ? "Antigravity's hooks.json" : nil]
            .compactMap { $0 }.joined(separator: " and ")
        return """
            This adds hook entries to \(places), so sessions can show when they're busy, waiting \
            or done. It backs the file up first, and keeps your other settings. It also saves your login shell's \
            environment for the sessions it starts.
            """
    }

    /// First-time setup, rather than an agent installed since: it also offers waking the Mac.
    var isFirstSetup: Bool {
        if case .offer(claude: true, _) = self { return true }
        return false
    }
}

struct DaemonError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

/// Where acmd keeps things, the same way internal/paths resolves them.
enum DaemonPaths {
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

    /// Claude's settings, where internal/setup adds and marks the hooks.
    private static var claudeSettings: String {
        let env = ProcessInfo.processInfo.environment
        let dir = env["CLAUDE_CONFIG_DIR"].flatMap { $0.isEmpty ? nil : $0 }
            ?? NSString(string: "~/.claude").expandingTildeInPath
        let settings = (dir as NSString).appendingPathComponent("settings.json")
        return (try? String(contentsOfFile: settings, encoding: .utf8)) ?? ""
    }

    /// internal/setup's HookMarker, at the end of each hook command acmd writes.
    static let hookMarker = "# managed by acmd"

    static var hooksInstalled: Bool { claudeSettings.contains(hookMarker) }

    /// The hooks run this binary, rather than one from before the app moved.
    static var hooksCurrent: Bool { claudeSettings.contains(acmd) }

    /// agy's shared hooks.json, where internal/setup adds its named hook.
    private static var agyHooksFile: String {
        let path = NSString(string: "~/.gemini/config/hooks.json").expandingTildeInPath
        return (try? String(contentsOfFile: path, encoding: .utf8)) ?? ""
    }

    /// What the app needs to do about the hooks, from what's on this Mac.
    static var setupNeed: SetupNeed {
        let agy = agyPath(ProcessInfo.processInfo.environment, home: NSHomeDirectory()) {
            FileManager.default.isExecutableFile(atPath: $0)
        }
        let hooks = agyHooks(agyHooksFile)
        return SetupNeed(
            claudeHooked: hooksInstalled, claudeCurrent: hooksCurrent,
            agyInstalled: agy != nil, agyHooked: hooks.contains { $0.contains(hookMarker) },
            agyCurrent: hooks.contains { $0.contains(acmd) })
    }

    /// Where agy is, if it's installed: on PATH, or where its installer and Homebrew put
    /// it, as an app started from the Dock gets a short PATH.
    static func agyPath(_ env: [String: String], home: String, isExecutable: (String) -> Bool) -> String? {
        let path = (env["PATH"] ?? "").split(separator: ":").map(String.init)
        let usual = [(home as NSString).appendingPathComponent(".local/bin"), "/opt/homebrew/bin", "/usr/local/bin"]
        return (path + usual).lazy
            .filter { !$0.isEmpty }
            .map { ($0 as NSString).appendingPathComponent("agy") }
            .first(where: isExecutable)
    }

    /// The handler commands of acmd's named hook ("agent-command") in agy's hooks.json; none
    /// when the file or the hook isn't there.
    static func agyHooks(_ file: String) -> [String] {
        let text = file.hasPrefix("\u{FEFF}") ? String(file.dropFirst()) : file
        guard let data = text.data(using: .utf8),
              let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let hook = root["agent-command"]
        else { return [] }
        // Handlers sit under each event, flat or in matcher groups: find their commands.
        func commands(_ value: Any) -> [String] {
            switch value {
            case let list as [Any]: list.flatMap(commands)
            case let object as [String: Any]:
                object.flatMap { key, value in key == "command" ? [value as? String].compactMap { $0 } : commands(value) }
            default: []
            }
        }
        return commands(hook)
    }

    /// The binary inside the app, which always matches it.
    static var acmd: String {
        if let bin = ProcessInfo.processInfo.environment["ACMD_BIN"] { return bin }
        if let bundled = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("agent-command").path,
           FileManager.default.isExecutableFile(atPath: bundled) {
            return bundled
        }
        return "acmd"
    }
}

/// One connection to the daemon: newline-delimited JSON over its unix socket.
/// Calls block, so use it off the main thread.
///
/// This must never ask the daemon to shut down. The CLI does that on a version
/// mismatch, but the app can meet a daemon of another version and only needs
/// the same protocol.
final class DaemonConnection {
    /// internal/ipc.Proto
    private static let proto = 1
    private let fd: Int32
    private var pending = Data()

    init(socket path: String) throws {
        fd = Foundation.socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw DaemonError(message: "socket failed") }
        // A daemon that goes away is an error to report. Without this, writing to it
        // ends the whole app with SIGPIPE.
        var on: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            close(fd)
            throw DaemonError(message: "socket path too long")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: bytes) }
        let status = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard status == 0 else {
            close(fd)
            throw DaemonError(message: "daemon not reachable")
        }
        struct Hello: Codable {
            let proto: Int
            let version: String
        }
        let theirs: Hello = try call("hello", Hello(proto: Self.proto, version: "app"))
        guard theirs.proto == Self.proto else {
            close(fd)
            throw DaemonError(message: "daemon speaks protocol \(theirs.proto), the app \(Self.proto)")
        }
    }

    deinit { close(fd) }

    func call<Out: Decodable>(_ op: String, _ input: some Encodable) throws -> Out {
        try JSONDecoder().decode(Out.self, from: try exchange(op, JSONEncoder().encode(input)))
    }

    func call<Out: Decodable>(_ op: String) throws -> Out {
        try JSONDecoder().decode(Out.self, from: try exchange(op, nil))
    }

    /// A call whose reply has no data to read.
    func send(_ op: String, _ input: some Encodable) throws {
        _ = try exchange(op, JSONEncoder().encode(input))
    }

    private func exchange(_ op: String, _ data: Data?) throws -> Data {
        var request: [String: Any] = ["op": op]
        if let data { request["data"] = try JSONSerialization.jsonObject(with: data) }
        var line = try JSONSerialization.data(withJSONObject: request)
        line.append(0x0A)
        try line.withUnsafeBytes { raw in
            var offset = 0
            while offset < raw.count {
                let n = write(fd, raw.baseAddress! + offset, raw.count - offset)
                guard n > 0 else { throw DaemonError(message: "daemon connection lost") }
                offset += n
            }
        }
        let reply = try JSONSerialization.jsonObject(with: try readLine()) as? [String: Any]
        guard reply?["ok"] as? Bool == true else {
            throw DaemonError(message: reply?["error"] as? String ?? "daemon error")
        }
        return try JSONSerialization.data(
            withJSONObject: reply?["data"] ?? [String: Any](), options: .fragmentsAllowed)
    }

    private func readLine() throws -> Data {
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            if let end = pending.firstIndex(of: 0x0A) {
                let line = pending[pending.startIndex..<end]
                pending = Data(pending[(end + 1)...])
                return Data(line)
            }
            let n = read(fd, &buffer, buffer.count)
            guard n > 0 else { throw DaemonError(message: "daemon connection lost") }
            pending.append(contentsOf: buffer[0..<n])
        }
    }
}

/// Runs the app's own acmd for a command, off the main thread.
enum Acmd {
    /// Runs it with args, then done(succeeded, output) on the main actor. It has
    /// no terminal, so a command that needs an admin password asks through
    /// macOS's dialog.
    nonisolated static func run(_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) {
        DispatchQueue.global().async {
            let process = Process()
            let bin = DaemonPaths.acmd
            if bin.hasPrefix("/") {
                process.executableURL = URL(fileURLWithPath: bin)
                process.arguments = args
            } else {
                process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
                process.arguments = [bin] + args
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
