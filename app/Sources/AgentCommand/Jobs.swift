import Foundation

/// A scheduled job (internal/core.Job): what runs, when and where, and what's
/// happened with it.
struct Job: Codable, Identifiable, Equatable {
    var id = ""
    /// What the form loaded, sent back with a save: the daemon refuses the
    /// save ("conflict") when the job changed since.
    var version = ""
    var name = ""
    var description = ""
    var groupID = ""
    /// The harness it runs in ("" is the default harness).
    var harnessID = ""
    /// As the daemon keeps it: the words when there are any, else the cron.
    var schedule = ""
    var prompt = ""
    var promptFile = ""
    var place = JobPlace.folder.rawValue
    var cwd = ""
    var sessionID = ""
    var settings: [String: [String]] = [:]
    var runMissed = false
    /// Wakes the Mac shortly before each run, once acmd may.
    var wake = false
    // From the daemon.
    var cron = ""
    var once = ""
    var words = ""
    /// The schedule in English, also for cron with no words.
    var summary = ""
    var paused = false
    var createdAt = ""
    var nextRun = ""
    var lastRun: JobRun?
    /// The schedule as the form's controls show it, from the daemon; sent back
    /// with a check or a save in place of cron.
    var form: ScheduleForm?

    enum CodingKeys: String, CodingKey {
        case harnessID = "harness_id"
        case id, version, name, description, schedule, prompt, cwd, settings, cron, once, words, summary, paused, wake, form
        case groupID = "group_id", promptFile = "prompt_file", place = "where", sessionID = "session_id"
        case runMissed = "run_missed", createdAt = "created_at", nextRun = "next_run", lastRun = "last_run"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func text(_ key: CodingKeys) throws -> String { try c.decodeIfPresent(String.self, forKey: key) ?? "" }
        id = try text(.id)
        version = try text(.version)
        name = try text(.name)
        description = try text(.description)
        groupID = try text(.groupID)
        harnessID = try text(.harnessID)
        schedule = try text(.schedule)
        prompt = try text(.prompt)
        promptFile = try text(.promptFile)
        place = try text(.place)
        cwd = try text(.cwd)
        sessionID = try text(.sessionID)
        settings = try c.decodeIfPresent([String: [String]].self, forKey: .settings) ?? [:]
        runMissed = try c.decodeIfPresent(Bool.self, forKey: .runMissed) ?? false
        wake = try c.decodeIfPresent(Bool.self, forKey: .wake) ?? false
        cron = try text(.cron)
        once = try text(.once)
        words = try text(.words)
        summary = try text(.summary)
        paused = try c.decodeIfPresent(Bool.self, forKey: .paused) ?? false
        createdAt = try text(.createdAt)
        nextRun = try text(.nextRun)
        lastRun = try c.decodeIfPresent(JobRun.self, forKey: .lastRun)
        form = try? c.decodeIfPresent(ScheduleForm.self, forKey: .form)
    }

    /// Only what you set: the daemon works out the rest.
    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if !id.isEmpty { try c.encode(id, forKey: .id) }
        if !version.isEmpty { try c.encode(version, forKey: .version) }
        try c.encode(name, forKey: .name)
        try c.encode(description, forKey: .description)
        try c.encode(groupID, forKey: .groupID)
        try c.encode(harnessID, forKey: .harnessID)
        try c.encode(schedule, forKey: .schedule)
        try c.encode(prompt, forKey: .prompt)
        try c.encode(promptFile, forKey: .promptFile)
        try c.encode(place, forKey: .place)
        try c.encode(cwd, forKey: .cwd)
        try c.encode(sessionID, forKey: .sessionID)
        try c.encode(settings, forKey: .settings)
        try c.encode(runMissed, forKey: .runMissed)
        try c.encode(wake, forKey: .wake)
        try c.encodeIfPresent(form, forKey: .form)
    }

    var next: Date? { JobTime.parse(nextRun) }

    /// The harness it runs in: its own, the default for none, or its kind's
    /// placeholder when it's gone.
    var harness: Harness {
        if harnessID.isEmpty { return HarnessList.shared.defaultHarness ?? .claude }
        return Harness.resolve(id: harnessID, kind: "claude")
    }

    /// How it stands, for the list.
    var state: JobState {
        if paused { return .paused }
        if lastRun?.result == "failed" { return .failing }
        if next == nil { return lastRun == nil ? .never : .finished }
        return .scheduled
    }

    /// The schedule in English, with the cron beside it.
    var scheduleLine: String {
        let english = summary.isEmpty ? words : summary
        switch (english.isEmpty, cron.isEmpty) {
        case (false, false): return "\(english) · \(cron)"
        case (false, true): return english
        case (true, false): return cron
        default: return schedule
        }
    }

    /// Where it runs, short enough for the list: the folder's name.
    func placeName(sessionName: (String) -> String?) -> String {
        let folder = cwd.isEmpty ? "no folder" : (cwd as NSString).lastPathComponent
        switch JobPlace(rawValue: place) {
        case .worktree: return "worktree of \(folder)"
        case .session: return sessionName(sessionID) ?? "a session that's gone"
        default: return folder
        }
    }

    /// Where it runs, in a few words.
    func placeLine(sessionName: (String) -> String?) -> String {
        let folder = JobTime.tilde(cwd)
        switch JobPlace(rawValue: place) {
        case .worktree: return "a new worktree of \(folder)"
        case .session: return "continues \(sessionName(sessionID) ?? "a session that's gone")"
        default: return folder
        }
    }
}

