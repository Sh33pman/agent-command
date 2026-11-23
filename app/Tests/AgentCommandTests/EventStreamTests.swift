import Foundation
import Testing

@testable import AgentCommand

private func session(_ id: String, _ name: String = "", status: String = "busy") -> [String: Any] {
    ["id": id, "name": name.isEmpty ? id : name, "cwd": "/w", "status": status, "kind": "hosted", "harness_kind": "terminal"]
}

/// The app keeps the daemon's state from its stream: a snapshot, then each
/// entity added, updated whole, or removed.
@Test func theStreamKeepsTheState() throws {
    var state = SnapshotAssembler()
    do { let ok = state.apply(["rev": 1, "type": "added", "entity": "session", "id": "a", "data": session("a")]); #expect(!ok, "nothing before a snapshot") }
    do { let ok = state.apply(["rev": 5, "type": "snapshot", "snapshot": ["sessions": [session("a")], "groups": [["id": "g", "name": "G"]]]]); #expect(ok) }
    #expect(state.hasSnapshot && state.rev == 5)
    do { let ok = state.apply(["rev": 6, "type": "added", "entity": "session", "id": "b", "data": session("b")]); #expect(ok) }
    do { let ok = state.apply(["rev": 7, "type": "updated", "entity": "session", "id": "a", "data": session("a", "renamed", status: "done")]); #expect(ok) }
    do { let ok = state.apply(["rev": 8, "type": "removed", "entity": "group", "id": "g"]); #expect(ok) }
    do { let ok = state.apply(["rev": 9, "type": "updated", "entity": "dismissed", "data": ["hooks"]]); #expect(ok) }
    let item: [String: Any] = [
        "id": 4, "session_id": "a", "kind": "done", "text": "t", "state": "open", "at": "2026-11-22T00:00:00Z", "read": false,
    ]
    do { let ok = state.apply(["rev": 10, "type": "added", "entity": "inbox", "id": "4", "data": item]); #expect(ok) }
    do { let ok = state.apply(["rev": 11, "type": "updated", "entity": "view", "data": ["mode": "list", "sort": "name"]]); #expect(ok) }
    let snap = try state.snapshot()
    #expect(snap.sessions?.map(\.id) == ["a", "b"] && snap.sessions?.first?.name == "renamed" && snap.sessions?.first?.status == "done")
    #expect(snap.groups?.isEmpty == true && snap.dismissed == ["hooks"] && snap.inbox?.first?.itemID == 4)
    #expect(state.rev == 11)

    // Removing an inbox item by its id; an entity the app doesn't know is kept
    // with its type, for its children; nonsense.
    do { let ok = state.apply(["rev": 12, "type": "removed", "entity": "inbox", "id": "4"]); #expect(ok) }
    #expect(try state.snapshot().inbox?.isEmpty == true)
    do { let ok = state.apply(["rev": 13, "type": "added", "entity": "folder", "id": "f", "data": ["id": "f", "parent": "g"]]); #expect(ok) }
    #expect(try state.snapshot().others == [try JSONDecoder().decode(EntityRef.self, from: Data(#"{"type":"folder","id":"f","parent":"g"}"#.utf8))])
    do { let ok = state.apply(["rev": 13, "type": "removed", "entity": "folder", "id": "f"]); #expect(ok) }
    #expect(try state.snapshot().others?.isEmpty == true)
    do { let ok = state.apply(["rev": 13, "type": "removed", "entity": "folder", "id": "never"]); #expect(!ok) }
    do { let ok = state.apply(["rev": 13, "type": "added", "entity": "folder", "data": [String: Any]()]); #expect(!ok, "no id") }
    do { let ok = state.apply(["rev": 14, "type": "removed", "entity": "session", "id": "nope"]); #expect(!ok) }
    do { let ok = state.apply(["rev": 15, "type": "added", "entity": "session", "id": "c", "data": "not an object"]); #expect(!ok) }
    do { let ok = state.apply(["rev": 16, "type": "shuffled", "entity": "session", "id": "a"]); #expect(!ok) }
    do { let ok = state.apply(["rev": 17, "type": "resync"]); #expect(!ok, "a new snapshot follows") }
    do { let ok = state.apply(["rev": 18, "type": "removed", "entity": "speech"]); #expect(ok) }
    do { let ok = state.apply(["rev": 19, "type": "snapshot", "snapshot": ["sessions": [session("z")]]]); #expect(ok) }
    #expect(try state.snapshot().sessions?.map(\.id) == ["z"], "a new snapshot replaces what was there")
}

/// A connection becomes the stream: the subscribe's OK, then one event a line.
@Test func aConnectionFollowsTheStream() throws {
    let daemon = try FakeDaemon()
    defer { daemon.stop() }
    daemon.streams([
        ["rev": 3, "type": "snapshot", "snapshot": ["sessions": [session("a")]]],
        ["rev": 4, "type": "added", "entity": "session", "id": "b", "data": session("b")],
    ])
    let conn = try DaemonConnection(socket: DaemonPaths.socket)
    try conn.subscribe(since: 2)
    #expect(daemon.received("subscribe").first?["since"] as? Int == 2)
    var state = SnapshotAssembler()
    do { let ok = state.apply(try conn.nextEvent()); #expect(ok) }
    do { let ok = state.apply(try conn.nextEvent()); #expect(ok) }
    #expect(try state.snapshot().sessions?.map(\.id) == ["a", "b"] && !conn.hasLineWaiting)
}
