import Foundation
import Testing

@testable import AgentCommand

// The sidebar is built from the daemon's structure (groups, sets) and this app's
// own options (D2): grouping, sort, collapse and the numbers are the app's.

private func session(_ id: String, group: String = "default", name: String? = nil, status: String = "done",
                     cwd: String = "/tmp", activity: String? = nil) throws -> Session {
    var s = try makeSession(id: id, name: name ?? id, cwd: cwd, status: status)
    s.groupID = group
    s.lastEventAt = activity
    return s
}

private let groups = [
    SessionGroup(id: "default", name: "Default", isDefault: true),
    SessionGroup(id: "web", name: "web", color: "green", order: 20),
    SessionGroup(id: "api", name: "api", color: "blue", order: 10),
    SessionGroup(id: "old", name: "old", archivedAt: "2026-11-01T00:00:00Z", order: 5),
]

/// Rows as "[group] id id", for reading at a glance.
private func render(_ rows: [SidebarRow]) -> String {
    rows.map { row in
        switch row {
        case .header(let h): "[\(h.group.name)\(h.collapsed ? "+" : "")]"
        case .session(let s, _): s.id
        }
    }.joined(separator: " ")
}

@Test func groupsShowInTheOrderYouArrangedThemWithDefaultLast() throws {
    let sessions = try [session("a", group: "web"), session("b"), session("c", group: "api"), session("d", group: "old")]
    let rows = SidebarLayout.rows(sessions: sessions, groups: groups, sets: [], options: SidebarOptions())
    // d's group is archived: it shows in Default. An empty group still shows, to drop into.
    #expect(render(rows) == "[api] c [web] a [Default] b d")
    let empty = SidebarLayout.rows(sessions: try [session("a", group: "web")], groups: groups, sets: [], options: SidebarOptions())
    #expect(render(empty) == "[api] [web] a", "Default with nothing in it is left out")
}

@Test func withOnlyDefaultOrInListModeThereAreNoHeaders() throws {
    let sessions = try [session("a", group: "web"), session("b")]
    let onlyDefault = [groups[0]]
    #expect(render(SidebarLayout.rows(sessions: sessions, groups: onlyDefault, sets: [], options: SidebarOptions())) == "a b")
    var list = SidebarOptions()
    list.mode = .list
    #expect(render(SidebarLayout.rows(sessions: sessions, groups: groups, sets: [], options: list)) == "a b")
}

@Test func aCollapsedGroupShowsItsHeaderWithWhatNeedsYou() throws {
    let sessions = try [session("a", group: "api"), session("w", group: "api", status: "waiting"), session("b")]
    var options = SidebarOptions()
    options.collapsed = ["api"]
    let rows = SidebarLayout.rows(sessions: sessions, groups: groups, sets: [], options: options)
    #expect(render(rows) == "[api+] [web] [Default] b")
    guard case .header(let header) = rows[0] else { Issue.record("no header"); return }
    #expect(header.count == 2 && header.urgent?.reason == "waiting")
    #expect(SidebarLayout.sessionIDs(rows) == ["b"], "a collapsed group's sessions aren't numbered")

    // While filtering, matches show even in a collapsed group, and empty groups go.
    let filtered = SidebarLayout.rows(sessions: sessions, groups: groups, sets: [], options: options, filtering: true)
    #expect(render(filtered) == "[api] a w [Default] b")
}

@Test func eachSortKeepsTiesInCreationOrder() throws {
    let sessions = try [
        session("1", name: "beta", status: "busy", cwd: "/w/zeta", activity: "2026-11-20T09:00:02Z"),
        session("2", name: "alpha 10", status: "waiting", cwd: "/w/api", activity: "2026-11-20T09:00:01.5Z"),
        session("3", name: "alpha 9", status: "done", cwd: "/w/api", activity: "2026-11-20T09:00:03Z"),
    ]
    func order(_ sort: SidebarOptions.Sort, descending: Bool = false) -> [String] {
        SidebarLayout.sort(sessions, by: sort, descending: descending).map(\.id)
    }
    #expect(order(.created) == ["1", "2", "3"])
    #expect(order(.created, descending: true) == ["1", "2", "3"], "created ties are all ties: kept in creation order")
    #expect(order(.name) == ["3", "2", "1"], "numbers in names sort naturally: alpha 9 before alpha 10")
    #expect(order(.activity) == ["2", "1", "3"])
    #expect(order(.activity, descending: true) == ["3", "1", "2"])
    #expect(order(.status) == ["2", "1", "3"], "waiting, then busy, then done")
    #expect(order(.folder) == ["3", "2", "1"], "by folder name, then by name")
}