/// One time a job was due (internal/core.JobRun).
extension Session {
    /// The scheduled job behind it, for the clock beside its name: the job that
    /// started it, or one that continues it on a schedule. nil for neither.
    func scheduledNote(jobs: [Job]) -> String? {
        if let jobID, !jobID.isEmpty {
            guard let job = jobs.first(where: { $0.id == jobID }) else { return "Started by a scheduled job" }
            return "Started by the scheduled job \u{201C}\(job.name)\u{201D}"
        }
        guard !isOnHost else { return nil } // a host's sessions aren't this Mac's jobs'
        let names = jobs.filter { JobPlace(rawValue: $0.place) == .session && $0.sessionID == id }.map { "\u{201C}\($0.name)\u{201D}" }
        guard !names.isEmpty else { return nil }
        return names.count == 1 ? "Continued by the scheduled job \(names[0])" : "Continued by the scheduled jobs \(names.joined(separator: ", "))"
    }
}

nonisolated struct JobRun: Codable, Identifiable, Equatable {
    let id: Int64
    let jobID: String
    let due: String
    let at: String
    let result: String
    let sessionID: String
    let detail: String

    enum CodingKeys: String, CodingKey {
        case id, due, at, result, detail
        case jobID = "job_id", sessionID = "session_id"
    }

    init(id: Int64, jobID: String, due: String, at: String, result: String, sessionID: String = "", detail: String = "") {
        (self.id, self.jobID, self.due, self.at, self.result, self.sessionID, self.detail) =
            (id, jobID, due, at, result, sessionID, detail)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        jobID = try c.decodeIfPresent(String.self, forKey: .jobID) ?? ""
        due = try c.decodeIfPresent(String.self, forKey: .due) ?? ""
        at = try c.decodeIfPresent(String.self, forKey: .at) ?? ""
        result = try c.decodeIfPresent(String.self, forKey: .result) ?? ""
        sessionID = try c.decodeIfPresent(String.self, forKey: .sessionID) ?? ""
        detail = try c.decodeIfPresent(String.self, forKey: .detail) ?? ""
    }

    var resultLabel: String {
        switch result {
        case "started": "Ran"
        case "skipped": "Skipped"
        case "missed": "Missed"
        case "failed": "Failed"
        default: result.capitalized
        }
    }
}

/// What the daemon makes of a job before it's saved (internal/ipc.JobCheck).
nonisolated struct JobCheck: Decodable, Equatable {
    /// The schedule as the daemon read it, for the form's controls.
    var form: ScheduleForm?
    var cron = ""
    var words = ""
    /// The schedule in English, also for cron with no words.
    var summary = ""
    var next: [String] = []
    var errors: [String: String] = [:]

    enum CodingKeys: String, CodingKey { case form, cron, words, summary, next, errors }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        form = try? c.decodeIfPresent(ScheduleForm.self, forKey: .form)
        cron = try c.decodeIfPresent(String.self, forKey: .cron) ?? ""
        words = try c.decodeIfPresent(String.self, forKey: .words) ?? ""
        summary = try c.decodeIfPresent(String.self, forKey: .summary) ?? words
        next = try c.decodeIfPresent([String].self, forKey: .next) ?? []
        errors = try c.decodeIfPresent([String: String].self, forKey: .errors) ?? [:]
    }

    var isValid: Bool { errors.isEmpty }
}

