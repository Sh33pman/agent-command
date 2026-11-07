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

func makeSession(
    id: String, name: String = "", cwd: String = "/tmp", harness: String = "claude",
    status: String = "done", claude: String = ""
) throws -> Session {
    let fields: [String: Any] = [
        "ID": id, "Name": name.isEmpty && cwd == "/tmp" ? id : name, "Cwd": cwd, "Status": status, "Kind": "hosted",
        "Unseen": false, "Harness": harness, "ClaudeSessionID": claude,
    ]
    return try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
}

func makeItem(
    _ id: Int64, session: String, kind: String = "done", options: [String] = [], read: Bool = false
) throws -> InboxItem {
    let fields: [String: Any] = [
        "id": id, "session_id": session, "kind": kind, "text": "message \(id)", "options": options,
        "read": read, "state": "open",
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
    /// What it says in its hello.
    var proto = 1

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
                    } else if let data = replies[op] {
                        answer["data"] = data
                    }
                }
                guard var out = try? JSONSerialization.data(withJSONObject: answer) else { return }
                out.append(0x0A)
                _ = out.withUnsafeBytes { write(client, $0.baseAddress!, $0.count) }
            }
        }
    }
}
