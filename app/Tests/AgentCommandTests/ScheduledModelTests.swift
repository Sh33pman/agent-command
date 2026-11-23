import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// The Scheduled view's model, with jobs as the store hands them over and no daemon.
@MainActor
private final class Board {
    let store = SessionStore()
    let model: ScheduledModel
    /// The daemon, for folders and prompt files.
    let fake = FakeCalls()
    var closed = false
    var shown: String?

    init(jobs: [Job], sessions: [Session] = []) throws {
        try isolateFromTheRealDaemon()
        _ = NSApplication.shared
        store.jobs = jobs
        store.sessions = sessions
        model = ScheduledModel(store: store)
        model.checkDelay = 0
        model.runAcmd = { _, done in done(false, "tests don't run acmd") }
        model.calls = fake
        store.jobCalls = fake
        model.folders = RemoteHostsClient(calls: fake)
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

    #expect(try !board.press(2, "d"), "plain d is the Inbox's dismiss, not duplicate")
    #expect(!model.isEditing)
    try board.press(2, "d", flags: .command)
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
    #expect(!model.confirmingDelete)
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
    try await until("the check to come back") { model.error != nil }
    #expect(model.error?.contains("background service") == true)
    #expect(model.problem("name") == nil)
    try board.press(1, "s", flags: .command)
    #expect(board.fake.ops.last == "job.save" && board.fake.last["name"] as? String == "x", "⌘S saves")
}

/// Folders and files are chosen with the daemon's folder ops: the same chooser
/// as for a host.
@MainActor @Test func pathsComeFromTheDaemonsChooser() throws {
    let board = try Board(jobs: [])
    let model = board.model
    model.newJob()
    board.fake.replies["folders.recent"] = ["home": "/Users/me", "folders": [["name": "notes", "path": "/w/notes"]]]
    board.fake.replies["folders.list"] = ["path": "/w/notes", "home": "/Users/me", "folders": [[String: Any]](),
                                          "files": [["name": "brief.md", "path": "/w/notes/brief.md"]]]

    model.chooseFolder()
    let folder = try #require(model.chooser)
    #expect(!folder.picksFile && model.chooserTitle == "Choose the job's folder" && folder.host == nil && folder.hostName == "this Mac")
    folder.start()
    folder.submit() // the recent folder
    #expect(model.draft?.cwd == "/w/notes" && model.chooser == nil)

    model.choosePromptFile()
    let file = try #require(model.chooser)
    file.start()
    file.submit() // a recent folder opens when choosing a file
    #expect(board.fake.ops.last == "folders.list" && board.fake.last["files"] as? Bool == true)
    file.select(.file(RemoteFolder(name: "brief.md", path: "/w/notes/brief.md")))
    file.submit()
    #expect(model.draft?.promptFile == "/w/notes/brief.md" && model.draft?.promptSource == .file)

    model.addPath(to: "add-dir", directory: true)
    model.chooser?.choose("/a")
    model.addPath(to: "add-dir", directory: true)
    model.chooser?.choose("/b")
    #expect(model.draft?.settings["add-dir"] == "/a\n/b")
    var chosen = ""
    model.chooseSettingFile { chosen = $0 }
    #expect(model.chooser?.fileExts == [".json"])
    model.chooser?.choose("/w/mcp.json")
    #expect(chosen == "/w/mcp.json")
    model.chooseFolder()
    model.closeChooser() // cancelled
    #expect(model.draft?.cwd == "/w/notes" && model.chooser == nil)
}

/// A prompt file is read and written through the daemon: a saved job's own by
/// its id, a draft's by its folder and path.
@MainActor @Test func promptFilesGoThroughTheDaemon() throws {
    var saved = job("J", "brief")
    saved.promptFile = "brief.md"
    saved.prompt = ""
    let board = try Board(jobs: [saved])
    let model = board.model
    model.newJob()
    #expect(model.promptFileRef == nil)
    model.loadPromptFile()
    #expect(model.promptFile == nil && board.fake.ops.isEmpty, "no file: nothing to read")
    model.draft?.cwd = "/w"
    model.draft?.promptFile = "notes.md"
    #expect(model.promptFileRef == PromptFileRef(cwd: "/w", path: "notes.md"))
    board.fake.replies["job.prompt.read"] = ["path": "/w/notes.md", "text": "# Notes", "exists": true]
    model.loadPromptFile()
    #expect(model.promptFile == PromptFile(path: "/w/notes.md", text: "# Notes", exists: true))
    board.fake.replies["job.prompt.write"] = ["path": "/w/notes.md", "text": "# Changed", "exists": true]
    model.savePromptFile("# Changed")
    #expect(board.fake.last["text"] as? String == "# Changed" && model.promptFile?.text == "# Changed")
    board.fake.failures["job.prompt.write"] = "a prompt file is Markdown or text"
    model.savePromptFile("x")
    #expect(model.error == "Couldn't save the prompt file: a prompt file is Markdown or text")
    board.fake.failures["job.prompt.read"] = "gone"
    model.loadPromptFile()
    #expect(model.promptFile == nil)

    // A saved job's own file, while the form still names it: by its id.
    model.cancel()
    model.select("J")
    model.edit()
    #expect(model.promptFileRef == PromptFileRef(job_id: "J"))
    model.draft?.promptFile = "other.md"
    #expect(model.promptFileRef == PromptFileRef(cwd: "/w", path: "other.md"), "a new path is a draft's")
}

@MainActor @Test func namesForTheDetail() throws {
    var other = job("B", "beta")
    other.groupID = "G"
    let s = try makeSession(id: "S1", name: "standup")
    let term = try makeSession(id: "T1", name: "shell", harness: "terminal")
    let ag = try makeSession(id: "G1", name: "gravity", harness: "antigravity")
    let board = try Board(jobs: [job("A", "alpha"), other], sessions: [s, term, ag])
    board.store.groups = [SessionGroup(id: "G", name: "Scheduled")]
    let model = board.model
    #expect(model.sessionName("S1") == "standup" && model.sessionName("nope") == nil)
    #expect(model.groupName("G") == "Scheduled" && model.groupName("") == "Default")
    #expect(model.continuableSessions.map(\.id) == ["S1"], "terminals without Claude, and other agents, can't continue")

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

@MainActor @Test func wakingTheMacRunsAcmd() throws {
    let board = try Board(jobs: [job("A", "alpha")])
    let model = board.model
    #expect(model.wake == nil)
    board.store.wake = WakeState()
    #expect(model.wake == WakeState())

    var ran: [[String]] = []
    var finish: ((Bool, String) -> Void)?
    model.runAcmd = { args, done in
        ran.append(args)
        finish = done
    }
    model.turnWakeOn()
    #expect(model.wakeBusy)
    model.turnWakeOff() // ignored while one runs
    #expect(ran == [["schedule", "wake", "on"]])
    finish?(true, "")
    #expect(!model.wakeBusy && model.notice == "Waking the Mac is on." && model.error == nil)

    model.setWakeAll(true)
    finish?(true, "")
    model.setWakeAll(false)
    finish?(true, "")
    #expect(model.notice == "Only jobs set to wake the Mac do.")
    model.turnWakeOff()
    finish?(false, "acmd: removing the rule: canceled")
    #expect(model.error == nil, "a canceled password dialog isn't an error")
    model.turnWakeOff()
    finish?(false, "acmd: removing the rule: exit status 1")
    #expect(model.error == "acmd: removing the rule: exit status 1")
    model.turnWakeOn()
    finish?(false, "")
    #expect(model.error == "Couldn't change waking the Mac.")
    #expect(ran == [["schedule", "wake", "on"], ["schedule", "wake", "on", "--all=true"], ["schedule", "wake", "on", "--all=false"],
                    ["schedule", "wake", "off"], ["schedule", "wake", "off"], ["schedule", "wake", "on"]])
}

