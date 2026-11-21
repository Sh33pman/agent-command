import Foundation
import Testing

@testable import AgentCommand

@Test func renameNames() {
    #expect(RenameNames.names(base: " work ", count: 1) == ["work"])
    #expect(RenameNames.names(base: "work", count: 2) == ["work - 1", "work - 2"])
    #expect(RenameNames.names(base: "  ", count: 3).isEmpty)
}

@Test func popupOpensForTheRightThings() {
    #expect(PopupOpens.needsYou.opens(forItem: "question"))
    #expect(PopupOpens.needsYou.opens(forItem: "permission"))
    #expect(PopupOpens.needsYou.opens(forItem: "error"))
    #expect(!PopupOpens.needsYou.opens(forItem: "done"))
    #expect(PopupOpens.everything.opens(forItem: "done"))
    #expect(!PopupOpens.never.opens(forItem: "question"))
    #expect(PopupOpens.needsYou.opens(forNotice: "waiting"))
    #expect(!PopupOpens.needsYou.opens(forNotice: "done"))
    #expect(PopupOpens.everything.opens(forNotice: "done"))
    #expect(!PopupOpens.never.opens(forNotice: "error"))
}

@Test func anItemFromTheDaemonFillsInWhatItOmits() throws {
    let item = try JSONDecoder().decode(InboxItem.self, from: Data(#"{"id": 3, "session_id": "s", "kind": "done"}"#.utf8))
    #expect(item.id == 3 && item.sessionID == "s" && item.kind == "done")
    #expect(item.text.isEmpty && item.options.isEmpty && !item.multipart && !item.read)
}

@Test func aSessionDecodesItsRemoteFlag() throws {
    #expect(try makeSession(id: "old").remote == nil, "older daemons don't send it")
    #expect(try !makeSession(id: "old").isRemote)
    for on in [true, false] {
        let fields: [String: Any] = ["ID": "r", "Name": "r", "Cwd": "/", "Status": "busy", "Kind": "hosted", "Unseen": false,
                                     "Harness": "claude", "ClaudeSessionID": "", "Remote": on]
        let decoded = try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
        #expect(decoded.isRemote == on)
    }
}

@Test func aSessionIsWhatTheDaemonSays() throws {
    let terminal = try makeSession(id: "t", harness: "terminal", status: "running")
    #expect(terminal.isTerminal && terminal.runningAgent == nil && !terminal.can(.resume))
    let withAgent = try makeSession(id: "a", harness: "terminal", status: "busy", claude: "c1")
    #expect(withAgent.isTerminal && withAgent.runningAgent == .claude)
    let paused = try makeSession(id: "p", status: "paused", claude: "c2")
    #expect(paused.runningAgent == .claude && paused.can(.resume) && paused.can(.continueInJob))
    let claude = try makeSession(id: "c")
    #expect(claude.harnessKind == .claude && claude.harnessTitle == "Claude")
    #expect(try makeSession(id: "o", harness: "").harnessKind == .claude, "older sessions are Claude's")
    let hooked = try makeSession(id: "h", harness: "antigravity", status: "busy", claude: "conv-1")
    #expect(hooked.runningAgent == .antigravity && hooked.harnessTitle == "Antigravity")
    #expect(hooked.details.contains("Antigravity conversation conv-1"))
    #expect(try makeSession(id: "w", claude: "s-1").details.contains("Claude session s-1"))
    #expect(terminal.harnessTitle == "Terminal")
    // An agent the app doesn't know: shown by its name, with a generic icon.
    let other = try makeSession(id: "x", harness: "codex", agent: "codex")
    #expect(other.harnessTitle == "Codex" && other.runningAgent?.icon == "codex" && RowMark(other) == .dot)
    // A capability the app doesn't know is ignored; one the daemon didn't give isn't offered.
    let odd = try makeSession(id: "q", capabilities: ["teleport", "attach"])
    #expect(odd.can(.attach) && !odd.can(.archive))
    #expect(try makeSession(id: "z", status: "archived").isArchived)
    #expect(try makeSession(id: "n", name: "", cwd: "/tmp/project", status: "done").displayName == "project")
    #expect(try makeSession(id: "m", name: "mine", status: "done").displayName == "mine")
    let named = try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: [
        "ID": "d", "Name": "", "Cwd": "/w/x", "Status": "busy", "Kind": "hosted", "Unseen": false, "Harness": "claude",
        "ClaudeSessionID": "", "display_name": "From the daemon",
    ]))
    #expect(named.displayName == "From the daemon" && named.capabilities == nil && !named.can(.attach))
}

/// A harness the daemon lists that the app has no icon for: shown with a generic one.
@MainActor @Test func aNewAgentFromTheDaemonNeedsNoNewApp() {
    let fake = FakeCalls()
    let list = HarnessList()
    list.calls = fake
    fake.replies["harnesses"] = ["harnesses": [
        ["name": "claude", "title": "Claude", "icon": "claude", "agent": true],
        ["name": "copilot", "title": "Copilot", "icon": "copilot", "agent": true],
        ["name": "terminal", "title": "Terminal", "icon": "terminal", "agent": false],
    ], "default": "claude"]
    var loaded = false
    list.load { loaded = true }
    #expect(loaded && list.harnesses.map(\.rawValue) == ["claude", "copilot", "terminal"])
    let copilot = list.harnesses[1]
    #expect(copilot.isAgent && copilot.title == "Copilot" && copilot.panelPrompt == "Start Session")
    #expect(HarnessChoice(list.harnesses).matches.map(\.title) == ["Claude", "Copilot", "Terminal"])
    let running = try? makeSession(id: "s", harness: "copilot", agent: "copilot")
    #expect(running.map(RowMark.init) == .dot, "no mark of its own: the generic one")

    // An answer with nothing in it, or a failure, keeps what was there.
    fake.replies["harnesses"] = ["harnesses": [Any]()]
    list.load()
    fake.failures["harnesses"] = "down"
    list.load()
    #expect(list.harnesses.count == 3)
    list.set([])
    #expect(list.harnesses == Harness.known)
    #expect(Harness.agent("") .title == "Agent")
}
