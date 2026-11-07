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

@Test func aSessionKnowsWhatItIs() throws {
    let terminal = try makeSession(id: "t", harness: "terminal", status: "running")
    #expect(terminal.isTerminal && !terminal.isAgent && !terminal.isStopped)
    let withAgent = try makeSession(id: "a", harness: "terminal", status: "busy", claude: "c1")
    #expect(withAgent.isTerminal && withAgent.isAgent)
    let paused = try makeSession(id: "p", status: "paused", claude: "c2")
    #expect(!paused.isTerminal && paused.isAgent && paused.isStopped)
    #expect(try makeSession(id: "f", status: "finished").isStopped)
    #expect(try makeSession(id: "z", status: "archived").isArchived)
    #expect(try makeSession(id: "n", name: "", cwd: "/tmp/project", status: "done").displayName == "project")
    #expect(try makeSession(id: "m", name: "mine", status: "done").displayName == "mine")
}