/// A setting an agent takes for a scheduled run (internal/harness.Setting).
nonisolated struct AgentSetting: Decodable, Identifiable, Equatable {
    let key: String
    let label: String
    let help: String
    let kind: String
    let choices: [String]

    var id: String { key }
    /// Several values, one a line.
    var isList: Bool { kind == "list" || kind == "paths" }

    enum CodingKeys: String, CodingKey { case key, label, help, kind, choices }

    init(key: String, label: String, help: String = "", kind: String, choices: [String] = []) {
        (self.key, self.label, self.help, self.kind, self.choices) = (key, label, help, kind, choices)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? key
        help = try c.decodeIfPresent(String.self, forKey: .help) ?? ""
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "text"
        choices = try c.decodeIfPresent([String].self, forKey: .choices) ?? []
    }
}

/// Where a job runs (internal/core's Where).
nonisolated enum JobPlace: String, CaseIterable, Identifiable {
    case folder, worktree, session
    var id: String { rawValue }

    var label: String {
        switch self {
        case .folder: "In a folder"
        case .worktree: "In a new git worktree"
        case .session: "In an existing session"
        }
    }

    /// What it means for a job of this agent's.
    func help(_ harness: Harness) -> String {
        switch self {
        case .folder where harness.isClaude: "Each run is a new session in the folder, with its CLAUDE.md, settings and skills."
        case .folder: "Each run is a new \(harness.title) session in the folder."
        case .worktree: "Each run gets its own worktree of the repo, so it can't touch your own work there."
        case .session: "Each run continues the session's conversation, so it remembers the runs before."
        }
    }
}

/// How a job stands.
nonisolated enum JobState: Equatable {
    case scheduled, paused, failing, finished, never

    var label: String {
        switch self {
        case .scheduled: "Scheduled"
        case .paused: "Paused"
        case .failing: "Failing"
        case .finished: "Done"
        case .never: "Won't run"
        }
    }
}

/// What the form edits: a job as you set it, with lists one value a line.
struct JobDraft: Equatable {
    enum PromptSource: String, CaseIterable, Identifiable {
        case text, file
        var id: String { rawValue }
        var label: String { self == .text ? "Write it here" : "Markdown file" }
    }

    var id = ""
    /// The job's version when the form loaded it (Job.version).
    var version = ""
    var name = ""
    var description = ""
    var groupID = ""
    /// The harness it runs in. Set it with setHarness, which drops what was another kind's.
    var harness = HarnessList.shared.defaultHarness ?? Harness.claude
    /// The schedule as the controls set it. The daemon turns it into cron.
    var builder = ScheduleBuilder()
    var promptSource = PromptSource.text
    var prompt = ""
    var promptFile = ""
    var place = JobPlace.folder
    var cwd = ""
    var sessionID = ""
    /// Each setting's text; a list's values are one a line.
    var settings: [String: String] = [:]
    var runMissed = false
    var wake = false

    init(folder: String = "") {
        cwd = folder
    }

    init(_ job: Job) {
        id = job.id
        version = job.version
        name = job.name
        description = job.description
        groupID = job.groupID
        harness = job.harness
        builder = job.form.map { ScheduleBuilder(form: $0) } ?? .custom(job.cron.isEmpty ? job.schedule : job.cron)
        builder.pinned = builder.kind == .custom
        promptSource = job.promptFile.isEmpty ? .text : .file
        prompt = job.prompt
        promptFile = job.promptFile
        place = JobPlace(rawValue: job.place) ?? .folder
        cwd = job.cwd
        sessionID = job.sessionID
        settings = job.settings.mapValues { $0.joined(separator: "\n") }
        runMissed = job.runMissed
        wake = job.wake
    }

    var isNew: Bool { id.isEmpty }

    /// What's different in other, by the form's names for it.
    func changes(to other: JobDraft) -> [String] {
        var out: [String] = []
        if name != other.name { out.append("name") }
        if description != other.description { out.append("description") }
        if groupID != other.groupID { out.append("group") }
        if harness != other.harness { out.append("harness") }
        if builder.form() != other.builder.form() || builder.cron != other.builder.cron { out.append("schedule") }
        if prompt != other.prompt || promptFile != other.promptFile || promptSource != other.promptSource { out.append("prompt") }
        if place != other.place || cwd != other.cwd || sessionID != other.sessionID { out.append("where it runs") }
        if settings != other.settings { out.append("settings") }
        if runMissed != other.runMissed || wake != other.wake { out.append("options") }
        return out
    }