@Test func aSetStaysTogetherWhereverItsFirstMemberSorts() throws {
    let sessions = try [session("a", name: "c"), session("b", name: "a"), session("x", name: "b")]
    var options = SidebarOptions()
    options.sort = .name
    let rows = SidebarLayout.rows(sessions: sessions, groups: [groups[0]], sets: [["a", "b"]], options: options)
    // By name: b (a), x (b), a (c). The set a,b goes where b sorts, in the set's order.
    #expect(render(rows) == "a b x")
    let links: [SetLink] = rows.compactMap { if case .session(_, let link) = $0 { link } else { nil } }
    #expect(links == [SetLink.first, .last, .none])
}

@Test func theStatusOrderMatchesTheCLIs() throws {
    let ranks = try ["waiting", "error", "busy", "starting", "paused", "archived"].map {
        SidebarLayout.statusRank(try makeSession(id: $0, status: $0))
    }
    #expect(ranks == [0, 1, 2, 5, 6, 7])
    #expect(SidebarLayout.statusRank(try makeSession(id: "d", status: "done", unseen: true)) == 3)
    #expect(SidebarLayout.statusRank(try makeSession(id: "d", status: "done")) == 4)
}

@Test func optionsAreKeptForNextTime() throws {
    let defaults = try #require(UserDefaults(suiteName: "acmd.tests.\(UUID().uuidString)"))
    #expect(SidebarOptions.load(defaults) == SidebarOptions(), "groups, by creation, before anything is chosen")
    var options = SidebarOptions()
    options.mode = .list
    options.sort = .activity
    options.descending = true
    options.collapsed = ["api"]
    options.save(defaults)
    #expect(SidebarOptions.load(defaults) == options)
    #expect(SidebarOptions.Mode.allCases.map(\.title) == ["Groups", "List"])
    #expect(SidebarOptions.Sort.allCases.map(\.title) == ["Created", "Name", "Activity", "Status", "Folder"])
}

@Test func droppingSessionsOnAGroupMovesThemThere() {
    let item = SidebarDrop.sessionItem("a")
    #expect(SidebarDrop.action(items: [item], onto: "api", groups: groups) == .moveSessions(["a"], to: "api"))
    #expect(SidebarDrop.action(items: [item], onto: "api", groups: groups, marked: ["a", "c"]) == .moveSessions(["a", "c"], to: "api"),
            "a marked session takes the other marked ones")
    #expect(SidebarDrop.action(items: [item], onto: "api", groups: groups, marked: ["z"]) == .moveSessions(["a"], to: "api"),
            "an unmarked one goes alone")
    #expect(SidebarDrop.action(items: [], onto: "api", groups: groups) == nil)
    #expect(SidebarDrop.action(items: ["file:///x"], onto: "api", groups: groups) == nil)
}

@Test func droppingAGroupOnAnotherPutsItBeforeIt() {
    let web = SidebarDrop.groupItem("web")
    #expect(SidebarDrop.action(items: [web], onto: "api", groups: groups) == .moveGroup("web", before: "api", after: nil))
    #expect(SidebarDrop.action(items: [SidebarDrop.groupItem("api")], onto: "default", groups: groups)
            == .moveGroup("api", before: nil, after: "web"), "onto Default: after the last group")
    #expect(SidebarDrop.action(items: [web], onto: "default", groups: groups) == nil, "already last")
    #expect(SidebarDrop.action(items: [web], onto: "web", groups: groups) == nil, "onto itself")
    #expect(SidebarDrop.action(items: [SidebarDrop.groupItem("default")], onto: "api", groups: groups) == nil, "Default stays last")
    #expect(SidebarDrop.action(items: [web], onto: "nope", groups: groups) == nil)
}

@Test func attentionAndGroupsDecodeFromTheDaemon() throws {
    let s: Session = try JSONDecoder().decode(Session.self, from: Data("""
        {"id":"a","name":"a","cwd":"/","status":"waiting","kind":"hosted","unseen":false,"harness":"claude",
         "claude_session_id":"","group_id":"api","last_event_at":"2026-11-20T09:00:00Z",
         "attention":{"rank":1,"reason":"waiting","since":"2026-11-20T08:59:00.25Z"}}
        """.utf8))
    #expect(s.groupID == "api" && s.attention?.rank == 1 && s.attention?.reason == "waiting")
    #expect(s.attention?.sinceDate == Timestamps.date("2026-11-20T08:59:00.25Z"))
    #expect(SessionAttention(rank: 1, reason: "x", since: "not a time").sinceDate == .distantPast)
    let g: SessionGroup = try JSONDecoder().decode(SessionGroup.self, from: Data("""
        {"type":"group","id":"api","name":"api","color":"blue","order":10,"is_default":false}
        """.utf8))
    #expect(g.color == "blue" && g.order == 10 && !g.isArchived)
}

