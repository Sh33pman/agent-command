import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// The popup's queue, driven the way the store drives it, with no daemon.
@MainActor
private final class Harness {
    let store = SessionStore()
    let model: InboxModel

    init(queue: [InboxItem], sessions: [Session] = [], cursor: InboxItem? = nil, showsCursor: Bool = true) throws {
        try isolateFromTheRealDaemon()
        _ = NSApplication.shared
        store.sessions = sessions
        store.queue = queue
        model = InboxModel(store: store, filter: .queue, showsCursor: showsCursor)
        model.canMarkRead = { false }
        model.start(cursor: cursor)
    }

    /// A new list of what's waiting, as the store hands over from the daemon.
    func queue(_ items: [InboxItem]) {
        store.queue = items
        store.onInboxChanged?()
    }

    @discardableResult
    func press(_ code: UInt16, _ characters: String = "", flags: NSEvent.ModifierFlags = [], isRepeat: Bool = false) throws -> Bool {
        model.handle(try keyDown(code, characters, flags: flags, isRepeat: isRepeat))
    }

    /// Past the half second that guards against a press meant for the last message.
    func settle() async throws {
        try await Task.sleep(for: .milliseconds(600))
    }

    deinit { MainActor.assumeIsolated { model.stop() } }
}

extension UsesSharedSpeech {
@Suite @MainActor
struct InboxModelTests {
    private func question(_ id: Int64, _ session: String) throws -> InboxItem {
        try makeItem(id, session: session, kind: "question", options: ["Red", "Green", "Blue"])
    }

    // MARK: Moving through the queue

    @Test func thePopupShowsTheFirstMessageByItself() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b")])
        #expect(h.model.selectedID == 1)
        #expect(h.model.position?.index == 1 && h.model.position?.count == 2)
    }

    @Test func thePopupOpensOnTheMessageItWasOpenedFor() throws {
        let items = [try makeItem(1, session: "a"), try makeItem(2, session: "b")]
        let h = try Harness(queue: items, cursor: items[1])
        #expect(h.model.selectedID == 2)
    }

