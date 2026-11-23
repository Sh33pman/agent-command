import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// Tests that use SpeechController.shared, which the Inbox also stops as it moves. They
/// take turns, or one suite would stop the other's read.
@Suite(.serialized)
enum UsesSharedSpeech {}

/// Nothing here may reach a real daemon, or it would read and change the person's real
/// Inbox. Every test that uses the store calls this first: the runtime folder is its own,
/// and the store's follow loop, the only thing that starts a daemon, is never started.
func isolateFromTheRealDaemon() throws {
    // Short: unix socket paths are limited to 104 bytes.
    let dir = "/tmp/acmdt-\(getpid())"
    try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    setenv("ACMD_RUNTIME_DIR", dir, 1)
    try #require(DaemonPaths.socket == dir + "/daemon.sock")
}

/// A session as the daemon sends it. Like the daemon, it says why the session
/// needs you (internal/core Attention), waiting since `since`.
func makeSession(
    id: String, name: String = "", cwd: String = "/tmp", harness: String = "claude",
    status: String = "done", claude: String = "", unseen: Bool = false, since: String = "2026-11-20T09:00:00Z",
    agent: String? = nil, capabilities: [String]? = nil
) throws -> Session {
    var fields: [String: Any] = [
        "id": id, "name": name.isEmpty && cwd == "/tmp" ? id : name, "cwd": cwd, "status": status, "kind": "hosted",
        "unseen": unseen, "harness_kind": harness, "claude_session_id": claude,
    ]
    switch (status, unseen) {
    case ("waiting", _): fields["attention"] = ["rank": 1, "reason": "waiting", "since": since]
    case ("error", _): fields["attention"] = ["rank": 2, "reason": "error", "since": since]
    case ("done", true): fields["attention"] = ["rank": 3, "reason": "unseen_done", "since": since]
    default: break
    }
    // What the daemon says it is and what can be done with it (internal/core/caps.go),
    // for a session on this Mac.
    let runs = agent ?? (harness == "terminal" ? (claude.isEmpty ? "none" : "claude") : (harness.isEmpty ? "claude" : harness))
    fields["agent"] = runs
    fields["capabilities"] = capabilities ?? {
        if status == "archived" { return harness == "terminal" ? ["purge"] : ["unarchive", "purge", "open_in_editor"] }
        var caps = ["attach", "archive", "set_remote"]
        if !cwd.isEmpty { caps.append("open_in_editor") }
        if status == "paused" || status == "finished" { caps.append("resume") }
        if (runs == "claude" && (harness != "terminal" || !claude.isEmpty)) || (runs == "antigravity" && !claude.isEmpty) {
            caps.append("continue_in_job")
        }
        return caps
    }()
    return try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
}

func makeItem(
    _ id: Int64, session: String, kind: String = "done", options: [String] = [], read: Bool = false, host: String = ""
) throws -> InboxItem {
    let fields: [String: Any] = [
        "id": id, "session_id": session, "kind": kind, "text": "message \(id)", "options": options,
        "read": read, "state": "open", "host": host,
    ]
    return try JSONDecoder().decode(InboxItem.self, from: JSONSerialization.data(withJSONObject: fields))
}

enum Key {
    static let up: UInt16 = 126, down: UInt16 = 125, left: UInt16 = 123, right: UInt16 = 124
    static let returnKey: UInt16 = 36, escape: UInt16 = 53
    static let h: UInt16 = 4, j: UInt16 = 38, k: UInt16 = 40, l: UInt16 = 37
    static let one: UInt16 = 18
}

func keyDown(_ code: UInt16, _ characters: String = "", flags: NSEvent.ModifierFlags = [], isRepeat: Bool = false) throws -> NSEvent {
    try #require(NSEvent.keyEvent(
        with: .keyDown, location: .zero, modifierFlags: flags, timestamp: 0, windowNumber: 0, context: nil,
        characters: characters, charactersIgnoringModifiers: characters, isARepeat: isRepeat, keyCode: code))
}

/// Runs what's waiting on the main queue, as the store hands its results back there.
@MainActor
func until(_ what: String = "the result", sourceLocation: SourceLocation = #_sourceLocation, _ done: () -> Bool) async throws {
    for _ in 0..<300 {
        if done() { return }
        try await Task.sleep(for: .milliseconds(10))
    }
    Issue.record("timed out waiting for \(what)", sourceLocation: sourceLocation)
}