@MainActor
@Test func theStoreCollapsesGroupsAndKeepsItsView() throws {
    let store = SessionStore()
    store.defaults = try #require(UserDefaults(suiteName: "acmd.tests.\(UUID().uuidString)"))
    store.filter = .all
    store.groups = groups
    store.sessions = try [session("a", group: "api"), session("b")]
    #expect(store.sidebarIDs == ["a", "b"])
    store.toggleCollapsed("api")
    #expect(store.sidebarIDs == ["b"] && SidebarOptions.load(store.defaults).collapsed == ["api"], "kept for next time")
    #expect(store.sessionID(number: 1) == "b", "numbers count what you can see")
    store.toggleCollapsed("api")
    #expect(store.sidebarIDs == ["a", "b"])
}

// What the app asks the daemon for. These share the test socket, so they take
// turns with the store's other daemon tests.
extension UsesSharedSpeech {
    @Suite @MainActor
    struct GroupCallsTests {
        private func sent(_ daemon: FakeDaemon, count: Int) async throws -> [[String: Any]] {
            try await until("the mutations") { daemon.received("mutate").count >= count }
            return daemon.received("mutate")
        }

        @Test func groupChangesNameTheGroupByID() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let store = SessionStore()
            store.groups = [SessionGroup(id: "api", name: "API"), SessionGroup(id: "web", name: "Web")]
            store.renameGroup("api", to: "backend")
            store.setGroupColor("api", "red")
            store.deleteGroup("web")
            store.moveSessions(["a", "b"], to: "api")
            let all = try await sent(daemon, count: 4)
            #expect(all.map { $0["kind"] as? String } == ["group.rename", "group.color", "group.delete", "session.move"])
            #expect(all[0]["group_id"] as? String == "api" && all[0]["text"] as? String == "backend")
            #expect(all[1]["color"] as? String == "red")
            #expect(targetIDs(all[3]) == ["a", "b"] && all[3]["group_id"] as? String == "api")
            #expect(all.allSatisfy { $0["host"] == nil }, "this Mac's groups name no host")
            #expect(all.allSatisfy { $0["flag"] == nil }, "no group is moved up or down by a flag")
        }

        @Test func movingAGroupNamesItsNeighbour() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let store = SessionStore()
            store.groups = groups
            store.moveGroup("web", by: -1) // above api
            store.moveGroup("api", by: 1) // below web
            store.moveGroup("api", by: -1) // already first: nothing
            store.perform(.moveGroup("web", before: nil, after: "api"))
            let all = try await sent(daemon, count: 3)
            #expect(all.count == 3)
            #expect(all[0]["before"] as? String == "api" && all[0]["after"] == nil)
            #expect(all[1]["after"] as? String == "web")
            #expect(all[2]["group_id"] as? String == "web" && all[2]["after"] as? String == "api")
        }

        @Test func aNewGroupIsMadeThenFilled() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("mutate", ["id": "g1", "name": "fresh"])
            let store = SessionStore()
            store.createGroup("fresh", moving: ["a"])
            let all = try await sent(daemon, count: 2)
            #expect(all[0]["kind"] as? String == "group.create" && all[0]["text"] as? String == "fresh")
            #expect(all[1]["kind"] as? String == "session.move" && all[1]["group_id"] as? String == "g1")
            #expect(targetIDs(all[1]) == ["a"])
        }

        @Test func aDropOfSessionsMovesThemAndClearsTheMarks() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let store = SessionStore()
            store.sessions = [try makeSession(id: "a")]
            store.groups = groups
            store.setSidebarFocused(true)
            store.setCursor("a")
            store.toggleMarkAtCursor()
            store.perform(.moveSessions(["a"], to: "api"))
            #expect(store.markedIDs.isEmpty)
            let all = try await sent(daemon, count: 1)
            #expect(all[0]["kind"] as? String == "session.move")
        }
    }
}

