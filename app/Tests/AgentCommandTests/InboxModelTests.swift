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

@Suite(.serialized) @MainActor
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

    @Test func emptyingTheQueueSaysSo() throws {
        let h = try Harness(queue: [makeItem(1, session: "a")])
        var emptied = false
        h.model.onQueueEmptied = { emptied = true }
        h.queue([])
        #expect(emptied)
        #expect(h.model.selectedID == nil)
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