/// The real runner, with a fake acmd in ACMD_BIN (shared with the speech
/// tests, so in their serialized suite): nothing asks for a password.
extension UsesSharedSpeech {
@Suite @MainActor
struct ScheduledAcmdTests {
    @Test func theRunnerRunsTheAppsAcmd() async throws {
        let dir = NSTemporaryDirectory() + "acmd-wake-tests-\(getpid())"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let path = dir + "/fake-acmd"
        try "#!/bin/sh\necho \"ran $*\"\n[ \"$3\" = on ]\n".write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: path)
        setenv("ACMD_BIN", path, 1)
        defer { unsetenv("ACMD_BIN") }
        let run = ScheduledModel(store: SessionStore()).runAcmd

        func result(_ args: [String]) async -> (Bool, String) {
            await withCheckedContinuation { done in run(args) { ok, out in done.resume(returning: (ok, out)) } }
        }
        var (ok, out) = await result(["schedule", "wake", "on"])
        #expect(ok && out == "ran schedule wake on")
        (ok, out) = await result(["schedule", "wake", "off"])
        #expect(!ok && out == "ran schedule wake off")
        setenv("ACMD_BIN", dir + "/missing", 1)
        (ok, out) = await result(["x"])
        #expect(!ok && !out.isEmpty)
        setenv("ACMD_BIN", "no-such-acmd-on-path", 1)
        (ok, _) = await result(["x"])
        #expect(!ok)
    }
}
}

