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
        let fields: [String: Any] = ["id": "r", "name": "r", "cwd": "/", "status": "busy", "kind": "hosted", "unseen": false,
                                     "harness_kind": "claude", "claude_session_id": "", "remote": on]
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
    #expect(claude.harness == .claude && claude.harnessTitle == "Claude")
    #expect(try makeSession(id: "o", harness: "").harness == .claude, "older sessions are Claude's")
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
        "id": "d", "name": "", "cwd": "/w/x", "status": "busy", "kind": "hosted", "unseen": false, "harness_kind": "claude",
        "claude_session_id": "", "display_name": "From the daemon",
    ]))
    #expect(named.displayName == "From the daemon" && named.capabilities == nil && !named.can(.attach))
}

/// The daemon's harnesses: each has its kind, and one of a kind the app has
/// no icon for is shown with a generic one.
@MainActor @Test func harnessesComeFromTheDaemon() {
    let fake = FakeCalls()
    let list = HarnessList()
    list.calls = fake
    fake.replies["harnesses"] = ["harnesses": [
        ["id": "HC", "name": "Claude", "kind": "claude", "title": "Claude", "icon": "claude", "installed": true],
        ["id": "HW", "name": "Work", "kind": "claude", "title": "Claude", "installed": true, "config_dir": "~/.claude-work",
         "folder": "/Users/me/.claude-work", "command": "/bin/claude", "args": ["--x"]],
        ["id": "HP", "name": "Pilot", "kind": "copilot", "title": "Copilot", "installed": false],
    ], "default": "HW"]
    var loaded = false
    list.load { loaded = true }
    #expect(loaded && list.harnesses.map(\.name) == ["Claude", "Work", "Pilot"] && list.defaultHarness?.name == "Work")
    let work = list.harnesses[1]
    #expect(work.isClaude && work.kindTitle == "Claude" && work.configDir == "~/.claude-work" && work.args == ["--x"] && work.request == ("HW", "claude"))
    let pilot = list.harnesses[2]
    #expect(pilot.isAgent && !pilot.installed && pilot.title == "Pilot" && pilot.panelPrompt == "Start Session")
    let running = try? makeSession(id: "s", harness: "copilot", agent: "copilot")
    #expect(running.map(RowMark.init) == .dot, "no mark of its own: the generic one")

    // A failure keeps what was there; an empty list is none.
    fake.failures["harnesses"] = "down"
    list.load()
    #expect(list.harnesses.count == 3)
    list.set([])
    #expect(list.harnesses.isEmpty && list.defaultID == nil)
    list.set([work], default: "nope")
    #expect(list.defaultID == "HW", "a default that isn't one: the first installed")
    #expect(Harness.agent("") .title == "Agent")
}

/// Adding, changing and removing go through the daemon's harness ops, then
/// the list is read again.
@MainActor @Test func harnessesAreChangedThroughTheDaemon() {
    let fake = FakeCalls()
    let list = HarnessList()
    list.calls = fake
    fake.replies["harness.add"] = ["id": "HW", "name": "Work", "kind": "claude"]
    fake.replies["harnesses"] = ["harnesses": [["id": "HW", "name": "Work", "kind": "claude", "installed": true]]]
    var added: Harness?
    list.add(HarnessList.Save(kind: "claude", name: "Work", config_dir: "~/.claude-work")) { added = try? $0.get() }
    #expect(added?.harnessID == "HW" && fake.ops == ["harness.add", "harnesses"] && list.harnesses.map(\.name) == ["Work"])
    #expect(fake.asked[0].input["config_dir"] as? String == "~/.claude-work" && fake.asked[0].input["command"] == nil)
    fake.failures["harness.update"] = "another harness is already called \"Claude\""
    var why: String?
    list.update(HarnessList.Save(id: "HW", name: "Claude")) { if case .failure(let e) = $0 { why = e.localizedDescription } }
    #expect(why?.contains("already called") == true)
    fake.failures["harness.remove"] = "Work runs scheduled jobs"
    list.remove(list.harnesses[0]) { why = $0 }
    #expect(why == "Work runs scheduled jobs" && fake.asked.last?.input["id"] as? String == "HW")
}

/// The name rules the daemon checks, shown as you type.
@Test func harnessNamesAreLettersAndUnique() {
    let others = [Harness(id: "H1", kind: "claude", name: "My Work")]
    #expect(HarnessName.problem("Personal", others: others) == nil)
    #expect(HarnessName.problem(" ", others: others) == "Give it a name.")
    for bad in ["Work2", "my-work", "two  spaces"] {
        #expect(HarnessName.problem(bad, others: others)?.contains("letters") == true, "\(bad)")
    }
    #expect(HarnessName.problem("my work", others: others)?.contains("already") == true)
    #expect(HarnessName.defaultFolder("My Work") == "~/.claude-my_work")
}

/// A host's session runs in one of the host's harnesses: it shows its kind,
/// not this Mac's harness of the same id.
@Test func aHostsSessionShowsItsKind() throws {
    let s: Session = try JSONDecoder().decode(Session.self, from: Data("""
        {"id": "s", "harness_kind": "antigravity", "harness_id": "H1", "host": "studio"}
        """.utf8))
    #expect(s.harness == .antigravity && s.harnessTitle == "Antigravity")
}

/// The list's other answers: a removal that works reads the list again, a
/// change it can't read says so, and kinds are named.
@MainActor @Test func harnessListAnswers() {
    let fake = FakeCalls()
    let list = HarnessList()
    list.calls = fake
    fake.replies["harnesses"] = ["harnesses": [["id": "HC", "name": "Claude", "kind": "claude", "installed": true]], "default": "HC"]
    var why: String? = "unset"
    list.remove(Harness(id: "HW", kind: "claude", name: "Work")) { why = $0 }
    #expect(why == nil && fake.ops == ["harness.remove", "harnesses"] && list.defaultHarness?.harnessID == "HC")
    fake.replies["harness.update"] = "not a harness"
    var failed = false
    list.update(HarnessList.Save(id: "HC", name: "Main")) { if case .failure = $0 { failed = true } }
    #expect(failed, "an answer it can't read")
    fake.replies["harness.update"] = ["id": "HC", "name": "Main", "kind": "claude"]
    var renamed: Harness?
    list.update(HarnessList.Save(id: "HC", name: "Main")) { renamed = try? $0.get() }
    #expect(renamed?.name == "Main")
    fake.failures["harness.add"] = "no"
    var addFailed = false
    list.add(HarnessList.Save(kind: "claude", name: "X")) { if case .failure = $0 { addFailed = true } }
    #expect(addFailed)
    #expect(Harness.title(of: "") == "Agent" && Harness.title(of: "codex") == "Codex" && Harness.title(of: "terminal") == "Terminal")
    #expect(Harness.terminal.request == ("", "terminal") && !Harness.terminal.isAgent && Harness.antigravity.isAntigravity)
    #expect(Harness(id: "nope") == nil && Harness(id: "") == nil)
}
