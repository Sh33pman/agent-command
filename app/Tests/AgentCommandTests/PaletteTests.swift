import Testing

@testable import AgentCommand

private func titles(_ entries: [PaletteEntry], in section: String) -> [String] {
    entries.filter { $0.section == section }.map(\.title)
}

private func entry(_ entries: [PaletteEntry], _ section: String, _ title: String) -> PaletteEntry? {
    entries.first { $0.section == section && $0.title == title }
}

@Test func paletteWithNothingShowingHasTheAppWideCommands() {
    let entries = Palette.entries(PaletteState())
    #expect(titles(entries, in: "New") == [
        "Claude session", "Antigravity session", "Terminal", "New session…", "Resume a session…",
    ])
    #expect(entry(entries, "New", "Claude session")?.action == .newSession(harness: "claude"))
    #expect(entry(entries, "New", "Claude session")?.shortcut == "⌘N")
    #expect(entry(entries, "New", "Antigravity session")?.action == .newSession(harness: "antigravity"))
    #expect(entry(entries, "New", "Terminal")?.action == .newSession(harness: "terminal"))
    #expect(entry(entries, "New", "New session…")?.action == .chooseHarness)
    #expect(entry(entries, "New", "New session…")?.shortcut == "⇧⌘N")
    #expect(titles(entries, in: "Panes") == [
        "Split right", "Split down", "Split right with agent", "Split down with agent", "Split right…", "Split down…", "Close pane",
    ], "no focus commands for one pane")
    #expect(titles(entries, in: "Screen").isEmpty, "no screen commands without a terminal")
    #expect(titles(entries, in: "Go to").isEmpty, "nothing needs you, and no sessions")
    #expect(entry(entries, "App", "Quit")?.action == .quit)
    #expect(entry(entries, "App", "Report a bug…")?.action == .report(.bug))
    #expect(entry(entries, "App", "Request a feature…")?.action == .report(.feature))
    #expect(entry(entries, "App", "Read the latest message aloud") == nil, "nothing to read")
    let ids = entries.map { $0.section + "/" + $0.title + "/" + $0.key }
    #expect(Set(ids).count == ids.count, "every entry has its own id")
}

@Test func antigravitySessionsCopyTheirConversationID() throws {
    let hooked = try makeSession(id: "g1", name: "grav", harness: "antigravity", claude: "conv-9")
    let entries = Palette.entries(PaletteState(targets: [hooked], sessions: [hooked], setMembers: []))
    #expect(entry(entries, "Antigravity session", "Copy Antigravity conversation ID")?.action == .copy("conv-9"))
    #expect(entry(entries, "Antigravity session", "Copy Claude session ID") == nil)
    let fresh = try makeSession(id: "g2", name: "new", harness: "antigravity")
    let before = Palette.entries(PaletteState(targets: [fresh], sessions: [fresh], setMembers: []))
    #expect(entry(before, "Antigravity session", "Copy Antigravity conversation ID") == nil, "no id before its first hook")
}

@Test func paletteActsOnTheOneSessionShowing() throws {
    let claude = try makeSession(id: "c1", name: "api", cwd: "/w/api", claude: "abc-123")
    let other = try makeSession(id: "t1", name: "shell", harness: "terminal", status: "running")
    let entries = Palette.entries(PaletteState(targets: [claude], sessions: [claude, other], setMembers: [["c1", "t1"]]))
    #expect(titles(entries, in: "Claude session") == [
        "Rename…", "Archive…", "Turn Remote on", "Move out of set", "Open in editor", "Copy folder path", "Copy Claude session ID",
    ])
    #expect(entry(entries, "Claude session", "Copy folder path")?.action == .copy("/w/api"))
    #expect(entry(entries, "Claude session", "Copy Claude session ID")?.action == .copy("abc-123"))
    #expect(entry(entries, "Claude session", "Move out of set")?.action == .moveOut(sessionID: "c1"))
    #expect(entry(entries, "Claude session", "Archive…")?.action == .archive([claude]))
    #expect(entry(entries, "App", "Inbox for api")?.action == .inboxFor(claude))
    let goTo = entries.filter { $0.section == "Go to" }
    #expect(goTo.map(\.title) == ["shell"], "the session showing isn't listed to go to")
    #expect(goTo.first?.shortcut == "running")
    #expect(goTo.first?.action == .select(sessionID: "t1"))
}

@Test func paletteForATerminalHasNoClaudeID() throws {
    let terminal = try makeSession(id: "t1", harness: "terminal")
    let entries = Palette.entries(PaletteState(targets: [terminal], sessions: [terminal]))
    #expect(titles(entries, in: "Terminal session") == ["Rename…", "Archive…", "Turn Remote on", "Open in editor", "Copy folder path"])
    #expect(entry(entries, "Terminal session", "Open in editor")?.shortcut == "⇧⌘E")
    let nowhere = try makeSession(id: "n1", cwd: "", harness: "terminal")
    #expect(!titles(Palette.entries(PaletteState(targets: [nowhere])), in: "Terminal session").contains("Open in editor"))
}

@Test func paletteForAntigravityHasNoClaudeID() throws {
    let agent = try makeSession(id: "g1", harness: "antigravity")
    let entries = Palette.entries(PaletteState(targets: [agent], sessions: [agent]))
    #expect(titles(entries, in: "Antigravity session") == ["Rename…", "Archive…", "Turn Remote on", "Open in editor", "Copy folder path"])
    let unnamed = try makeSession(id: "x1", harness: "codex")
    #expect(titles(Palette.entries(PaletteState(targets: [unnamed])), in: "Agent session").first == "Rename…")
    #expect(Palette.archivePrompt([agent])?.note.hasPrefix("Agents stop.") == true, "it can be unarchived, as Claude can")
}

