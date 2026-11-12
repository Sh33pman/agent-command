import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// The Scheduled view's model, with jobs as the store hands them over and no daemon.
@MainActor
private final class Board {
    let store = SessionStore()
    let model: ScheduledModel
    var picked: [String?] = []
    var closed = false
    var shown: String?

    init(jobs: [Job], sessions: [Session] = []) throws {
        try isolateFromTheRealDaemon()
        _ = NSApplication.shared
        store.jobs = jobs
        store.sessions = sessions
        model = ScheduledModel(store: store)
        model.checkDelay = 0
        model.choosePath = { [unowned self] _, _ in self.picked.isEmpty ? nil : self.picked.removeFirst() }
        model.close = { [unowned self] in self.closed = true }
        model.goToSession = { [unowned self] in self.shown = $0 }
        model.start()
    }

    @discardableResult
    func press(_ code: UInt16, _ characters: String = "", flags: NSEvent.ModifierFlags = []) throws -> Bool {
        model.handle(try keyDown(code, characters, flags: flags))
    }

    deinit { MainActor.assumeIsolated { model.stop() } }
}

private func job(_ id: String, _ name: String, cwd: String = "/w") -> Job {
    var j = Job()
    j.id = id
    j.name = name
    j.cwd = cwd
    j.schedule = "every day at 08:00"
    j.words = "every day at 08:00"
    j.prompt = "# \(name)"
    return j
}

@MainActor @Test func theListMovesAndOpensTheForm() throws {
    let board = try Board(jobs: [job("A", "alpha"), job("B", "beta"), job("C", "gamma")])
    let model = board.model
    #expect(model.selectedID == "A", "the first job, to start")
    try board.press(125) // down
    try board.press(38, "j")
    #expect(model.selectedID == "C")
    try board.press(125)
    #expect(model.selectedID == "C", "stops at the end")
    try board.press(40, "k")
    try board.press(126) // up
    #expect(model.selectedID == "A")

    try board.press(36) // return: edit
    #expect(model.isEditing && model.draft?.name == "alpha" && !model.isDirty)
    #expect(try !board.press(38, "j"), "keys go to the fields while editing")
    model.select("B")
    #expect(model.selectedID == "A", "the list can't change under the form")
    try board.press(53) // esc: nothing changed, so it just closes
    #expect(!model.isEditing)

    try board.press(45, "n")
    #expect(model.draft?.isNew == true && model.draft?.cwd == "/w", "a new job starts in the selected one's folder")
    board.model.cancel()

    try board.press(2, "d")
    #expect(model.draft?.name == "alpha copy" && model.isDirty, "a copy counts as a change")
    try board.press(53)
    #expect(model.confirmingDiscard)
    try board.press(53)
    #expect(!model.confirmingDiscard && model.isEditing, "esc keeps editing")
    try board.press(53)
    try board.press(36) // return: discard
    #expect(!model.isEditing)

    #expect(try !board.press(45, "n", flags: .command), "⌘ keys are the app's")
    #expect(try !board.press(0, "a"), "keys it doesn't use pass through")
    try board.press(53)
    #expect(board.closed)
}

@MainActor @Test func deletingAsksFirst() throws {
    let board = try Board(jobs: [job("A", "alpha"), job("B", "beta")])
    let model = board.model
    model.select("B")
    try board.press(51) // delete
    #expect(model.confirmingDelete && board.store.jobs.count == 2)
    try board.press(53)
    #expect(!model.confirmingDelete && board.store.jobs.count == 2)
    try board.press(117) // forward delete
    try board.press(0, "a")
    #expect(model.confirmingDelete, "other keys don't answer it")
    try board.press(36)
    #expect(board.store.jobs.map(\.id) == ["A"] && model.selectedID == "A")
    #expect(model.notice == "Deleted beta.")
}

@MainActor @Test func theFormChecksAsYouType() async throws {
    let board = try Board(jobs: [])
    let model = board.model
    #expect(model.selected == nil)
    model.newJob()
    model.draft?.name = "x"
    #expect(model.canSave, "before a check comes back, the daemon has the last word")
    // No daemon here: the check says so instead of hanging.
    try await Task.sleep(for: .milliseconds(300))
    #expect(model.error?.contains("background service") == true)
    #expect(model.problem("name") == nil)
    try board.press(1, "s", flags: .command)
    #expect(model.saving || model.error != nil)
}

