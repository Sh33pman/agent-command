import Foundation
import Testing

@testable import AgentCommand

@Test func harnessesHaveTheirDaemonNames() {
    #expect(Harness.allCases.map(\.rawValue) == ["claude", "antigravity", "terminal"])
    #expect(Harness.allCases.map(\.title) == ["Claude", "Antigravity", "Terminal"])
    #expect(Harness.allCases.map(\.id) == ["claude", "antigravity", "terminal"])
    #expect(Harness.defaultForNew == .claude, "⌘N starts Claude")
    #expect(Harness.terminal.panelPrompt == "Open Terminal")
    #expect(Harness.antigravity.panelPrompt == "Start Session" && Harness.claude.panelPrompt == "Start Session")
    #expect(Harness(session: "") == .claude && Harness(session: "antigravity") == .antigravity)
    #expect(Harness(session: "codex") == nil)
    #expect(Harness.agents == [.claude, .antigravity])
    #expect(Harness.claude.productName == "Claude Code" && Harness.antigravity.productName == "Antigravity")
    #expect(Harness.claude.conversationTitle == "Claude session")
    #expect(Harness.antigravity.conversationTitle == "Antigravity conversation")
}

private func conversation(_ id: String, _ title: String, harness: String? = nil, running: Bool = false) throws -> Conversation {
    var fields: [String: Any] = ["id": id, "cwd": "/w", "title": title, "updated_at": "2026-11-07T10:00:00Z", "running": running]
    if let harness { fields["harness"] = harness }
    return try JSONDecoder().decode(Conversation.self, from: JSONSerialization.data(withJSONObject: fields))
}

@Test func theResumePickerListsBothAgents() throws {
    let claude = try conversation("aaaa1111", "Fix the parser", running: true)
    let gravity = try conversation("bbbb2222", "Plan the release", harness: "antigravity")
    var choice = ResumeChoice([claude, gravity])
    #expect(choice.rows == [.conversation(claude), .conversation(gravity)])
    #expect(choice.chosen?.target == ResumeTarget(id: "aaaa1111", harness: .claude, isRunning: true))
    choice.move(1)
    #expect(choice.chosen?.target == ResumeTarget(id: "bbbb2222", harness: .antigravity), "resumed as Antigravity's")
    choice.move(1)
    #expect(choice.chosen == .conversation(claude), "it wraps around")
    choice.select(.conversation(gravity))
    #expect(choice.chosen == .conversation(gravity))
    #expect(Set(choice.rows.map(\.id)).count == 2)

    choice.setSearch("release")
    #expect(choice.rows == [.conversation(gravity)] && choice.typedID == nil)
    choice.setSearch("Antigravity")
    #expect(choice.rows == [.conversation(gravity)], "the agent's name finds its conversations")
}

@Test func aTypedIDAsksWhoseItIs() throws {
    var choice = ResumeChoice([try conversation("aaaa1111", "Fix the parser")])
    choice.setSearch("aaaa")
    #expect(choice.typedID == nil, "it's listed")
    choice.setSearch(" 0123ABCD-ef ")
    #expect(choice.typedID == "0123abcd-ef")
    #expect(choice.rows == [.typed("0123abcd-ef", .claude), .typed("0123abcd-ef", .antigravity)])
    #expect(choice.chosen?.target == ResumeTarget(id: "0123abcd-ef", harness: .claude), "Claude's first")
    choice.move(1)
    #expect(choice.chosen?.target == ResumeTarget(id: "0123abcd-ef", harness: .antigravity))
    #expect(choice.rows.map(\.id) == ["typed:claude:0123abcd-ef", "typed:antigravity:0123abcd-ef"])
    choice.setSearch("0123abcd-ee")
    #expect(choice.chosen == .typed("0123abcd-ee", .claude), "a new search starts at the top")
    choice.setSearch("0123")
    #expect(choice.typedID == nil && choice.rows.isEmpty && choice.chosen == nil, "too short for an id")
    choice.move(1)
    #expect(choice.chosen == nil)
}

@Test func thePickerMovesAndPicks() {
    var choice = HarnessChoice()
    #expect(choice.matches == [.claude, .antigravity, .terminal])
    #expect(choice.chosen == .claude, "the default comes first")
    choice.move(1)
    #expect(choice.chosen == .antigravity)
    choice.move(1)
    choice.move(1)
    #expect(choice.chosen == .claude, "it wraps around")
    choice.move(-1)
    #expect(choice.chosen == .terminal)
    choice.select(.antigravity)
    #expect(choice.chosen == .antigravity, "the mouse moves it too")
    #expect(choice.number(of: .claude) == 1 && choice.number(of: .terminal) == 3)
}

@Test func typingNarrowsThePicker() {
    var choice = HarnessChoice()
    choice.move(2)
    #expect(choice.setSearch("ANTI") == nil)
    #expect(choice.matches == [.antigravity] && choice.chosen == .antigravity, "the selection starts over")
    #expect(choice.number(of: .antigravity) == 2, "rows keep their numbers")
    #expect(choice.setSearch("zz") == nil)
    #expect(choice.matches.isEmpty && choice.chosen == nil)
    choice.move(1)
    #expect(choice.chosen == nil, "nothing to move through")
    choice.select(.claude)
    #expect(choice.chosen == nil)
    #expect(choice.setSearch("") == nil && choice.matches.count == 3)
}

@Test func aNumberPicksStraightAway() {
    var choice = HarnessChoice()
    #expect(choice.setSearch("1") == .claude)
    #expect(choice.setSearch("2") == .antigravity)
    #expect(choice.setSearch("3") == .terminal)
    #expect(choice.search.isEmpty, "the number isn't kept as a search")
    #expect(choice.setSearch("4") == nil && choice.matches.isEmpty)
    #expect(HarnessChoice([.terminal]).matches == [.terminal])
}