@Test func paletteActsOnASelectionTogether() throws {
    let a = try makeSession(id: "a"), b = try makeSession(id: "b")
    let entries = Palette.entries(PaletteState(targets: [a, b], sessions: [a, b]))
    #expect(entry(entries, "2 sessions", "Rename…")?.action == .rename([a, b]))
    #expect(entry(entries, "2 sessions", "Archive…")?.action == .archive([a, b]))
    #expect(titles(entries, in: "Go to") == ["a", "b"])
    #expect(!entries.contains { $0.title.hasPrefix("Inbox for") }, "no single session to show")
}

@Test func paletteListsPaneAndScreenCommandsWhenThereAreSome() {
    let entries = Palette.entries(PaletteState(paneCount: 2, showsScreen: true))
    #expect(titles(entries, in: "Panes").count == 12)
    #expect(entry(entries, "Panes", "Focus up")?.action == .focus(.up))
    #expect(entry(entries, "Panes", "Equalize panes")?.action == .equalizePanes)
    #expect(entry(entries, "Panes", "Split down")?.action == .split(.down, .terminal))
    #expect(entry(entries, "Panes", "Split right with agent")?.action == .split(.right, .agent))
    #expect(entry(entries, "Panes", "Split down…")?.action == .split(.down, .ask))
    #expect(titles(entries, in: "Screen").count == Palette.screenCommands.count)
    #expect(entry(entries, "Screen", "Paste")?.action == .screen(binding: "paste_from_clipboard"))
}

@Test func paletteTogglesSayWhatTheyllDo() throws {
    let claude = try makeSession(id: "c1")
    var state = PaletteState(targets: [claude], sessions: [claude])
    var entries = Palette.entries(state)
    #expect(entry(entries, "App", "Turn speech on")?.action == .toggleSpeech)
    #expect(entry(entries, "Sessions", "Turn Agent Remote Control on for new sessions")?.action == .toggleAgentRemoteControl)
    #expect(entry(entries, "App", "Read aloud in the popup") != nil)
    #expect(entry(entries, "App", "Turn popup off")?.action == .togglePopup)
    #expect(titles(entries, in: "App").filter { $0.hasPrefix("Popup style") }.count == ReplyPopupStyle.allCases.count - 1)
    #expect(titles(entries, in: "App").filter { $0.hasPrefix("Popup opens") }.count == PopupOpens.allCases.count - 1)
    #expect(entries.contains { $0.action == .popupOpens(.everything) })
    #expect(!entries.contains { $0.action == .popupOpens(.needsYou) }, "the current choice isn't offered")
    #expect(!entries.contains { $0.action == .popupStyle(.window) })

    state.speechOn = true
    state.agentRemoteControl = true
    state.popupReads = true
    state.popupOn = false
    entries = Palette.entries(state)
    #expect(entry(entries, "App", "Turn speech off") != nil)
    #expect(entry(entries, "App", "Read the latest message aloud")?.action == .toggleReading)
    #expect(entry(entries, "Sessions", "Turn Agent Remote Control off for new sessions") != nil)
    #expect(entry(entries, "App", "Stop reading aloud in the popup")?.action == .togglePopupReading)
    #expect(entry(entries, "App", "Turn popup on") != nil)

    state.speaking = true
    state.targets = []
    entries = Palette.entries(state)
    #expect(entry(entries, "App", "Stop reading")?.action == .toggleReading, "stopping works from anywhere")
}

@Test func archivingSaysWhatHappensToEachKind() throws {
    let claude = try makeSession(id: "c1", name: "api", cwd: "/w")
    let terminal = try makeSession(id: "t1", harness: "terminal")
    #expect(Palette.archivePrompt([]) == nil)
    let one = try #require(Palette.archivePrompt([claude]))
    #expect(one.title == "Archive api?")
    #expect(one.note == "Agents stop. You can unarchive them later and continue the conversation.")
    let both = try #require(Palette.archivePrompt([claude, terminal]))
    #expect(both.title == "Archive 2 sessions?")
    #expect(both.note.hasPrefix("Terminals end") && both.note.hasSuffix("continue the conversation."))
}

@Test func archivingASessionFromAHostSaysItEndsThere() throws {
    let added = try makeRemoteSession(id: "api", host: "studio")
    let other = try makeRemoteSession(id: "web", host: "laptop")
    let gone = try makeRemoteSession(id: "old", host: "studio", gone: true)
    let local = try makeRemoteSession(id: "here")
    let one = try #require(Palette.archivePrompt([added]))
    #expect(one.note.hasSuffix("It ends on studio too. To keep it running there, use Remove from This Mac instead."))
    let mixed = try #require(Palette.archivePrompt([local, added]))
    #expect(mixed.note.contains("api ends on studio too."))
    let both = try #require(Palette.archivePrompt([added, other]))
    #expect(both.note.hasSuffix("Sessions added from a host end there too. To keep them running there, use Remove from This Mac instead."))
    #expect(!(try #require(Palette.archivePrompt([local]))).note.contains("Remove from This Mac"))
    #expect(!(try #require(Palette.archivePrompt([gone]))).note.contains("Remove from This Mac"), "it's gone from there already")
}

@Test func paletteOffersTheNextSessionNeedingYouWhenThereIsOne() throws {
    let waiting = try makeSession(id: "w", status: "waiting")
    #expect(!Palette.entries(PaletteState(sessions: [waiting])).contains { $0.action == .nextAttention })
    let entries = Palette.entries(PaletteState(sessions: [waiting], needsYou: true))
    #expect(entry(entries, "Go to", "Next needing attention")?.shortcut == "⇧⌘A")
    #expect(entries.first { $0.section == "Go to" }?.action == .nextAttention, "first in its section")
}