@MainActor @Test func pathsComeFromThePicker() throws {
    let board = try Board(jobs: [])
    let model = board.model
    model.newJob()
    board.picked = ["/w/notes", "/w/notes/brief.md", "/a", "/b", nil]
    model.chooseFolder()
    model.choosePromptFile()
    #expect(model.draft?.cwd == "/w/notes" && model.draft?.promptFile == "/w/notes/brief.md" && model.draft?.promptSource == .file)
    model.addPath(to: "add-dir", directory: true)
    model.addPath(to: "add-dir", directory: true)
    #expect(model.draft?.settings["add-dir"] == "/a\n/b")
    model.chooseFolder() // cancelled
    #expect(model.draft?.cwd == "/w/notes")
}

@MainActor @Test func promptFilesAreEditedInPlace() throws {
    let board = try Board(jobs: [])
    let model = board.model
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("acmd-jobs-\(UUID().uuidString)")
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: dir) }
    try "# Brief".write(to: dir.appendingPathComponent("brief.md"), atomically: true, encoding: .utf8)

    model.newJob()
    #expect(model.promptFilePath == nil && model.readPromptFile() == nil && !model.writePromptFile("x"))
    model.draft?.cwd = dir.path
    model.draft?.promptFile = "brief.md"
    #expect(model.promptFilePath == dir.appendingPathComponent("brief.md").path, "relative to the folder")
    #expect(model.readPromptFile() == "# Brief")
    #expect(model.writePromptFile("# Changed"))
    #expect(model.readPromptFile() == "# Changed")
    model.draft?.promptFile = dir.appendingPathComponent("brief.md").path
    #expect(model.readPromptFile() == "# Changed", "or a full path")
    model.draft?.promptFile = "/no/such/folder/x.md"
    #expect(!model.writePromptFile("x") && model.error?.contains("Couldn't save") == true)
    model.draft?.cwd = ""
    model.draft?.promptFile = "relative.md"
    #expect(model.promptFilePath == nil)
}

@MainActor @Test func namesForTheDetail() throws {
    var other = job("B", "beta")
    other.groupID = "G"
    let s = try makeSession(id: "S1", name: "standup")
    let term = try makeSession(id: "T1", name: "shell", harness: "terminal")
    let board = try Board(jobs: [job("A", "alpha"), other], sessions: [s, term])
    board.store.groups = [SessionGroup(id: "G", name: "Scheduled")]
    let model = board.model
    #expect(model.sessionName("S1") == "standup" && model.sessionName("nope") == nil)
    #expect(model.groupName("G") == "Scheduled" && model.groupName("") == "Default")
    #expect(model.continuableSessions.map(\.id) == ["S1"], "terminals without Claude can't continue")

    // A job deleted elsewhere moves the selection on.
    model.select("B")
    board.store.jobs = [job("A", "alpha")]
    model.jobsChanged()
    #expect(model.selectedID == "A")
    try board.press(5, "g")
    #expect(board.shown == nil, "no runs to go to")
    model.togglePause()
    #expect(board.store.jobs[0].paused && model.notice == "Paused alpha.")
}

@MainActor @Test func aNewJobsProblemsWaitUntilYouTouchThem() throws {
    let board = try Board(jobs: [job("A", "alpha")])
    let model = board.model
    model.newJob()
    model.receive(check: ["name": "Give the job a name.", "prompt": "Write a prompt.", "settings": "Bad."])
    #expect(model.problem("name") == nil && model.problem("prompt") == nil, "an empty form isn't wrong yet")
    #expect(model.problem("settings") == "Bad.", "what you can't have left alone shows at once")
    model.draft?.name = "x"
    model.receive(check: ["name": "Too short.", "prompt": "Write a prompt."])
    #expect(model.problem("name") == "Too short." && model.problem("prompt") == nil)
    model.save()
    #expect(model.problem("prompt") == "Write a prompt.", "Save shows them all")
    #expect(!model.saving, "and doesn't save what's wrong")

    // An existing job shows its problems straight away.
    model.cancel(force: true)
    model.edit()
    model.receive(check: ["cwd": "Gone."])
    #expect(model.problem("cwd") == "Gone.")
}

@Test func aRowSaysWhereInAWord() {
    var j = job("A", "alpha", cwd: "/Users/me/notes")
    #expect(j.placeName { _ in nil } == "notes")
    j.place = "worktree"
    #expect(j.placeName { _ in nil } == "worktree of notes")
    j.place = "session"
    j.sessionID = "S"
    #expect(j.placeName { _ in "standup" } == "standup" && j.placeName { _ in nil } == "a session that's gone")
    j.place = "folder"
    j.cwd = ""
    #expect(j.placeName { _ in nil } == "no folder")
    #expect(MarkdownCommand.heading2.label == "H2" && MarkdownCommand.bold.label == nil)
}
