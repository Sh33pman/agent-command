import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// Nothing here may reach a real daemon, or it would read and change the person's real
/// Inbox. Every test that uses the store calls this first: the runtime folder is empty,
/// so calls fail without doing anything, and start() is never called.
func isolateFromTheRealDaemon() throws {
    let dir = NSTemporaryDirectory() + "acmd-app-tests-\(getpid())"
    try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    setenv("ACMD_RUNTIME_DIR", dir, 1)
    try #require(DaemonPaths.socket == dir + "/daemon.sock")
    try #require(!FileManager.default.fileExists(atPath: DaemonPaths.socket))
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