    /// Changes the harness. Another kind's settings and session don't fit, so they go.
    mutating func setHarness(_ other: Harness) {
        guard other != harness else { return }
        if other.kind != harness.kind {
            settings = [:]
            sessionID = ""
        }
        harness = other
    }

    /// Cron typed into the cron field: Custom until the daemon reads it, when the
    /// controls follow (takeForm), unless Custom was chosen.
    mutating func typeCron(_ cron: String) {
        if builder.kind == .custom { builder.cron = cron } else { builder = .custom(cron) }
    }

    /// Takes the daemon's reading of the schedule: typed cron the controls can show
    /// moves them there.
    mutating func takeForm(_ form: ScheduleForm) {
        guard builder.kind == .custom, !builder.pinned, form.kind != "custom" else { return }
        builder = ScheduleBuilder(form: form)
    }

    /// The job to check or save.
    var job: Job {
        var j = Job()
        j.id = id
        j.version = version
        j.name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        j.description = description.trimmingCharacters(in: .whitespacesAndNewlines)
        j.groupID = groupID
        j.harnessID = harness.harnessID
        j.schedule = builder.kind == .custom ? builder.cron : ""
        j.form = builder.form()
        j.prompt = promptSource == .text ? prompt : ""
        j.promptFile = promptSource == .file ? promptFile : ""
        j.place = place.rawValue
        j.cwd = place == .session ? "" : cwd
        j.sessionID = place == .session ? sessionID : ""
        j.settings = settings.compactMapValues { text in
            let values = text.split(whereSeparator: \.isNewline)
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
            return values.isEmpty ? nil : values
        }
        j.runMissed = runMissed
        j.wake = wake
        return j
    }

    /// A new job like this one.
    func duplicate() -> JobDraft {
        var copy = self
        copy.id = ""
        copy.version = ""
        copy.name = name + " copy"
        return copy
    }
}

/// Times as the daemon sends them (RFC 3339), and as the list shows them.
nonisolated enum JobTime {
    static func parse(_ text: String) -> Date? {
        guard !text.isEmpty else { return nil }
        let precise = ISO8601DateFormatter()
        precise.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return precise.date(from: text) ?? ISO8601DateFormatter().date(from: text)
    }

    /// "today 08:00", "tomorrow 08:00", "Fri 08:00" within the week, else "20 Nov 08:00"
    /// (with the year when it isn't this one).
    static func describe(_ date: Date, now: Date = Date(), calendar: Calendar = .current) -> String {
        let time = format("HH:mm", date, calendar)
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now), to: calendar.startOfDay(for: date)).day ?? 0
        switch days {
        case 0: return "today \(time)"
        case 1: return "tomorrow \(time)"
        case -1: return "yesterday \(time)"
        case 2...6: return "\(format("EEE", date, calendar)) \(time)"
        default:
            let sameYear = calendar.component(.year, from: date) == calendar.component(.year, from: now)
            return format(sameYear ? "d MMM" : "d MMM yyyy", date, calendar) + " \(time)"
        }
    }

    /// describe for a time from the daemon; "" when there's none.
    static func describe(_ text: String, now: Date = Date(), calendar: Calendar = .current) -> String {
        parse(text).map { describe($0, now: now, calendar: calendar) } ?? ""
    }

    private static func format(_ pattern: String, _ date: Date, _ calendar: Calendar) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        f.dateFormat = pattern
        return f.string(from: date)
    }

    static func tilde(_ path: String) -> String {
        path.isEmpty ? "no folder" : (path as NSString).abbreviatingWithTildeInPath
    }
}

// MARK: Waking the Mac

extension WakeState {
    /// When the Mac next wakes, or why it doesn't.
    var nextLine: String {
        if let next = JobTime.parse(self.next) { return "Next wake: \(JobTime.describe(next))." }
        return all ? "No run to wake for yet." : "Wakes only for jobs set to wake the Mac."
    }
}

extension Job {
    /// Whether this job wakes the Mac, given the setting.
    func wakeLine(_ state: WakeState?) -> String {
        guard let state, state.allowed else { return wake ? "Once waking the Mac is turned on" : "No" }
        if state.all { return "Yes, as every job does" }
        return wake ? "Yes, shortly before each run" : "No"
    }
}

