import Foundation
import Testing

@testable import AgentCommand

// Sets are the daemon's: the panes follow what it says (reconcile), keep only
// where each set's panes are, and move the sets saved here before to it once.

/// Panes showing a set of the sessions, split right in order, with the last focused.
private func showing(_ sessions: [String], set id: String? = "S") -> PaneSets {
    var p = PaneSets()
    for session in sessions { p.split(.right, session: session, setID: id) }
    return p
}

private func panes(_ tree: PaneTree) -> [String] { tree.leaves.compactMap(\.session) }

@Test func aSetFromTheDaemonFitsThePanesShowingIt() {
    var p = showing(["a", "b"])
    p.reconcile(sets: ["S": ["a", "c", "b"]]) // c split beside a, on another client
    #expect(panes(p.current) == ["a", "c", "b"])
    #expect(p.current.focusedSession == "b", "the focus stays where it was")
    #expect(p.currentSetID == "S")

    p.reconcile(sets: ["S": ["a", "b"]]) // c moved out elsewhere
    #expect(panes(p.current) == ["a", "b"])
}

@Test func aSetTheDaemonEndedLeavesTheFocusedSessionAlone() {
    var p = showing(["a", "b", "c"])
    p.select(session: "b")
    p.reconcile(sets: [:]) // ended with `set.end`, or archived down to one
    #expect(p.currentSetID == nil)
    #expect(panes(p.current) == ["b"])
    #expect(p.current.focusedSession == "b")
    #expect(p.members.isEmpty)
}

@Test func aSessionShowingAloneThatJoinsASetShowsTheSet() {
    // A split whose session the snapshot brings before the split's own reply.
    var p = PaneSets()
    p.select(session: "a")
    p.reconcile(sets: ["S": ["a", "b"]])
    #expect(p.currentSetID == "S")
    #expect(panes(p.current) == ["a", "b"])
    // The split's reply then only focuses the pane that's already there.
    p.split(.right, session: "b", setID: "S")
    #expect(panes(p.current) == ["a", "b"] && p.current.focusedSession == "b")
}

@Test func aSetMadeElsewhereGetsPanesAside() {
    var p = PaneSets()
    p.select(session: "z")
    p.reconcile(sets: ["T": ["x", "y"]])
    #expect(panes(p.current) == ["z"], "what's showing stays")
    #expect(p.others.map(\.id) == ["T"])
    #expect(p.others.first.map { panes($0.tree) } == ["x", "y"])
    p.select(session: "y")
    #expect(p.currentSetID == "T" && panes(p.current) == ["x", "y"])
}

@Test func aSetAsideFollowsTheDaemonToo() {
    var p = showing(["a", "b"])
    p.select(session: "z") // the set goes aside
    p.reconcile(sets: ["S": ["a", "b", "c"]])
    #expect(p.others.first.map { panes($0.tree) } == ["a", "b", "c"])
    p.reconcile(sets: [:])
    #expect(p.others.isEmpty, "an ended set's panes go")
}

@Test func aSessionMovingBetweenSetsLeavesOneForTheOther() {
    var p = showing(["a", "b", "c"])
    p.reconcile(sets: ["S": ["a", "b"], "T": ["c", "d"]])
    #expect(panes(p.current) == ["a", "b"])
    #expect(p.others.first { $0.id == "T" }.map { panes($0.tree) } == ["c", "d"])
}

@Test func setsSavedBeforeMoveToTheDaemonOnePerHost() {
    var p = showing(["a", "h1", "b", "h2", "gone"], set: "S")
    p.select(session: "x")
    p.split(.right, session: "y", setID: "T")
    let hosts = ["a": "", "b": "", "h1": "studio", "h2": "studio", "x": "", "y": "mini"]
    let moves = p.setsToMove { hosts[$0] }
    // S: this Mac's two keep S's id; studio's two get a new one. T mixes hosts with
    // one each: no set. A session that's gone is left out.
    #expect(moves.count == 2)
    #expect(moves.first?.id == "S" && moves.first?.members == ["a", "b"])
    #expect(moves.last?.members == ["h1", "h2"] && moves.last?.id != "S" && moves.last?.id != "T")
}

@Test func panesAreSavedWithAVersionSoTheMoveHappensOnce() throws {
    let p = showing(["a", "b"])
    let saved = try #require(p.encodedForSaving())
    let back = try #require(PaneSets.decodeSaved(saved))
    #expect(back.panes == p && !back.fromBefore)

    // The format from before has no version: its sets still have to move.
    let before = try JSONEncoder().encode(p)
    let old = try #require(PaneSets.decodeSaved(before))
    #expect(old.panes == p && old.fromBefore)

    #expect(PaneSets.decodeSaved(Data("{}".utf8)) == nil)
    #expect(PaneSets.decodeSaved(Data(#"{"version":2,"panes":{"current":{"root":{"leaf":{"id":"x"}},"focused":"nope"},"others":[]}}"#.utf8)) == nil,
            "saved panes that don't hold together are dropped")
}

@Test func aTreeAddsAPaneBesideASessionWithoutTakingTheFocus() {
    var t = PaneTree()
    t.add(session: "a", after: nil)
    #expect(panes(t) == ["a"], "an empty tree just shows it")
    t.add(session: "b", after: nil)
    t.add(session: "c", after: "a")
    #expect(panes(t) == ["a", "c", "b"])
    #expect(t.focusedSession == "a")
    t.add(session: "a", after: "b")
    #expect(panes(t) == ["a", "c", "b"], "a session already here stays where it is")
}

