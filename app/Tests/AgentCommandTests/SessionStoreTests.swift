import AppKit
import Foundation
import Testing

@testable import AgentCommand

extension UsesSharedSpeech {
    @Suite @MainActor
    struct SessionStoreTests {
        private func store(_ ids: [String] = ["a", "b", "c", "d"]) throws -> SessionStore {
            _ = NSApplication.shared
            let store = SessionStore()
            store.sessions = try ids.map { try makeSession(id: $0, claude: "c-\($0)") }
            return store
        }

        private func snapshot(_ fields: [String: Any]) throws -> Snapshot {
            try JSONDecoder().decode(Snapshot.self, from: JSONSerialization.data(withJSONObject: fields))
        }

        private func hosted(_ id: String, status: String = "done") -> [String: Any] {
            ["ID": id, "Name": id, "Cwd": "/tmp", "Status": status, "Kind": "hosted", "Unseen": false, "Harness": "claude", "ClaudeSessionID": "c"]
        }

        private func item(_ id: Int) -> [String: Any] {
            ["id": id, "session_id": "a", "kind": "done", "state": "open"]
        }

        // MARK: The sidebar's keyboard

        @Test func theCursorStartsOnTheSelectedSessionAndStopsAtTheEnds() throws {
            let s = try store()
            s.reflect(selected: "b", visible: ["b"], sets: [], closed: [])
            s.setSidebarFocused(true)
            #expect(s.cursorID == "b")
            s.moveCursor(by: -1)
            s.moveCursor(by: -1)
            #expect(s.cursorID == "a")
            s.moveCursor(by: 10)
            #expect(s.cursorID == "d")
            s.setCursor("gone")
            s.moveCursor(by: 1)
            #expect(s.cursorID == "a", "lost, it starts again from the top")
        }

        @Test func withNothingSelectedTheCursorStartsAtTheTop() throws {
            let s = try store()
            s.setSidebarFocused(true)
            #expect(s.cursorID == "a")
            let empty = try store([])
            empty.moveCursor(by: 1)
            #expect(empty.cursorID == nil)
        }

        @Test func aSetsRowsStayTogether() throws {
            let s = try store()
            s.reflect(selected: nil, visible: [], sets: [["a", "d"]], closed: ["d"])
            #expect(s.sidebarIDs == ["a", "d", "b", "c"])
            #expect(s.closedIDs == ["d"] && s.setMembers == [["a", "d"]])
        }

        // MARK: Going to a session

        @Test func numbersCountDownTheSidebar() throws {
            let s = try store()
            s.reflect(selected: nil, visible: [], sets: [["a", "d"]], closed: [])
            #expect(s.sessionID(number: 1) == "a")
            #expect(s.sessionID(number: 2) == "d", "a set's sessions are numbered together")
            #expect(s.sessionID(number: 4) == "c")
            #expect(s.sessionID(number: 5) == nil)
            #expect(s.sessionID(number: 0) == nil)
        }

        @Test func showingASessionMovesTheCursorOnlyWhenTheSidebarHasTheKeyboard() throws {
            let s = try store()
            var shown: [String?] = []
            s.onSelect = { shown.append($0?.id) }
            s.show("c")
            #expect(s.selectedID == "c" && shown == ["c"])
            #expect(s.cursorID == nil)
            s.setSidebarFocused(true)
            s.show("b")
            #expect(s.selectedID == "b" && s.cursorID == "b")
        }

        @Test func attentionGoesToWaitingThenErrorThenUnseenFinishedTurns() throws {
            let s = try store([])
            s.sessions = try [
                makeSession(id: "done-seen", status: "done"),
                makeSession(id: "done-new", status: "done", unseen: true),
                makeSession(id: "error", status: "error"),
                makeSession(id: "busy", status: "busy", unseen: true),
                makeSession(id: "wait1", status: "waiting"),
                makeSession(id: "wait2", status: "waiting"),
            ]
            #expect(s.attentionIDs == ["wait1", "wait2", "error", "done-new"])
            #expect(s.nextAttentionID == "wait1", "with nothing selected, the most urgent")
            s.reflect(selected: "busy", visible: [], sets: [], closed: [])
            #expect(s.nextAttentionID == "wait1", "from a session that doesn't need you too")
            s.reflect(selected: "wait1", visible: [], sets: [], closed: [])
            #expect(s.nextAttentionID == "wait2")
            s.reflect(selected: "done-new", visible: [], sets: [], closed: [])
            #expect(s.nextAttentionID == "wait1", "it goes round")
        }