/// The app's keys mean the same here, applied to the job: ⌘R edits it, ⇧⌘⌫ deletes it,
/// ⌘W closes Scheduled. The panes behind it don't take keys.
@MainActor @Test func theAppKeysActOnTheJob() throws {
    let board = try Board(jobs: [job("A", "alpha"), job("B", "beta")])
    let model = board.model
    try board.press(15, "r", flags: .command)
    #expect(model.isEditing && model.draft?.name == "alpha")
    try board.press(13, "w", flags: .command)
    #expect(!model.isEditing && !board.closed, "⌘W leaves the form first")
    #expect(try board.press(36, "\r", flags: .command), "no split behind Scheduled")
    #expect(try board.press(38, "j", flags: [.command, .control]), "no pane to focus")
    try board.press(51, flags: [.command, .shift])
    #expect(model.confirmingDelete)
    try board.press(53)
    try board.press(13, "w", flags: .command)
    #expect(board.closed)
}

@MainActor @Test func eachAgentContinuesItsOwnSessions() throws {
    let claude = try makeSession(id: "C1", name: "claude")
    let typed = try makeSession(id: "T1", name: "typed", harness: "terminal", claude: "s-1")
    let fresh = try makeSession(id: "G0", name: "fresh", harness: "antigravity")
    let hooked = try makeSession(id: "G1", name: "hooked", harness: "antigravity", claude: "conv-1")
    let board = try Board(jobs: [], sessions: [claude, typed, fresh, hooked])
    let model = board.model
    #expect(model.continuableSessions(for: .claude).map(\.id) == ["C1", "T1"], "not Antigravity's, though it has an id")
    #expect(model.continuableSessions(for: .antigravity).map(\.id) == ["G1"], "only once it has its conversation")
    #expect(model.harness == .claude && model.continuableSessions.map(\.id) == ["C1", "T1"])
    model.newJob()
    model.draft?.setHarness(.antigravity)
    #expect(model.harness == .antigravity && model.continuableSessions.map(\.id) == ["G1"], "the form's agent")
}

@MainActor @Test func changingTheAgentIsAChange() throws {
    let board = try Board(jobs: [])
    let model = board.model
    model.newJob()
    model.receive(check: ["harness": "Antigravity isn't installed."])
    #expect(model.problem("harness") == nil, "not until it's changed")
    model.draft?.setHarness(.antigravity)
    #expect(model.problem("harness") == "Antigravity isn't installed.")
}

