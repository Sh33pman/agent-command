import AppKit
import Observation

/// The Scheduled view: the jobs on the left; on the right the selected one and
/// its runs, or the form that adds or edits one.
@Observable
final class ScheduledModel {
    let store: SessionStore
    private(set) var selectedID: String?
    /// The form, while adding or editing a job.
    var draft: JobDraft? {
        didSet { if draft != oldValue, draft != nil { scheduleCheck() } }
    }
    /// The draft as it was when the form opened, to tell whether it changed.
    private(set) var original: JobDraft?
    /// What the daemon makes of the draft.
    private(set) var check = JobCheck()
    private(set) var saving = false
    /// Why the last save, run or check failed.
    private(set) var error: String?
    /// What just happened, for a moment.
    private(set) var notice: String?
    private(set) var runs: [JobRun] = []
    private(set) var settings: [AgentSetting] = []
    /// Asking whether to delete the selected job.
    var confirmingDelete = false
    /// Asking whether to throw away the form's changes.
    var confirmingDiscard = false
    /// Save was pressed: show every field's problem, not only the ones you've changed.
    private(set) var triedSaving = false

    @ObservationIgnored var close: () -> Void = {}
    @ObservationIgnored var goToSession: (String) -> Void = { _ in }
    /// Picks a folder or a file; nil when cancelled. Tests replace it.
    @ObservationIgnored var choosePath: (_ directory: Bool, _ start: String) -> String? = ScheduledModel.openPanel
    /// How long the form waits after a change before checking it.
    @ObservationIgnored var checkDelay: TimeInterval = 0.3
    @ObservationIgnored private var checkToken = 0
    @ObservationIgnored private var noticeTimer: Timer?
    @ObservationIgnored private var monitor: Any?
    @ObservationIgnored var handlesKey: (NSEvent) -> Bool = { _ in true }

    init(store: SessionStore) {
        self.store = store
    }

    var jobs: [Job] { store.jobs }
    var selected: Job? { jobs.first { $0.id == selectedID } }
    var isEditing: Bool { draft != nil }
    var isDirty: Bool { draft != original }
    /// Save stays pressable while there are problems: pressing it shows them all.
    var canSave: Bool { draft != nil && !saving }

    /// The sessions a job can continue: Claude sessions acmd runs.
    var continuableSessions: [Session] {
        store.sessions.filter { !$0.isTerminal || !$0.claudeSessionID.isEmpty }
    }

    func sessionName(_ id: String) -> String? {
        (store.sessions + store.archived).first { $0.id == id }?.displayName
    }

    func groupName(_ id: String) -> String {
        store.groups.first { $0.id == id }?.name ?? "Default"
    }

    // MARK: Lifecycle