@Test func rowsHaveIDsThatNeverClash() throws {
    // A group and a session with the same id are still two rows to the list.
    let sessions = try [session("api", group: "api")]
    let rows = SidebarLayout.rows(sessions: sessions, groups: groups, sets: [], options: SidebarOptions())
    #expect(rows.map(\.id) == ["group:api", "api", "group:web"])
    #expect(Set(rows.map(\.id)).count == rows.count)
}

/// A host's tree comes after this Mac's groups, as the host arranges it: its
/// groups named with the host, its Default as the host itself, and its sessions
/// only ever in its own groups.
@Test func eachHostsTreeFollowsThisMacsGroups() throws {
    let groups = [
        SessionGroup(id: "default", name: "Default", isDefault: true),
        SessionGroup(id: "mine", name: "Mine", order: 1),
        SessionGroup(id: "s-default", name: "Default", isDefault: true, host: "studio"),
        SessionGroup(id: "s-api", name: "API", order: 2, host: "studio"),
        SessionGroup(id: "b-ops", name: "Ops", order: 1, host: "box"),
    ]
    var onStudio = try makeRemoteSession(id: "h1", host: "studio")
    onStudio.groupID = "s-api"
    var lost = try makeRemoteSession(id: "h2", host: "studio")
    lost.groupID = "gone" // a group the host no longer has: its own Default
    var onBox = try makeRemoteSession(id: "h3", host: "box")
    onBox.groupID = "b-ops"
    let rows = SidebarLayout.rows(sessions: [try session("a", group: "mine"), onStudio, lost, onBox], groups: groups, sets: [],
                                  options: SidebarOptions())
    let titles = rows.compactMap { row -> String? in
        if case .header(let h) = row { return h.group.title }
        return nil
    }
    #expect(titles == ["Mine", "Ops · box", "API · studio", "studio"])
    #expect(SidebarLayout.sessionIDs(rows) == ["a", "h3", "h1", "h2"])
    #expect(SidebarLayout.groups(for: onStudio, in: groups).map(\.id) == ["s-api", "s-default"], "only its host's groups")
    #expect(SidebarLayout.groups(for: try session("a"), in: groups).map(\.id) == ["mine", "default"])
    let g: SessionGroup = try JSONDecoder().decode(SessionGroup.self, from: Data(#"{"id":"x","name":"N","host":"studio"}"#.utf8))
    #expect(g.host == "studio" && g.title == "N · studio")
    // Moving a host's sessions offers only that host's groups.
    let entries = Palette.groupEntries([onStudio], section: "s", groups: groups).map(\.title)
    #expect(entries.contains("Move to studio") && !entries.contains("Move to Mine") && !entries.contains { $0.contains("box") })
}

// A host's groups have their host's ids, and every host's Default is
// "default": the app tells them apart by key, and names the host in ops.
extension UsesSharedSpeech.GroupCallsTests {
    @Test func aHostsDefaultIsNotThisMacs() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        let store = SessionStore()
        let mine = SessionGroup(id: "default", name: "Default", isDefault: true)
        let theirs = SessionGroup(id: "default", name: "Default", isDefault: true, host: "studio")
        store.groups = [mine, theirs]
        var here = try makeSession(id: "a")
        here.groupID = "other"
        var there = try makeRemoteSession(id: "h", host: "studio")
        there.groupID = "other"
        store.sessions = [here, there]
        #expect(mine.key != theirs.key && store.group(key: theirs.key)?.host == "studio")
        store.moveSessions(["a", "h"], to: mine.key) // only this Mac's moves into this Mac's
        store.moveSessions(["a", "h"], to: theirs.key) // only studio's into studio's
        let all = try await sent(daemon, count: 2)
        #expect(targetIDs(all[0]) == ["a"] && all[0]["host"] == nil && all[0]["group_id"] as? String == "default")
        #expect(targetIDs(all[1]) == ["h"] && targetHosts(all[1]) == ["studio"] && all[1]["host"] as? String == "studio")

        // The sidebar: each Default shows its own machine's sessions.
        here.groupID = "default"
        there.groupID = "default"
        let rows = SidebarLayout.rows(sessions: [here, there], groups: [mine, theirs], sets: [], options: SidebarOptions())
        var under: [String: [String]] = [:]
        var current = ""
        for row in rows {
            switch row {
            case .header(let h): current = h.group.key
            case .session(let s, _): under[current, default: []].append(s.id)
            }
        }
        #expect(under[mine.key] == ["a"] && under[theirs.key] == ["h"])
        #expect(Set(rows.map(\.id)).count == rows.count, "every row its own id")
    }
}