extension UsesSharedSpeech {
    @Suite @MainActor
    struct ScheduledAgentSettingsTests {
        @Test func theFormShowsItsAgentsSettings() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("harness.settings", [["key": "model", "label": "Model", "kind": "text"]])
            var gravityJob = job("G", "gravity")
            gravityJob.harnessID = "HG"
            gravityJob.settings = ["mode": ["plan"]]
            let board = try Board(jobs: [job("A", "alpha"), gravityJob])
            let model = board.model
            try await until { !model.settings(for: .claude).isEmpty }
            #expect(daemon.received("harness.settings").map { $0["kind"] as? String } == ["claude"], "the default's kind")
            #expect(model.settings.map(\.label) == ["Model"] && model.settings(for: .antigravity).isEmpty)

            daemon.reply("harness.settings", [["key": "mode", "label": "Mode", "kind": "choice", "choices": ["plan", "fast"]]])
            model.newJob()
            model.draft?.setHarness(.antigravity)
            try await until { !model.settings(for: .antigravity).isEmpty }
            #expect(daemon.received("harness.settings").last?["kind"] as? String == "antigravity")
            #expect(model.harness.isAntigravity && model.settings.map(\.key) == ["mode"], "the form's harness's kind")
            model.cancel(force: true)

            model.select("A")
            model.newJob()
            #expect(model.settings.map(\.key) == ["model"])
            model.draft?.setHarness(.antigravity)
            #expect(model.settings.map(\.key) == ["mode"], "the form's agent")
            #expect(daemon.received("harness.settings").count == 2, "each kind's are asked for once")
        }
    }
}

/// A save refused because the job changed since the form opened keeps your
/// changes, says what changed once the job's new version arrives, and the
/// next save sends that version, replacing it on purpose.
@MainActor @Test func aRefusedSaveKeepsYourChanges() throws {
    var loaded = job("A", "alpha")
    loaded.version = "v1"
    let board = try Board(jobs: [loaded])
    let model = board.model
    model.edit()
    model.draft?.name = "Mine"
    board.fake.failures["job.save"] = "alpha changed since you opened it"
    board.fake.codes["job.save"] = "conflict"
    model.save()
    #expect(board.fake.last["version"] as? String == "v1", "it sends the version it loaded")
    #expect(model.isEditing && model.draft?.name == "Mine" && model.error?.hasSuffix("Getting the latest…") == true)

    // The job as it is now arrives.
    var theirs = loaded
    theirs.version = "v2"
    theirs.name = "Theirs"
    theirs.prompt = "# New"
    board.store.jobs = [theirs]
    model.jobsChanged()
    #expect(model.draft?.name == "Mine" && model.draft?.version == "v2")
    #expect(model.error?.contains("(its name, prompt)") == true && model.error?.contains("Save again") == true)
    #expect(model.isDirty, "measured against the job as it is now")

    board.fake.failures = [:]
    var saved = theirs
    saved.name = "Mine"
    saved.version = "v3"
    board.fake.replies["job.save"] = try JSONSerialization.jsonObject(with: JSONEncoder().encode(saved))
    model.save()
    #expect(board.fake.last["version"] as? String == "v2" && !model.isEditing && model.error == nil)
}

/// A job deleted while you edit it: Save adds it again.
@MainActor @Test func aJobDeletedWhileYouEditItIsAddedAgain() throws {
    var loaded = job("A", "alpha")
    loaded.version = "v1"
    let board = try Board(jobs: [loaded])
    let model = board.model
    model.edit()
    board.store.jobs = []
    board.fake.failures["job.save"] = "changed"
    board.fake.codes["job.save"] = "conflict"
    model.save()
    #expect(model.error?.contains("was deleted") == true && model.draft?.isNew == true && model.draft?.version == "")
    #expect(JobDraft(loaded).duplicate().version == "", "a copy is a new job")
}