    func start(editing job: Job? = nil) {
        store.loadAgentSettings { [weak self] in self?.settings = $0 }
        if let job {
            select(job.id)
            edit()
        } else {
            select(jobs.first?.id)
        }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, self.handlesKey(event) else { return event }
            return self.handle(event) ? nil : event
        }
    }

    func stop() {
        noticeTimer?.invalidate()
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    // MARK: The list

    func select(_ id: String?) {
        guard !isEditing else { return }
        selectedID = id
        confirmingDelete = false
        error = nil
        runs = []
        guard let id else { return }
        store.loadJobRuns(id) { [weak self] runs in
            guard let self, self.selectedID == id else { return }
            self.runs = runs
        }
    }

    func move(by step: Int) {
        guard !jobs.isEmpty else { return }
        let index = jobs.firstIndex { $0.id == selectedID } ?? (step > 0 ? -1 : jobs.count)
        select(jobs[max(0, min(jobs.count - 1, index + step))].id)
    }

    /// The jobs changed under the view (the daemon, or the CLI).
    func jobsChanged() {
        if let id = selectedID, !jobs.contains(where: { $0.id == id }), !isEditing {
            select(jobs.first?.id)
        } else if let id = selectedID, !isEditing {
            store.loadJobRuns(id) { [weak self] runs in
                guard let self, self.selectedID == id else { return }
                self.runs = runs
            }
        }
    }

    // MARK: The form

    func newJob() {
        let folder = selected.map(\.cwd) ?? ""
        open(JobDraft(folder: folder))
    }

    func edit() {
        guard let selected else { return }
        open(JobDraft(selected))
    }

    func duplicate() {
        guard let selected else { return }
        open(JobDraft(selected).duplicate())
        original = nil // a copy is a change, even before it's edited
    }

    private func open(_ form: JobDraft) {
        error = nil
        triedSaving = false
        check = JobCheck()
        original = form
        draft = form
    }

    /// Closes the form; with unsaved changes, asks first unless force.
    func cancel(force: Bool = false) {
        guard isEditing else { return }
        if isDirty && !force {
            confirmingDiscard = true
            return
        }
        confirmingDiscard = false
        draft = nil
        original = nil
        error = nil
        check = JobCheck()
    }

    func save() {
        guard let draft, !saving else { return }
        triedSaving = true
        guard check.isValid else { return }
        saving = true
        error = nil
        store.saveJob(draft.job) { [weak self] result in
            guard let self else { return }
            self.saving = false
            switch result {
            case .success(let job):
                self.draft = nil
                self.original = nil
                self.select(job.id)
                self.show(draft.isNew ? "Added \(job.name)." : "Saved \(job.name).")
            case .failure(let failure):
                self.error = failure.localizedDescription
            }
        }
    }

    /// Checks the draft a moment after it last changed.
    private func scheduleCheck() {
        checkToken += 1
        let token = checkToken
        DispatchQueue.main.asyncAfter(deadline: .now() + checkDelay) { [weak self] in
            guard let self, token == self.checkToken, let draft = self.draft else { return }
            self.store.checkJob(draft.job) { [weak self] check in
                guard let self, token == self.checkToken else { return }
                self.check = check ?? JobCheck()
                self.error = check == nil ? "Couldn't reach acmd's background service to check the job." : nil
            }
        }
    }

    /// Takes a check's errors as if the daemon sent them (tests).
    func receive(check errors: [String: String]) {
        var c = JobCheck()
        c.errors = errors
        check = c
    }

    /// The form's error for a field, if the daemon found one. On a new job, only
    /// once you've changed the field or pressed Save: an empty form isn't wrong yet.
    func problem(_ field: String) -> String? {
        guard let message = check.errors[field] else { return nil }
        guard let draft, let original, draft.isNew, !triedSaving else { return message }
        let changed: Bool = switch field {
        case "name": draft.name != original.name
        case "schedule": draft.schedule != original.schedule
        case "prompt": draft.prompt != original.prompt || draft.promptFile != original.promptFile
        case "cwd": draft.cwd != original.cwd
        case "session": draft.sessionID != original.sessionID
        default: true
        }
        return changed ? message : nil
    }

    func chooseFolder() {
        guard let path = choosePath(true, draft?.cwd ?? "") else { return }
        draft?.cwd = path
    }

    func choosePromptFile() {
        guard let path = choosePath(false, draft?.cwd ?? "") else { return }
        draft?.promptFile = path
        draft?.promptSource = .file
    }

    /// Adds a folder to a setting that takes several (Extra folders).
    func addPath(to key: String, directory: Bool) {
        guard let path = choosePath(directory, draft?.cwd ?? "") else { return }
        let current = draft?.settings[key] ?? ""
        draft?.settings[key] = current.isEmpty ? path : current + "\n" + path
    }

    private static func openPanel(directory: Bool, start: String) -> String? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = directory
        panel.canChooseFiles = !directory
        panel.allowsMultipleSelection = false
        if !directory { panel.allowedContentTypes = [.init(filenameExtension: "md")!, .plainText] }
        if !start.isEmpty { panel.directoryURL = URL(fileURLWithPath: start) }
        return panel.runModal() == .OK ? panel.url?.path : nil
    }

    // MARK: Prompt files

    /// The prompt file's text, to edit it in place; nil when it can't be read.
    func readPromptFile() -> String? {
        guard let path = promptFilePath else { return nil }
        return try? String(contentsOfFile: path, encoding: .utf8)
    }

    /// Writes the editor's text back to the prompt file.
    func writePromptFile(_ text: String) -> Bool {
        guard let path = promptFilePath else { return false }
        do {
            try text.write(toFile: path, atomically: true, encoding: .utf8)
            scheduleCheck()
            return true
        } catch {
            self.error = "Couldn't save \(JobTime.tilde(path)): \(error.localizedDescription)"
            return false
        }
    }

    /// The prompt file's full path: a relative one is from the job's folder.
    var promptFilePath: String? {
        guard let draft, !draft.promptFile.isEmpty else { return nil }
        let file = (draft.promptFile as NSString).expandingTildeInPath
        if file.hasPrefix("/") { return file }
        guard !draft.cwd.isEmpty else { return nil }
        return ((draft.cwd as NSString).expandingTildeInPath as NSString).appendingPathComponent(file)
    }

    // MARK: Actions on the selected job

    func runNow() {
        guard let job = selected else { return }
        store.runJob(job.id) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let run):
                self.show(run.result == "skipped" ? "Skipped: \(run.detail)" : "Started \(job.name).")
                self.select(job.id)
            case .failure(let failure):
                self.error = failure.localizedDescription
                self.select(job.id)
                self.error = failure.localizedDescription
            }
        }
    }

    func togglePause() {
        guard let job = selected else { return }
        store.setJobPaused(job.id, !job.paused)
        show(job.paused ? "Resumed \(job.name)." : "Paused \(job.name).")
    }

    func delete(confirmed: Bool = false) {
        guard let job = selected else { return }
        guard confirmed else {
            confirmingDelete = true
            return
        }
        confirmingDelete = false
        let index = jobs.firstIndex { $0.id == job.id } ?? 0
        store.deleteJob(job.id)
        select(jobs.isEmpty ? nil : jobs[min(index, jobs.count - 1)].id)
        show("Deleted \(job.name).")
    }

    private func show(_ message: String) {
        notice = message
        noticeTimer?.invalidate()
        noticeTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: false) { [weak self] _ in
            self?.notice = nil
        }
    }

    // MARK: Waking the Mac

    /// Waking the Mac for jobs; nil when the daemon can't.
    var wake: WakeState? { store.wake }
    /// A wake command is running (it may be waiting on the password dialog).
    private(set) var wakeBusy = false

    /// Runs the app's acmd with args, then done(succeeded, output). Tests swap
    /// it, so they never ask for a password or touch the Mac's wakes.
    @ObservationIgnored var runAcmd: (_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) -> Void = Acmd.run

    /// Lets acmd wake the Mac: macOS asks for an admin password once.
    func turnWakeOn() {
        wakeCommand(["schedule", "wake", "on"], done: "Waking the Mac is on.")
    }

    /// Clears acmd's wakes and removes its rule (asks for the password again).
    func turnWakeOff() {
        wakeCommand(["schedule", "wake", "off"], done: "Waking the Mac is off.")
    }

    /// Wakes for every job, or only those set to.
    func setWakeAll(_ all: Bool) {
        wakeCommand(["schedule", "wake", "on", "--all=\(all)"],
                    done: all ? "Every job wakes the Mac." : "Only jobs set to wake the Mac do.")
    }

    private func wakeCommand(_ args: [String], done message: String) {
        guard !wakeBusy else { return }
        wakeBusy = true
        error = nil
        runAcmd(args) { [weak self] ok, output in
            guard let self else { return }
            self.wakeBusy = false
            if ok {
                self.show(message)
            } else if !output.contains("canceled") {
                self.error = output.isEmpty ? "Couldn't change waking the Mac." : output
            }
        }
    }

    // MARK: Keys

    /// The view's keys. While the form is open only Esc and ⌘↩ are its own; the
    /// rest go to the fields.
    func handle(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        let command = flags.contains(.command)
        let key = event.charactersIgnoringModifiers ?? ""
        if confirmingDelete || confirmingDiscard {
            switch event.keyCode {
            case 53: // esc
                confirmingDelete = false
                confirmingDiscard = false
                return true
            case 36: // return
                if confirmingDelete { delete(confirmed: true) } else { cancel(force: true) }
                return true
            default: return true
            }
        }
        if isEditing {
            switch (event.keyCode, command) {
            case (53, _):
                cancel()
                return true
            case (36, true):
                if canSave { save() }
                return true
            default:
                if command && key == "s" {
                    if canSave { save() }
                    return true
                }
                return false
            }
        }
        if command { return false }
        switch event.keyCode {
        case 53: close()
        case 125: move(by: 1) // down
        case 126: move(by: -1) // up
        case 36: edit() // return
        case 51, 117: delete() // delete, forward delete
        default:
            switch key {
            case "j": move(by: 1)
            case "k": move(by: -1)
            case "n": newJob()
            case "e": edit()
            case "r": runNow()
            case "p": togglePause()
            case "d": duplicate()
            case "g":
                if let session = runs.first(where: { !$0.sessionID.isEmpty })?.sessionID {
                    goToSession(session)
                }
            default: return false
            }
        }
        return true
    }
}
