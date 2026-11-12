import Foundation

/// A scheduled job (internal/core.Job): what runs, when and where, and what's
/// happened with it.
struct Job: Codable, Identifiable, Equatable {
    var id = ""
    var name = ""
    var description = ""
    var groupID = ""
    var harness = ""
    /// As the daemon keeps it: the words when there are any, else the cron.
    var schedule = ""
    var prompt = ""
    var promptFile = ""
    var place = JobPlace.folder.rawValue
    var cwd = ""
    var sessionID = ""
    var settings: [String: [String]] = [:]
    var runMissed = false
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

    enum CodingKeys: String, CodingKey {
        case id, name, description, harness, schedule, prompt, cwd, settings, cron, once, words, summary, paused
        case groupID = "group_id", promptFile = "prompt_file", place = "where", sessionID = "session_id"
        case runMissed = "run_missed", createdAt = "created_at", nextRun = "next_run", lastRun = "last_run"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func text(_ key: CodingKeys) throws -> String { try c.decodeIfPresent(String.self, forKey: key) ?? "" }
        id = try text(.id)
        name = try text(.name)
        description = try text(.description)
        groupID = try text(.groupID)
        harness = try text(.harness)
        schedule = try text(.schedule)
        prompt = try text(.prompt)
        promptFile = try text(.promptFile)
        place = try text(.place)
        cwd = try text(.cwd)
        sessionID = try text(.sessionID)
        settings = try c.decodeIfPresent([String: [String]].self, forKey: .settings) ?? [:]
        runMissed = try c.decodeIfPresent(Bool.self, forKey: .runMissed) ?? false
        cron = try text(.cron)
        once = try text(.once)
        words = try text(.words)
        summary = try text(.summary)
        paused = try c.decodeIfPresent(Bool.self, forKey: .paused) ?? false
        createdAt = try text(.createdAt)
        nextRun = try text(.nextRun)
        lastRun = try c.decodeIfPresent(JobRun.self, forKey: .lastRun)
    }

    /// Only what you set: the daemon works out the rest.
    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if !id.isEmpty { try c.encode(id, forKey: .id) }
        try c.encode(name, forKey: .name)
        try c.encode(description, forKey: .description)
        try c.encode(groupID, forKey: .groupID)
        try c.encode(harness, forKey: .harness)
        try c.encode(schedule, forKey: .schedule)
        try c.encode(prompt, forKey: .prompt)
        try c.encode(promptFile, forKey: .promptFile)
        try c.encode(place, forKey: .place)
        try c.encode(cwd, forKey: .cwd)
        try c.encode(sessionID, forKey: .sessionID)
        try c.encode(settings, forKey: .settings)
        try c.encode(runMissed, forKey: .runMissed)
    }

    var next: Date? { JobTime.parse(nextRun) }

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
struct JobRun: Codable, Identifiable, Equatable {
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
struct JobCheck: Decodable, Equatable {
    var cron = ""
    var words = ""
    /// The schedule in English, also for cron with no words.
    var summary = ""
    var next: [String] = []
    var errors: [String: String] = [:]

    enum CodingKeys: String, CodingKey { case cron, words, summary, next, errors }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        cron = try c.decodeIfPresent(String.self, forKey: .cron) ?? ""
        words = try c.decodeIfPresent(String.self, forKey: .words) ?? ""
        summary = try c.decodeIfPresent(String.self, forKey: .summary) ?? words
        next = try c.decodeIfPresent([String].self, forKey: .next) ?? []
        errors = try c.decodeIfPresent([String: String].self, forKey: .errors) ?? [:]
    }

    var isValid: Bool { errors.isEmpty }
}

/// A setting an agent takes for a scheduled run (internal/harness.Setting).
struct AgentSetting: Decodable, Identifiable, Equatable {
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
enum JobPlace: String, CaseIterable, Identifiable {
    case folder, worktree, session
    var id: String { rawValue }

    var label: String {
        switch self {
        case .folder: "In a folder"
        case .worktree: "In a new git worktree"
        case .session: "In an existing session"
        }
    }

    var help: String {
        switch self {
        case .folder: "Each run is a new session in the folder, with its CLAUDE.md, settings and skills."
        case .worktree: "Each run gets its own worktree of the repo, so it can't touch your own work there."
        case .session: "Each run continues the session's conversation, so it remembers the runs before."
        }
    }
}

/// How a job stands.
enum JobState: Equatable {
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
    var name = ""
    var description = ""
    var groupID = ""
    /// Cron, or a one-off ("once on 2026-12-01 at 14:00"). ScheduleBuilder edits it.
    var schedule = "0 9 * * 1-5"
    /// Custom was chosen: show the cron as typed, even when the controls could show it.
    var customSchedule = false
    var promptSource = PromptSource.text
    var prompt = ""
    var promptFile = ""
    var place = JobPlace.folder
    var cwd = ""
    var sessionID = ""
    /// Each setting's text; a list's values are one a line.
    var settings: [String: String] = [:]
    var runMissed = false

    init(folder: String = "") {
        cwd = folder
    }

    init(_ job: Job) {
        id = job.id
        name = job.name
        description = job.description
        groupID = job.groupID
        if !job.cron.isEmpty {
            schedule = job.cron
        } else if let once = JobTime.parse(job.once) {
            var b = ScheduleBuilder()
            b.kind = .once
            b.once = once
            schedule = b.schedule()
        } else {
            schedule = job.schedule
        }
        promptSource = job.promptFile.isEmpty ? .text : .file
        prompt = job.prompt
        promptFile = job.promptFile
        place = JobPlace(rawValue: job.place) ?? .folder
        cwd = job.cwd
        sessionID = job.sessionID
        settings = job.settings.mapValues { $0.joined(separator: "\n") }
        runMissed = job.runMissed
    }

    var isNew: Bool { id.isEmpty }

    /// The schedule as the form's controls show it.
    var builder: ScheduleBuilder {
        get {
            var b = ScheduleBuilder(schedule: schedule)
            if customSchedule, b.kind != .custom {
                b.cron = schedule
                b.kind = .custom
            }
            return b
        }
        set {
            customSchedule = newValue.kind == .custom
            schedule = newValue.schedule()
        }
    }

    /// The job to check or save.
    var job: Job {
        var j = Job()
        j.id = id
        j.name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        j.description = description.trimmingCharacters(in: .whitespacesAndNewlines)
        j.groupID = groupID
        j.schedule = schedule
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
        return j
    }

    /// A new job like this one.
    func duplicate() -> JobDraft {
        var copy = self
        copy.id = ""
        copy.name = name + " copy"
        return copy
    }
}

/// Times as the daemon sends them (RFC 3339), and as the list shows them.
enum JobTime {
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
