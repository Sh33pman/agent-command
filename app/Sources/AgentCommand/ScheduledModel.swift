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
        didSet {
            if draft != oldValue, draft != nil { scheduleCheck() }
            if let agent = draft?.harness, agent != oldValue?.harness { loadSettings(agent) }
        }
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
    /// Each agent's settings for scheduled runs, as the daemon lists them.
    private(set) var agentSettings: [Harness: [AgentSetting]] = [:]
    @ObservationIgnored private var asked: Set<Harness> = []
    /// Asking whether to delete the selected job.
    var confirmingDelete = false
    /// Asking whether to throw away the form's changes.
    var confirmingDiscard = false
    /// Save was pressed: show every field's problem, not only the ones you've changed.
    private(set) var triedSaving = false

    @ObservationIgnored var close: () -> Void = {}
    @ObservationIgnored var goToSession: (String) -> Void = { _ in }
    /// Picks a folder or a file; nil when cancelled. Tests replace it.
    /// The folder or prompt file being chosen, with the daemon's folders: the
    /// same chooser on this Mac as for a host.
    var chooser: RemoteFolderChooser?
    private(set) var chooserTitle = ""
    /// The draft's prompt file as the daemon read it; nil until it's read.
    private(set) var promptFile: PromptFile?
    /// The daemon's folder and prompt-file ops; tests swap them.
    @ObservationIgnored var folders = RemoteHostsClient()
    @ObservationIgnored var calls: DaemonCalls = DaemonSocketCalls()
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

    /// The agent the form or the selected job is for.
    var harness: Harness { draft?.harness ?? selected?.harnessKind ?? .claude }

    /// The settings the form shows: those of the draft's agent (or the selected job's).
    var settings: [AgentSetting] { settings(for: harness) }

    func settings(for agent: Harness) -> [AgentSetting] { agentSettings[agent] ?? [] }

    /// The sessions a job of the form's agent can continue.
    var continuableSessions: [Session] { continuableSessions(for: harness) }

    /// The sessions a job of an agent's can continue: the daemon says which can,
    /// and the agent in them is the job's.
    func continuableSessions(for agent: Harness) -> [Session] {
        store.sessions.filter { $0.can(.continueInJob) && $0.runningAgent?.rawValue == agent.rawValue }
    }

    func sessionName(_ id: String) -> String? {
        (store.sessions + store.archived).first { $0.id == id }?.displayName
    }

    func groupName(_ id: String) -> String {
        store.groups.first { $0.id == id }?.name ?? "Default"
    }

    // MARK: Lifecycle

    func start(editing job: Job? = nil) {
        loadSettings(.claude)
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

    /// Asks the daemon for an agent's settings, once.
    private func loadSettings(_ agent: Harness) {
        guard !asked.contains(agent) else { return }
        asked.insert(agent)
        store.loadAgentSettings(harness: agent == .claude ? "" : agent.rawValue) { [weak self] in
            self?.agentSettings[agent] = $0
        }
    }

    // MARK: The list

    func select(_ id: String?) {
        guard !isEditing else { return }
        selectedID = id
        confirmingDelete = false
        error = nil
        runs = []
        guard let id else { return }
        if let job = selected { loadSettings(job.harnessKind) }
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
                if let form = check?.form { self.draft?.takeForm(form) } // typed cron moves the controls
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
        case "schedule": draft.builder != original.builder
        case "prompt": draft.prompt != original.prompt || draft.promptFile != original.promptFile
        case "cwd": draft.cwd != original.cwd
        case "session": draft.sessionID != original.sessionID
        case "harness": draft.harness != original.harness
        default: true
        }
        return changed ? message : nil
    }

    func chooseFolder() {
        choose("Choose the job's folder", file: false) { [weak self] in self?.draft?.cwd = $0 }
    }

    func choosePromptFile() {
        choose("Choose the prompt file: Markdown or text", file: true) { [weak self] path in
            self?.draft?.promptFile = path
            self?.draft?.promptSource = .file
        }
    }

    /// Chooses an agent setting's file (a settings or MCP config file).
    func chooseSettingFile(then: @escaping (String) -> Void) {
        choose("Choose the file", file: true, exts: [".json"], then: then)
    }

    /// Adds a folder or file to a setting that takes several (Extra folders, MCP config).
    func addPath(to key: String, directory: Bool) {
        choose(directory ? "Add a folder" : "Add a file", file: !directory, exts: directory ? nil : [".json"]) { [weak self] path in
            let current = self?.draft?.settings[key] ?? ""
            self?.draft?.settings[key] = current.isEmpty ? path : current + "\n" + path
        }
    }

    private func choose(_ title: String, file: Bool, exts: [String]? = nil, then: @escaping (String) -> Void) {
        let c = RemoteFolderChooser(host: nil, picksFile: file, fileExts: exts, client: folders)
        c.choose = { [weak self] path in
            then(path)
            self?.chooser = nil
        }
        chooserTitle = title
        chooser = c
    }

    func closeChooser() { chooser = nil }

    // MARK: Prompt files

    /// What names the draft's prompt file to the daemon: the saved job's own,
    /// while it's the one the job has, or the draft's folder and path.
    var promptFileRef: PromptFileRef? {
        guard let draft, !draft.promptFile.isEmpty else { return nil }
        if !draft.isNew, let original, original.promptFile == draft.promptFile, original.cwd == draft.cwd {
            return PromptFileRef(job_id: draft.id)
        }
        return PromptFileRef(cwd: draft.cwd, path: draft.promptFile)
    }

    /// Reads the draft's prompt file through the daemon, to edit it in place.
    func loadPromptFile() {
        guard let ref = promptFileRef else { return promptFile = nil }
        call("job.prompt.read", ref) { [weak self] (result: Result<PromptFile, Error>) in
            switch result {
            case .success(let file): self?.promptFile = file
            case .failure: self?.promptFile = nil
            }
        }
    }

    /// Writes the editor's text back to the prompt file, through the daemon.
    func savePromptFile(_ text: String) {
        guard var ref = promptFileRef else { return }
        ref.text = text
        call("job.prompt.write", ref) { [weak self] (result: Result<PromptFile, Error>) in
            guard let self else { return }
            switch result {
            case .success(let file):
                self.promptFile = file
                self.scheduleCheck()
            case .failure(let failure):
                self.error = "Couldn't save the prompt file: \(errorText(failure))"
            }
        }
    }

    private func call<Out: Decodable>(_ op: String, _ input: some Encodable, _ done: @escaping (Result<Out, Error>) -> Void) {
        let data = (try? JSONEncoder().encode(input)) ?? Data("{}".utf8)
        calls.call(op, data) { result in
            done(result.flatMap { reply in Result { try JSONDecoder().decode(Out.self, from: reply) } })
        }
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

    /// The view's keys, from the keymap. While the form is open only leaving and saving
    /// are its own; the rest go to the fields.
    func handle(_ event: NSEvent) -> Bool {
        let keys = Keymap.shared
        let combo = KeyCombo(event: event)
        if confirmingDelete || confirmingDiscard {
            switch keys.action(for: combo, among: [.back, .leave, .open]) {
            case .back, .leave:
                confirmingDelete = false
                confirmingDiscard = false
            case .open:
                if confirmingDelete { delete(confirmed: true) } else { cancel(force: true) }
            default: break
            }
            return true
        }
        // There's no pane to split or move to, sidebar to go to, or session to remove, behind Scheduled.
        let covered = KeyAction.paneActions + [.focusSidebar, .removeAdded]
        if isEditing {
            switch keys.action(for: combo, among: [.save, .leave, .closePane] + covered) {
            case .save:
                if canSave { save() }
            case .leave, .closePane:
                cancel()
            case nil:
                return false
            default: break
            }
            return true
        }
        let actions: [KeyAction] = [
            .closePane, .rename, .archive, .duplicateJob, .up, .down, .open, .back, .go, .remove,
            .newJob, .editJob, .runJob, .pauseJob,
        ]
        switch keys.action(for: combo, among: actions + covered) {
        case .closePane, .back: close()
        case .down: move(by: 1)
        case .up: move(by: -1)
        // ⌘R renames and ⇧⌘⌫ archives a session: here, the same keys edit and delete the job.
        case .open, .editJob, .rename: edit()
        case .remove, .archive: delete()
        case .newJob: newJob()
        case .runJob: runNow()
        case .pauseJob: togglePause()
        case .duplicateJob: duplicate()
        case .go:
            if let session = runs.first(where: { !$0.sessionID.isEmpty })?.sessionID {
                goToSession(session)
            }
        case nil:
            return false
        default: break
        }
        return true
    }
}

/// Names a prompt file to the daemon: a saved job's own by id, or a draft's
/// folder and path (internal/ipc.PromptFileRef).
struct PromptFileRef: Encodable, Equatable {
    var job_id: String?
    var cwd: String?
    var path: String?
    var text: String?
}

/// A prompt file as the daemon read or wrote it.
struct PromptFile: Decodable, Equatable {
    var path = ""
    var text = ""
    var exists = false
}