/// Stands in for the daemon on the test's own socket: it speaks the same protocol, says
/// what it's told to, and records what it was asked.
final class FakeDaemon: @unchecked Sendable {
    private let lock = NSLock()
    private var listener: Int32 = -1
    private let path: String
    private var requests: [(op: String, data: [String: Any])] = []
    private var replies: [String: Any] = [:]
    private var failures: [String: String] = [:]
    private var clients: [Int32] = []
    /// The events it sends after a subscribe's OK.
    private var stream: [[String: Any]] = []

    func streams(_ events: [[String: Any]]) { lock.withLock { stream = events } }
    /// What it says in its hello.
    var proto = DaemonConnection.proto

    init() throws {
        try isolateFromTheRealDaemon()
        path = DaemonPaths.socket
        unlink(path)
        listener = socket(AF_UNIX, SOCK_STREAM, 0)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: Array(path.utf8)) }
        let bound = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(listener, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        try #require(bound == 0)
        try #require(listen(listener, 8) == 0)
        let fd = listener
        Thread.detachNewThread { [weak self] in
            while true {
                let client = accept(fd, nil, nil)
                if client < 0 { return }
                var on: Int32 = 1
                setsockopt(client, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
                self?.lock.withLock { self?.clients.append(client) }
                Thread.detachNewThread { self?.serve(client) }
            }
        }
    }

    /// Goes away, as a daemon does when it stops: connections end too.
    func stop() {
        shutdown(listener, SHUT_RDWR)
        close(listener)
        unlink(path)
        rmdir((path as NSString).deletingLastPathComponent)
        lock.withLock {
            clients.forEach { shutdown($0, SHUT_RDWR) }
            clients = []
        }
    }

    /// What it answers to an op from now on.
    func reply(_ op: String, _ data: Any) {
        lock.withLock { replies[op] = data }
    }

    /// The answer to op when it asks for the page at cursor: other pages get
    /// op's own reply.
    func reply(_ op: String, cursor: String, _ data: Any) {
        lock.withLock { replies[op + "#" + cursor] = data }
    }

    func fail(_ op: String, _ message: String) {
        lock.withLock { failures[op] = message }
    }

    /// What it has been asked, apart from saying hello.
    func received(_ op: String) -> [[String: Any]] {
        lock.withLock { requests.filter { $0.op == op }.map(\.data) }
    }

    private func serve(_ client: Int32) {
        defer {
            // Off the list first: once closed, this number can mean something else.
            lock.withLock { clients.removeAll { $0 == client } }
            close(client)
        }
        var pending = Data()
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let n = read(client, &buffer, buffer.count)
            if n <= 0 { return }
            pending.append(contentsOf: buffer[0..<n])
            while let end = pending.firstIndex(of: 0x0A) {
                let line = Data(pending[pending.startIndex..<end])
                pending = Data(pending[(end + 1)...])
                guard let request = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any],
                    let op = request["op"] as? String
                else { return }
                var answer: [String: Any] = ["ok": true]
                lock.withLock {
                    if op == "hello" {
                        answer["data"] = ["proto": proto, "version": "fake"]
                        return
                    }
                    requests.append((op, request["data"] as? [String: Any] ?? [:]))
                    if let failure = failures[op] {
                        answer = ["ok": false, "error": failure]
                    } else if let cursor = (request["data"] as? [String: Any])?["cursor"] as? String,
                              let data = replies[op + "#" + cursor] {
                        answer["data"] = data
                    } else if let data = replies[op] {
                        answer["data"] = data
                    }
                }
                var lines = [answer]
                if op == "subscribe", answer["ok"] as? Bool == true { lines += lock.withLock { stream } }
                for line in lines {
                    guard var out = try? JSONSerialization.data(withJSONObject: line) else { return }
                    out.append(0x0A)
                    _ = out.withUnsafeBytes { write(client, $0.baseAddress!, $0.count) }
                }
            }
        }
    }
}

/// In tests, a number is this Mac's Inbox item with that id.
extension InboxKey: ExpressibleByIntegerLiteral {
    public init(integerLiteral id: Int64) { self.init(host: "", id: id) }
}

/// The ids a mutation targets, as sent: [{host?, id}].
func targetIDs(_ m: [String: Any]) -> [String]? {
    (m["targets"] as? [[String: Any]])?.compactMap { $0["id"] as? String }
}

/// The hosts a mutation's targets are on ("" for this Mac).
func targetHosts(_ m: [String: Any]) -> [String]? {
    (m["targets"] as? [[String: Any]])?.map { $0["host"] as? String ?? "" }
}

/// The Inbox items a mutation names, by id.
func itemIDs(_ m: [String: Any]) -> [Int]? {
    (m["items"] as? [[String: Any]])?.compactMap { $0["id"] as? Int }
}