        @Test func attentionFollowsTheSidebarAndHasNothingWhenAllIsWell() throws {
            let s = try store([])
            s.sessions = try [makeSession(id: "w1", status: "waiting"), makeSession(id: "w2", status: "waiting")]
            s.reflect(selected: nil, visible: [], sets: [["w2", "w1"]], closed: [])
            #expect(s.attentionIDs == ["w2", "w1"])
            s.sessions = try [makeSession(id: "a", status: "busy"), makeSession(id: "b", status: "done")]
            #expect(s.nextAttentionID == nil)
            s.sessions = try [makeSession(id: "only", status: "error")]
            s.reflect(selected: "only", visible: [], sets: [], closed: [])
            #expect(s.nextAttentionID == "only", "the one that needs you, even when it's showing")
        }

        @Test func actionsApplyToMarksThenTheCursorThenTheSelection() throws {
            let s = try store()
            s.reflect(selected: "c", visible: ["c"], sets: [], closed: [])
            #expect(s.actionTargets.map(\.id) == ["c"], "the selection, with the keyboard elsewhere")
            s.setSidebarFocused(true)
            s.setCursor("d")
            #expect(s.actionTargets.map(\.id) == ["d"], "the cursor")
            s.toggleMarkAtCursor()
            s.setCursor("a")
            s.toggleMarkAtCursor()
            #expect(s.actionTargets.map(\.id) == ["a", "d"], "what's marked, in sidebar order")
            s.toggleMarkAtCursor()
            #expect(s.markedIDs == ["d"], "marking again unmarks")
        }

        @Test func aRightClickAppliesToItsRowUnlessItsMarked() throws {
            let s = try store()
            s.setSidebarFocused(true)
            s.setCursor("a")
            s.toggleMarkAtCursor()
            s.setCursor("b")
            s.toggleMarkAtCursor()
            #expect(s.actionTargets(forRow: try makeSession(id: "c")).map(\.id) == ["c"])
            #expect(s.actionTargets(forRow: try makeSession(id: "a")).map(\.id) == ["a", "b"])
        }

        @Test func marksOnlyLastWhileTheSidebarHasTheKeyboard() throws {
            let s = try store()
            #expect(s.sidebarState == nil)
            s.setSidebarFocused(true)
            s.toggleMarkAtCursor()
            let state = try #require(s.sidebarState)
            #expect(state.cursor == "a" && state.marks == ["a"])
            s.clearMarks()
            #expect(s.markedIDs.isEmpty)
            s.restoreSidebar(state)
            #expect(s.markedIDs == ["a"])
            s.setSidebarFocused(false)
            #expect(s.markedIDs.isEmpty)
        }

        @Test func noMarkWithoutACursor() throws {
            let s = try store()
            s.toggleMarkAtCursor()
            #expect(s.markedIDs.isEmpty)
        }

        // MARK: Asking the daemon

        @Test func renamingSeveralNumbersThem() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let s = try store()
            s.rename(Array(s.sessions.prefix(2)), to: "work")
            try await until { daemon.received("mutate").count == 2 }
            let sent = daemon.received("mutate")
            #expect(sent.allSatisfy { $0["kind"] as? String == "session.rename" })
            #expect(sent.compactMap { $0["text"] as? String } == ["work - 1", "work - 2"])
        }