@MainActor
@Test func theStoreReadsSetsFromEachSessionsParent() throws {
    func session(_ id: String, parent: String?, order: Int?) throws -> Session {
        var s = try makeSession(id: id)
        s.parent = parent
        s.order = order
        return s
    }
    let sessions = try [
        session("b", parent: "S", order: 2), session("a", parent: "S", order: 1),
        session("loose", parent: "default", order: nil),
        session("x", parent: "T", order: 1), // its partner isn't listed: archived, or another kind
        session("y", parent: "unknown", order: 1),
    ]
    let known = [SessionSetEntity(type: "set", id: "S", parent: "default"), SessionSetEntity(type: "set", id: "T", parent: "g")]
    #expect(SessionStore.sets(of: sessions, known: known) == ["S": ["a", "b"]])
}

@MainActor
@Test func aSnapshotsSetsReachThePanes() async throws {
    try isolateFromTheRealDaemon()
    let store = SessionStore()
    var seen: [[String: [String]]] = []
    store.onSetsChanged = { seen.append($0) }
    func hosted(_ id: String, _ extra: [String: Any]) -> [String: Any] {
        ["id": id, "name": id, "cwd": "/", "status": "done", "kind": "hosted", "unseen": false, "harness": "terminal",
         "claude_session_id": ""].merging(extra) { $1 }
    }
    let fields: [String: Any] = [
        "sessions": [hosted("a", ["parent": "S", "order": 1]), hosted("b", ["parent": "S", "order": 2])],
        "sets": [["type": "set", "id": "S", "parent": "default"]],
    ]
    store.publish(try JSONDecoder().decode(Snapshot.self, from: JSONSerialization.data(withJSONObject: fields)))
    try await until("the sets") { !seen.isEmpty }
    #expect(store.sets == ["S": ["a", "b"]] && store.setMembers == [["a", "b"]])
    #expect(seen == [["S": ["a", "b"]]])
}

@MainActor
@Test func theAppKeepsOneClientID() throws {
    let defaults = try #require(UserDefaults(suiteName: "acmd.tests.\(UUID().uuidString)"))
    let store = SessionStore()
    store.defaults = defaults
    let id = store.clientID
    #expect(!id.isEmpty && store.clientID == id)
    let again = SessionStore()
    again.defaults = defaults
    #expect(again.clientID == id, "kept for next time")
}

// What the app asks the daemon for. These share the test socket, so they take
// turns with the store's other daemon tests.
extension UsesSharedSpeech {
    @Suite @MainActor
    struct SetCallsTests {
        private func session(_ id: String, _ extra: [String: Any] = [:]) -> [String: Any] {
            ["id": id, "name": id, "cwd": "/w", "status": "running", "kind": "hosted", "unseen": false, "harness": "terminal",
             "claude_session_id": ""].merging(extra) { $1 }
        }

        @Test func aSplitAsksTheDaemonToSetTheNewSessionWithTheFocusedOne() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("new", session("b", ["parent": "S", "order": 2]))
            let store = SessionStore()
            var placed: Session?
            store.newSession(harness: "terminal", in: "/w", setWith: "a") { placed = $0 }
            try await until("the new session") { placed != nil }
            #expect(daemon.received("new").first?["set_with"] as? String == "a")
            #expect(placed?.parent == "S" && placed?.order == 2)
            #expect(store.selectedID == nil, "a split places it in the panes instead of selecting it")

            // On its own, a new session asks for no set.
            store.newSession(harness: "terminal", in: "/w")
            try await until("the second") { daemon.received("new").count == 2 }
            #expect(daemon.received("new").last?["set_with"] == nil)
        }

        @Test func aSplitFromAHostsSessionStartsOnThatHost() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("remote.new", session("h2", ["host": "studio", "parent": "S", "order": 2]))
            let store = SessionStore()
            var placed: Session?
            store.newRemoteSession(host: "studio", dir: "/srv/api", harness: .terminal, setWith: "h1", placed: { placed = $0 })
            try await until("the host's session") { placed != nil }
            let sent = try #require(daemon.received("remote.new").first)
            #expect(sent["host"] as? String == "studio" && sent["dir"] as? String == "/srv/api")
            #expect(sent["set_with"] as? String == "h1")
            #expect(store.sessions.contains { $0.id == "h2" }, "listed before the next snapshot")
        }

        @Test func movingOutOfASetAndMovingOldSetsGoToTheDaemon() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let store = SessionStore()
            store.removeFromSet("b")
            store.createSets([("S", ["a", "b"]), ("T", ["x", "y"])])
            store.createSets([]) // nothing to move: nothing sent
            try await until("the mutations") { daemon.received("mutate").count == 3 }
            let sent = daemon.received("mutate")
            #expect(sent.first?["kind"] as? String == "set.remove" && sent.first?["id"] as? String == "b")
            let creates = sent.filter { $0["kind"] as? String == "set.create" }
            #expect(creates.map { $0["set_id"] as? String } == ["S", "T"])
            #expect(creates.first?["ids"] as? [String] == ["a", "b"])
        }

        @Test func theLayoutIsKeptUnderThisAppsID() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let store = SessionStore()
            store.defaults = try #require(UserDefaults(suiteName: "acmd.tests.\(UUID().uuidString)"))
            store.saveLayout("{}")
            try await until("the save") { !daemon.received("layout.set").isEmpty }
            let sent = try #require(daemon.received("layout.set").first)
            #expect(sent["client_id"] as? String == store.clientID && sent["json"] as? String == "{}")
        }
    }
}