    @Test func nextAndPreviousStopAtTheEnds() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b")])
        h.model.step(-1)
        #expect(h.model.selectedID == 1)
        h.model.step(1)
        #expect(h.model.selectedID == 2)
        h.model.step(1)
        #expect(h.model.selectedID == 2)
        try h.press(Key.up)
        #expect(h.model.selectedID == 1)
        try h.press(Key.j, "j", flags: .control)
        #expect(h.model.selectedID == 2)
    }

    // MARK: Messages coming and going

    @Test func aNewerMessageFromTheSessionOnShowTakesOver() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b")])
        h.queue([try makeItem(2, session: "b"), try makeItem(3, session: "a")])
        #expect(h.model.selectedID == 3)
        #expect(h.model.notice?.contains("replaces") == true)
    }

    @Test func aReplacementThatComesLaterStillTakesOver() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b")])
        h.queue([try makeItem(2, session: "b")])
        #expect(h.model.selectedID == 2)
        h.queue([try makeItem(2, session: "b"), try makeItem(3, session: "a")])
        #expect(h.model.selectedID == 3)
    }

    @Test func movingCancelsWaitingForAReplacement() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b"), makeItem(3, session: "c")])
        h.queue([try makeItem(2, session: "b"), try makeItem(3, session: "c")])
        h.model.step(1)
        #expect(h.model.selectedID == 3)
        h.queue([try makeItem(2, session: "b"), try makeItem(3, session: "c"), try makeItem(4, session: "a")])
        #expect(h.model.selectedID == 3)
        #expect(h.model.notice?.contains("New message") == true)
    }

    @Test func anotherSessionsMessageLeavesYouWhereYouAre() throws {
        let h = try Harness(queue: [makeItem(1, session: "a")])
        h.queue([try makeItem(1, session: "a"), try makeItem(2, session: "b")])
        #expect(h.model.selectedID == 1)
        #expect(h.model.notice?.contains("New message") == true)
        #expect(h.model.notice?.contains("replaces") == false)
    }

    @Test func whatWasAlreadyThereIsNotNews() throws {
        let h = try Harness(queue: [makeItem(1, session: "a"), makeItem(2, session: "b")])
        #expect(h.model.notice == nil)
    }

    @Test func youAreNotPulledAwayWhileChoosing() async throws {
        let sessions = [try makeSession(id: "b", status: "waiting", claude: "c")]
        let h = try Harness(
            queue: [makeItem(1, session: "a"), question(2, "b")], sessions: sessions)
        h.queue([try question(2, "b")])
        #expect(h.model.selectedID == 2)
        try await h.settle()
        try h.press(Key.returnKey)
        let shown = try #require(h.model.selected)
        #expect(h.model.highlightedOption(of: shown) == 0)
        h.queue([try question(2, "b"), try makeItem(3, session: "a")])
        #expect(h.model.selectedID == 2)
        #expect(h.model.highlightedOption(of: shown) == 0)
    }

    @Test func aMessageYouDealtWithIsntFollowedLikeOneThatLeftByItself() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        let first = try makeItem(1, session: "a")
        let h = try Harness(queue: [first, try makeItem(2, session: "b")])
        h.model.dismiss(first)
        h.queue([try makeItem(2, session: "b")])
        #expect(h.model.selectedID == 2)
        h.queue([try makeItem(2, session: "b"), try makeItem(3, session: "a")])
        #expect(h.model.selectedID == 2, "you moved on from that session")
        #expect(h.model.notice?.contains("replaces") == false)
    }

    @Test func emptyingTheQueueSaysSo() throws {
        let h = try Harness(queue: [makeItem(1, session: "a")])
        var emptied = false
        h.model.onQueueEmptied = { emptied = true }
        h.queue([])
        #expect(emptied)
        #expect(h.model.selectedID == nil)
    }

    // MARK: The full Inbox, with its panels and a daemon

    private func mainInbox(
        _ daemon: FakeDaemon, queue: [InboxItem], sessions: [Session] = []
    ) throws -> Harness {
        _ = daemon
        return try Harness(queue: queue, sessions: sessions, showsCursor: false)
    }

    private func raw(_ id: Int, session: String = "a", read: Bool = false) -> [String: Any] {
        ["id": id, "session_id": session, "kind": "done", "state": "open", "text": "m", "read": read]
    }

    @Test func openingASessionShowsItsFirstMessage() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        daemon.reply("inbox", [raw(5), raw(4, read: true)])
        let h = try mainInbox(daemon, queue: [])
        h.model.setFilter(.session("a"))
        try await until { h.model.selectedID == 5 }
        #expect(h.model.items.map(\.id) == [5, 4] && h.model.panel == .messages)
        #expect(h.model.typing == false, "the keyboard stays on the list")
        try await until { daemon.received("mutate").contains { ($0["item_ids"] as? [Int]) == [5] } }
    }

    @Test func aSetListsOnlyItsSessions() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        daemon.reply("inbox", [raw(3, session: "c"), raw(2, session: "b"), raw(1, session: "a")])
        let h = try mainInbox(daemon, queue: [])
        h.model.setFilter(.set(["a", "b"]))
        try await until { h.model.selectedID != nil }
        #expect(h.model.items.map(\.id) == [2, 1])
        h.model.setFilter(.all)
        try await until { h.model.items.count == 3 }
    }

    @Test func nothingToShowIsFine() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        daemon.reply("inbox", [] as [Any])
        let h = try mainInbox(daemon, queue: [])
        h.model.setFilter(.session("a"))
        try await Task.sleep(for: .milliseconds(50))
        #expect(h.model.selectedID == nil)
    }

    @Test func theFullInboxOnlyOpensWhatYouOpen() throws {
        let items = [try makeItem(1, session: "a"), try makeItem(2, session: "b")]
        let h = try Harness(queue: items, sessions: [try makeSession(id: "a", claude: "c"), try makeSession(id: "b", claude: "c")], showsCursor: false)
        #expect(h.model.selected == nil)
        h.model.click(item: items[1])
        #expect(h.model.selected == nil, "a click moves the cursor")
        try h.press(Key.returnKey)
        #expect(h.model.selectedID == 2, "return opens")
        #expect(h.model.typing, "and goes to where you type")
    }

    @Test func thePanelsShareTheKeyboard() throws {
        let sessions = [try makeSession(id: "a", claude: "c"), try makeSession(id: "b", claude: "c")]
        let h = try Harness(queue: [], sessions: sessions, showsCursor: false)
        #expect(h.model.panel == .messages)
        try h.press(11, "b", flags: [.command, .shift])
        #expect(h.model.panel == .sessions)
        #expect(h.model.sessionCursor == .queue)
        try h.press(Key.down)
        #expect(h.model.sessionCursor == .all)
        try h.press(Key.down)
        #expect(h.model.sessionCursor == .session("a"))
        try h.press(Key.up)
        try h.press(Key.up)
        try h.press(Key.up)
        #expect(h.model.sessionCursor == .queue, "it stops at the top")
        h.model.click(filter: .session("b"))
        #expect(h.model.sessionCursor == .session("b") && h.model.filter == .queue, "a click only moves the cursor")
        try h.press(Key.escape)
        #expect(h.model.panel == .messages, "escape comes back")
    }

    @Test func thePopupHasNoSessionsPanel() throws {
        let h = try Harness(queue: [makeItem(1, session: "a")])
        try h.press(11, "b", flags: [.command, .shift])
        #expect(h.model.panel == .messages)
    }

    @Test func marksApplyToSessionsActions() throws {
        let sessions = [try makeSession(id: "a", claude: "c"), try makeSession(id: "b", claude: "c")]
        let h = try Harness(queue: [], sessions: sessions, showsCursor: false)
        var renamed: [String] = []
        h.model.rename = { renamed = $0.map(\.id) }
        h.model.setPanel(.sessions)
        try h.press(0, "x")
        #expect(h.model.markedSessions.isEmpty, "the Queue row can't be marked")
        h.model.click(filter: .session("a"))
        try h.press(7, "x")
        h.model.click(filter: .session("b"))
        try h.press(7, "x")
        #expect(h.model.markedSessions == ["a", "b"])
        try h.press(15, "r", flags: .command)
        #expect(renamed == ["a", "b"])
        #expect(h.model.targets(forRow: sessions[1]).map(\.id) == ["a", "b"])
        h.model.click(filter: .session("a"))
        try h.press(7, "x")
        #expect(h.model.targets(forRow: sessions[0]).map(\.id) == ["a"], "unmarked, a row is on its own")
        h.model.setPanel(.messages)
        #expect(h.model.markedSessions.isEmpty, "marks don't carry over")
    }

    @Test func theActionKeysWorkOnTheMessageUnderTheCursor() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        let sessions = [try makeSession(id: "a", claude: "c")]
        let items = [try makeItem(1, session: "a"), try makeItem(2, session: "a", read: true)]
        let h = try mainInbox(daemon, queue: items, sessions: sessions)
        var closed = false
        var went: String?
        h.model.close = { closed = true }
        h.model.goToSession = { went = $0 }
        h.model.click(item: items[0])
        try h.press(32, "u")
        try await until { daemon.received("mutate").contains { $0["kind"] as? String == "inbox.read" && $0["flag"] as? Bool == true } }
        try h.press(2, "d")
        try await until { daemon.received("mutate").contains { $0["kind"] as? String == "inbox.dismiss" } }
        try h.press(51) // delete dismisses too, as it deletes a job in Scheduled
        try await until { daemon.received("mutate").filter { $0["kind"] as? String == "inbox.dismiss" }.count == 2 }
        try h.press(7, "x")
        #expect(h.model.markedMessages == [1])
        try h.press(7, "x")
        #expect(h.model.markedMessages.isEmpty)
        try h.press(5, "g")
        #expect(closed && went == "a")
    }

    @Test func goingToAnArchivedSessionDoesNothing() throws {
        let archived = try makeSession(id: "a", status: "archived", claude: "c")
        let h = try Harness(queue: [makeItem(1, session: "a")], sessions: [archived], showsCursor: false)
        var went = false
        h.model.goToSession = { _ in went = true }
        h.model.go(try makeItem(1, session: "a"))
        #expect(!went)
    }

    @Test func aReplySendsTheDraftAndClearsIt() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        let sessions = [try makeSession(id: "a", status: "done", claude: "c")]
        let item = try makeItem(1, session: "a")
        let h = try mainInbox(daemon, queue: [item], sessions: sessions)
        h.model.open(item)
        h.model.drafts["a"] = "   "
        try h.press(Key.returnKey, flags: .command)
        #expect(daemon.received("inbox.answer").isEmpty, "nothing to send")
        h.model.drafts["a"] = "fix it"
        try h.press(Key.returnKey, flags: .command)
        try await until { !daemon.received("inbox.answer").isEmpty }
        #expect(daemon.received("inbox.answer").first?["text"] as? String == "fix it")
        try await until { h.model.drafts["a"] == nil }
        #expect(!h.model.typing && h.model.error == nil)
    }

    @Test func aFailedAnswerSaysWhyAndKeepsTheDraft() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        daemon.fail("inbox.answer", "it moved on")
        let sessions = [try makeSession(id: "a", status: "done", claude: "c")]
        let item = try makeItem(1, session: "a")
        let h = try mainInbox(daemon, queue: [item], sessions: sessions)
        h.model.open(item)
        h.model.drafts["a"] = "fix it"
        h.model.sendReply(item)
        try await until { h.model.error != nil }
        #expect(h.model.error == "it moved on" && h.model.drafts["a"] == "fix it" && !h.model.sending)
    }

    @Test func commandKeysAreUsedOrPassedOn() throws {
        let h = try Harness(queue: [], showsCursor: false)
        var closed = false
        h.model.close = { closed = true }
        #expect(try h.press(13, "w", flags: .command))
        #expect(closed)
        #expect(try !h.press(45, "n", flags: .command), "the app's other shortcuts still work")
        #expect(try h.press(0, "z"), "other keys stop here, not in the terminal behind")
        #expect(try h.press(Key.returnKey, flags: .command), "no split behind the Inbox")
    }

    @Test func typingKeepsTheKeysExceptEscape() throws {
        let sessions = [try makeSession(id: "a", status: "done", claude: "c")]
        let h = try Harness(queue: [makeItem(1, session: "a")], sessions: sessions)
        try h.press(Key.returnKey)
        #expect(h.model.typing)
        #expect(try !h.press(0, "a"), "letters go to the reply box")
        #expect(try !h.press(Key.down), "and arrows")
        #expect(h.model.selectedID == 1)
        #expect(try h.press(Key.escape))
    }

    @Test func theSpeechShortcutReadsTheMessageOnShow() throws {
        let h = try Harness(queue: [makeItem(1, session: "a")])
        let speech = SpeechController.shared
        speech.setEnabled(false)
        try h.press(1, "s", flags: [.command, .shift])
        #expect(speech.failure?.contains("Speech is off") == true, "it tried to read")
        speech.stop()
    }

    @Test func theSidebarOfTheInboxListsAgentsAndCountsUnread() async throws {
        let daemon = try FakeDaemon()
        defer { daemon.stop() }
        daemon.reply("inbox", [raw(1, session: "a"), raw(2, session: "a", read: true), raw(3, session: "old-term")])
        let sessions = [
            try makeSession(id: "a", claude: "c"), try makeSession(id: "shell", harness: "terminal", status: "running"),
            try makeSession(id: "old-term", harness: "terminal", status: "running"),
        ]
        let h = try mainInbox(daemon, queue: [try makeItem(1, session: "a")], sessions: sessions)
        try await until { h.model.unreadCount("a") == 1 }
        #expect(h.model.rows.map(\.session.id) == ["a", "old-term"], "a plain terminal with nothing from an agent isn't listed")
        #expect(h.model.unreadCount(set: ["a", "old-term"]) == 2)
        #expect(h.model.queueCount == 1 && h.model.openItem("a")?.id == 1 && h.model.openItem("shell") == nil)
        #expect(h.model.sessionName("a") == "a" && h.model.sessionName("nope") == "Deleted session")
        #expect(h.model.session(of: try makeItem(1, session: "a"))?.id == "a")
        #expect(!h.model.isMoveOutable(sessions[0]) && h.model.setMembers(of: "a").isEmpty)
    }

    // MARK: Answering with the keyboard

    private func waiting(_ session: String) throws -> Session {
        try makeSession(id: session, status: "waiting", claude: "c")
    }

    @Test func returnPicksOutTheFirstOptionAndKeysMoveIt() async throws {
        let h = try Harness(queue: [question(1, "a")], sessions: [waiting("a")])
        try await h.settle()
        let item = try #require(h.model.selected)
        #expect(h.model.highlightedOption(of: item) == nil)
        try h.press(Key.returnKey)
        #expect(h.model.highlightedOption(of: item) == 0)
        for (code, characters, want) in [
            (Key.right, "", 1), (Key.l, "l", 2), (Key.j, "j", 2), (Key.left, "", 1), (Key.h, "h", 0), (Key.up, "", 0),
            (Key.down, "", 1), (Key.k, "k", 0),
        ] {
            try h.press(code, characters)
            #expect(h.model.highlightedOption(of: item) == want, "after key \(code)")
        }
        #expect(h.model.selectedID == 1, "those keys move the option, not the message")
        try h.press(Key.escape)
        #expect(h.model.highlightedOption(of: item) == nil)
    }

    @Test func tabGoesToTheNextUnreadMessage() throws {
        let items = [
            try makeItem(1, session: "a", read: true), try makeItem(2, session: "b"),
            try makeItem(3, session: "c", read: true), try makeItem(4, session: "d"),
        ]
        let h = try Harness(queue: items)
        #expect(h.model.selectedID == 1)
        try h.press(48, "\t")
        #expect(h.model.selectedID == 2)
        try h.press(48, "\t")
        #expect(h.model.selectedID == 4)
        try h.press(48, "\t")
        #expect(h.model.selectedID == 2, "round to the start")
    }

    @Test func returnInTheInboxOpensAndAnswersAsInThePopup() throws {
        let item = try question(1, "a")
        let h = try Harness(queue: [item], sessions: [waiting("a")], showsCursor: false)
        h.model.click(item: item)
        try h.press(Key.returnKey)
        #expect(h.model.selectedID == 1 && h.model.highlightedOption(of: item) == 0)
    }

    @Test func holdingReturnDoesNotChooseByItself() async throws {
        let h = try Harness(queue: [question(1, "a")], sessions: [waiting("a")])
        try await h.settle()
        let item = try #require(h.model.selected)
        try h.press(Key.returnKey, isRepeat: true)
        #expect(h.model.highlightedOption(of: item) == nil, "a repeat doesn't start choosing")
        try h.press(Key.returnKey)
        try h.press(Key.returnKey, isRepeat: true)
        #expect(!h.model.sending, "a repeat doesn't choose")
    }

    @Test func aQuickSecondReturnIsIgnoredUntilYouHaveLooked() async throws {
        let h = try Harness(queue: [question(1, "a")], sessions: [waiting("a")])
        try await h.settle()
        try h.press(Key.returnKey)
        try h.press(Key.returnKey)
        #expect(!h.model.sending, "straight after the highlight appears")
        try h.press(Key.right)
        try h.press(Key.returnKey)
        #expect(h.model.sending, "moving the highlight means you've looked")
    }

    @Test func numbersWaitUntilAMessageHasSettled() async throws {
        let h = try Harness(queue: [question(1, "a")], sessions: [waiting("a")])
        try h.press(Key.one, "1")
        #expect(!h.model.sending, "straight after it appears")
        try await h.settle()
        try h.press(Key.one, "1")
        #expect(h.model.sending)
    }

    @Test func returnOnAFinishedTurnGoesToTheReplyBox() throws {
        let sessions = [try makeSession(id: "a", status: "done", claude: "c")]
        let h = try Harness(queue: [makeItem(1, session: "a")], sessions: sessions)
        try h.press(Key.returnKey)
        #expect(h.model.typing)
        try h.press(Key.escape)
        #expect(!h.model.typing)
    }

    @Test func aMessageThatCantBeAnsweredOffersNothing() async throws {
        let h = try Harness(queue: [question(1, "a")], sessions: [try makeSession(id: "a", status: "busy", claude: "c")])
        try await h.settle()
        let item = try #require(h.model.selected)
        try h.press(Key.returnKey)
        #expect(h.model.highlightedOption(of: item) == nil)
        #expect(h.model.answerable(item) != nil)
    }

    @Test func whatCanBeAnsweredDependsOnTheSession() throws {
        let h = try Harness(queue: [], sessions: [
            try waiting("waiting"), try makeSession(id: "done", status: "done", claude: "c"),
            try makeSession(id: "busy", status: "busy", claude: "c"), try makeSession(id: "gone", status: "paused", claude: "c"),
            try makeSession(id: "none", status: "done"),
        ])
        #expect(h.model.answerable(try question(1, "waiting")) == nil)
        #expect(h.model.answerable(try question(1, "done")) != nil)
        #expect(h.model.answerable(try makeItem(1, session: "done")) == nil)
        #expect(h.model.answerable(try makeItem(1, session: "busy")) != nil)
        #expect(h.model.answerable(try makeItem(1, session: "gone")) != nil)
        #expect(h.model.answerable(try makeItem(1, session: "none")) != nil)
        #expect(h.model.answerable(try makeItem(1, session: "unknown")) != nil)
    }
}
}