        @Test func archivingLeavesTheCursorOnTheNextRow() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let s = try store()
            s.setSidebarFocused(true)
            s.setCursor("b")
            s.toggleMarkAtCursor()
            s.archive([try makeSession(id: "b"), try makeSession(id: "c")])
            #expect(s.cursorID == "d")
            #expect(s.markedIDs.isEmpty)
            try await until { daemon.received("mutate").count == 2 }
            s.setCursor("d")
            s.archive([try makeSession(id: "d")])
            #expect(s.cursorID == "c", "the last row falls back to the one above")
        }

        @Test func readingAndDismissingAskTheDaemon() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let s = try store()
            var finished = 0
            s.setInboxRead(7, true) { finished += 1 }
            s.dismissInbox(8) { finished += 1 }
            try await until { finished == 2 }
            let sent = daemon.received("mutate")
            #expect(sent.contains { $0["kind"] as? String == "inbox.read" && ($0["item_ids"] as? [Int]) == [7] })
            #expect(sent.contains { $0["kind"] as? String == "inbox.dismiss" && ($0["item_ids"] as? [Int]) == [8] })
        }

        @Test func anAnswerReachesTheDaemonOrSaysWhyNot() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let s = try store()
            var result: String??
            s.answerInbox(5, option: 2, text: nil) { result = .some($0) }
            try await until { result != nil }
            #expect(result == .some(nil))
            let sent = try #require(daemon.received("inbox.answer").first)
            #expect(sent["id"] as? Int == 5 && sent["option"] as? Int == 2)
            daemon.fail("inbox.answer", "it moved on")
            result = nil
            s.answerInbox(5, option: nil, text: "hello") { result = .some($0) }
            try await until { result != nil }
            #expect(result == .some("it moved on"))
            #expect(daemon.received("inbox.answer").last?["text"] as? String == "hello")
        }

        @Test func aFailedRequestShowsAProblem() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.fail("mutate", "nope")
            let s = try store()
            s.dismissInbox(1)
            try await until { s.problem != nil }
            #expect(s.problem?.contains("nope") == true)
        }

        @Test func loadingTheInboxAsksForTheSession() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("inbox", [item(1), item(2)])
            let s = try store()
            var loaded: [InboxItem]?
            s.loadInbox(session: "a") { loaded = $0 }
            try await until { loaded != nil }
            #expect(loaded?.map(\.id) == [1, 2])
            #expect(daemon.received("inbox").first?["session_id"] as? String == "a")
        }

        @Test func theOtherRequestsReachTheDaemon() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("resume", hosted("a"))
            daemon.reply("new", hosted("n"))
            daemon.reply("conversations", [["id": "c1", "cwd": "/w", "updated_at": "2026-11-07T10:00:00Z"]])
            let s = try store(["a"])

            var resumed = false
            s.resume("a") { resumed = true }
            try await until { resumed }
            #expect(daemon.received("resume").first?["id"] as? String == "a")

            s.saveLayout("{}")
            try await until { daemon.received("layout.set").first?["json"] as? String == "{}" }

            var conversations: [Conversation]?
            s.loadConversations { list, _ in conversations = list }
            try await until { conversations != nil }
            #expect(conversations?.map(\.id) == ["c1"])

            s.archive("a")
            s.unarchive("a")
            try await until { daemon.received("mutate").count == 2 }
            #expect(daemon.received("mutate").allSatisfy { $0["id"] as? String == "a" })

            s.newSession(harness: "terminal", in: "/work")
            try await until { s.selectedID == "n" }
            let new = try #require(daemon.received("new").first)
            #expect(new["cwd"] as? String == "/work" && new["harness"] as? String == "terminal")
        }

        @Test func aNewSessionCanBePlacedInstead() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("new", hosted("n"))
            let s = try store(["a"])
            s.reflect(selected: "a", visible: [], sets: [], closed: [])
            var placed: String?
            s.newSession(harness: "claude", in: "/work") { placed = $0.id }
            try await until { placed != nil }
            #expect(placed == "n" && s.selectedID == "a", "whoever places it decides what's selected")
        }

        @Test func aFailedListSaysWhy() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.fail("conversations", "can't read them")
            let s = try store(["a"])
            var message: String?
            s.loadConversations { _, error in message = error }
            try await until { message != nil }
            #expect(message == "can't read them")
        }

        // MARK: What the daemon says

        @Test func onlySessionsAcmdRunsAreListedInOrder() async throws {
            let s = try store([])
            var listed: Set<String>?
            s.onListChanged = { listed = $0 }
            s.publish(try snapshot(["sessions": [
                hosted("b"), hosted("a"), hosted("z", status: "archived"),
                ["ID": "x", "Name": "x", "Cwd": "/", "Status": "busy", "Kind": "external", "Unseen": false, "Harness": "claude", "ClaudeSessionID": "c"],
            ]]))
            try await until { listed != nil }
            #expect(s.sessions.map(\.id) == ["a", "b"])
            #expect(s.archived.map(\.id) == ["z"])
            #expect(listed == ["a", "b"])
        }

        @Test func whatWasWaitingBeforeThisLaunchIsntNews() async throws {
            let s = try store([])
            var news: [[Int64]] = []
            var changes = 0
            var notices: [UInt64] = []
            s.onNewItems = { news.append($0.map(\.id)) }
            s.onInboxChanged = { changes += 1 }
            s.onNotices = { notices = $0.map(\.seq) }
            s.publish(try snapshot(["inbox": [item(1), item(2)]]))
            try await until { !s.queue.isEmpty }
            #expect(news.isEmpty && changes == 1)
            s.publish(try snapshot([
                "inbox": [item(2), item(3)],
                "notices": [["seq": 9, "session_id": "a", "kind": "done"]],
            ]))
            try await until { s.queue.count == 2 && s.queue.last?.id == 3 }
            #expect(news == [[3]] && changes == 2 && notices == [9])
            s.publish(try snapshot(["inbox": [item(2), item(3)]]))
            try await Task.sleep(for: .milliseconds(30))
            #expect(news == [[3]] && changes == 2, "nothing changed")
        }

        @Test func unreadIsCountedAndOnTheDock() async throws {
            let s = try store([])
            s.publish(try snapshot(["inbox": [
                ["id": 1, "session_id": "a", "kind": "done", "state": "open"],
                ["id": 2, "session_id": "b", "kind": "done", "state": "open"],
                ["id": 3, "session_id": "a", "kind": "done", "state": "open", "read": true],
            ]]))
            try await until { s.queue.count == 3 }
            #expect(s.unreadCount() == 2 && s.unreadCount(session: "a") == 1)
        }

        @Test func theLookupsFindSessions() async throws {
            let s = try store([])
            s.publish(try snapshot(["sessions": [hosted("a"), hosted("b"), hosted("z", status: "archived")]]))
            try await until { s.archived.count == 1 }
            s.reflect(selected: "b", visible: [], sets: [], closed: [])
            #expect(s.selected?.id == "b")
            #expect(s.session(id: "a")?.id == "a" && s.session(id: "z")?.id == "z" && s.session(id: "q") == nil)
        }

        // MARK: Reading aloud

        @Test func thereIsNothingToReadWithoutAMessage() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("inbox", [] as [Any])
            let s = try store()
            s.reflect(selected: "a", visible: [], sets: [], closed: [])
            let speech = SpeechController.shared
            speech.stop()
            s.toggleReadingSelected()
            try await until { speech.failure != nil }
            #expect(speech.failure?.contains("hasn't sent a message") == true)
            speech.stop()
        }

        @Test func theLatestMessageIsTheOneRead() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("inbox", [item(2), item(1)])
            let s = try store()
            s.reflect(selected: "a", visible: [], sets: [], closed: [])
            let speech = SpeechController.shared
            speech.setEnabled(false)
            s.toggleReadingSelected()
            // With speech off, asking to read it is what shows this.
            try await until { speech.failure != nil }
            #expect(speech.failure?.contains("Speech is off") == true)
            #expect(daemon.received("inbox").first?["session_id"] as? String == "a")
            speech.stop()
        }

        @Test func nothingSelectedReadsNothing() throws {
            let s = try store()
            let speech = SpeechController.shared
            speech.stop()
            s.toggleReadingSelected()
            #expect(speech.failure == nil && !speech.isSpeaking)
        }
    }

    @Suite @MainActor
    struct AppNotifierTests {
        private func notice(_ seq: UInt64, kind: String = "done", session: String = "a", emitter: String = "app") throws -> Notice {
            try JSONDecoder().decode(Notice.self, from: JSONSerialization.data(withJSONObject: [
                "seq": seq, "session_id": session, "kind": kind, "emitter": emitter,
            ] as [String: Any]))
        }

        @Test func noticesFromBeforeThisLaunchArentNews() throws {
            let n = AppNotifier()
            #expect(n.newNotices(in: [try notice(1), try notice(2)]).isEmpty)
            #expect(n.newNotices(in: [try notice(2), try notice(3)]).map(\.seq) == [3])
            #expect(n.newNotices(in: [try notice(3)]).isEmpty, "only once")
        }

        @Test func otherPartsOfTheAppAddWarnings() {
            let n = AppNotifier()
            let before = n.warnings.count
            var fixed = false
            n.moreWarnings = { [AppWarning(id: "wake", title: "t", detail: "d", fix: "f", actionTitle: "Turn On…", action: { fixed = true })] }
            #expect(n.warnings.count == before + 1)
            n.warnings.last?.action()
            #expect(fixed && n.warnings.last?.id == "wake")
        }

        @Test func noNoticesAtLaunchStillCountsFromTheStart() throws {
            let n = AppNotifier()
            #expect(n.newNotices(in: []).isEmpty)
            #expect(n.newNotices(in: [try notice(1)]).map(\.seq) == [1])
        }

        @Test func aRestartedDaemonCountsFromTheStartAgain() throws {
            let n = AppNotifier()
            _ = n.newNotices(in: [try notice(40)])
            #expect(n.newNotices(in: [try notice(1), try notice(2)]).map(\.seq) == [1, 2])
            #expect(n.newNotices(in: [try notice(3)]).map(\.seq) == [3])
        }

        @Test func aBannerIsForTheAppWhenYoureNotLookingAndThePopupWont() throws {
            let n = AppNotifier()
            #expect(n.shouldShow(try notice(1)))
            #expect(!n.shouldShow(try notice(1, emitter: "fallback")), "another part of acmd is showing it")
            n.isLooking = { $0 == "a" }
            #expect(!n.shouldShow(try notice(1, session: "a")))
            #expect(n.shouldShow(try notice(1, session: "b")))
            n.isLooking = { _ in false }
            n.popupShows = { $0 == "done" }
            #expect(!n.shouldShow(try notice(1, kind: "done")))
            #expect(n.shouldShow(try notice(1, kind: "waiting")))
        }

        @Test func warningsFollowThePermissions() throws {
            UserDefaults.standard.removeObject(forKey: "warnings.dismissed")
            let n = AppNotifier()
            #expect(n.warnings.isEmpty)
            n.setPermission(turnedOff: true, badgeOff: true)
            #expect(n.warnings.map(\.id) == ["notifications", "dock-badge"])
            n.setPermission(turnedOff: false, badgeOff: true)
            #expect(n.warnings.map(\.id) == ["dock-badge"])
            n.setPermission(turnedOff: false, badgeOff: false)
            #expect(n.warnings.isEmpty, "fixed, so it's gone")
        }

        @Test func aDismissedWarningStaysListedAndIsRemembered() throws {
            UserDefaults.standard.removeObject(forKey: "warnings.dismissed")
            defer { UserDefaults.standard.removeObject(forKey: "warnings.dismissed") }
            let n = AppNotifier()
            n.setPermission(turnedOff: false, badgeOff: true)
            let warning = try #require(n.warnings.first)
            #expect(!n.isDismissed(warning))
            n.setDismissed(warning, true)
            #expect(n.isDismissed(warning) && n.warnings.count == 1)
            let later = AppNotifier()
            later.setPermission(turnedOff: false, badgeOff: true)
            #expect(later.isDismissed(try #require(later.warnings.first)))
            n.setDismissed(warning, false)
            #expect(!n.isDismissed(warning))
        }
    }
}
