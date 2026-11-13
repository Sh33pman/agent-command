import Foundation
import Testing

@testable import AgentCommand

private func decodeJob(_ fields: [String: Any]) throws -> Job {
    try JSONDecoder().decode(Job.self, from: JSONSerialization.data(withJSONObject: fields))
}

@Test func aJobReadsWhatTheDaemonSends() throws {
    let job = try decodeJob([
        "id": "J1", "name": "Morning", "schedule": "weekdays at 08:00", "prompt": "# Hi", "where": "worktree",
        "cwd": "/w/repo", "group_id": "G", "settings": ["model": ["opus"]], "run_missed": true,
        "cron": "0 8 * * 1-5", "words": "weekdays at 08:00", "next_run": "2026-11-13T08:00:00Z",
        "last_run": ["id": 3, "job_id": "J1", "due": "2026-11-12T08:00:00Z", "at": "2026-11-12T08:00:01.5Z",
                     "result": "started", "session_id": "S"],
    ])
    #expect(job.name == "Morning" && job.place == "worktree" && job.groupID == "G" && job.runMissed)
    #expect(job.settings == ["model": ["opus"]])
    #expect(job.lastRun?.sessionID == "S" && job.lastRun?.resultLabel == "Ran")
    #expect(job.next == JobTime.parse("2026-11-13T08:00:00Z"))
    #expect(job.state == .scheduled)
    #expect(job.scheduleLine == "weekdays at 08:00 · 0 8 * * 1-5")
    #expect(job.placeLine { _ in nil }.hasPrefix("a new worktree of "))

    let bare = try decodeJob(["id": "J2"])
    #expect(bare.name.isEmpty && bare.settings.isEmpty && bare.lastRun == nil && bare.state == .never)
}

@Test func aJobSendsOnlyWhatYouSet() throws {
    var job = Job()
    job.name = "n"
    job.cron = "set by the daemon"
    job.paused = true
    let sent = try JSONSerialization.jsonObject(with: JSONEncoder().encode(job)) as? [String: Any]
    #expect(sent?["id"] == nil, "a new job has no id")
    #expect(sent?["cron"] == nil && sent?["paused"] == nil)
    #expect(sent?["name"] as? String == "n" && sent?["where"] as? String == "folder")
    job.id = "J"
    let edited = try JSONSerialization.jsonObject(with: JSONEncoder().encode(job)) as? [String: Any]
    #expect(edited?["id"] as? String == "J")
}

@Test func aJobsStateAndLines() throws {
    var job = Job()
    job.nextRun = "2026-11-13T08:00:00Z"
    job.words = "every day at 08:00"
    #expect(job.state == .scheduled && job.scheduleLine == "every day at 08:00")
    job.paused = true
    #expect(job.state == .paused && job.state.label == "Paused")
    job.paused = false
    job.lastRun = JobRun(id: 1, jobID: "J", due: "", at: "", result: "failed", detail: "gone")
    #expect(job.state == .failing && job.lastRun?.resultLabel == "Failed")
    job.lastRun = JobRun(id: 1, jobID: "J", due: "", at: "", result: "started")
    job.nextRun = ""
    #expect(job.state == .finished, "a one-off that ran")
    job.words = ""
    job.cron = "*/7 * * * *"
    #expect(job.scheduleLine == "*/7 * * * *")
    job.cron = ""
    job.schedule = "typed"
    #expect(job.scheduleLine == "typed")

    job.place = "session"
    job.sessionID = "S"
    #expect(job.placeLine { $0 == "S" ? "standup" : nil } == "continues standup")
    #expect(job.placeLine { _ in nil } == "continues a session that's gone")
    job.place = "folder"
    job.cwd = ""
    #expect(job.placeLine { _ in nil } == "no folder")
    for (result, label) in [("skipped", "Skipped"), ("missed", "Missed"), ("odd", "Odd")] {
        #expect(JobRun(id: 1, jobID: "", due: "", at: "", result: result).resultLabel == label)
    }
    #expect(JobState.finished.label == "Done" && JobState.never.label == "Won't run" && JobState.failing.label == "Failing")
}

@Test func aDraftRoundTripsAJob() throws {
    var job = try decodeJob([
        "id": "J", "name": "n", "description": "d", "group_id": "G", "schedule": "every hour", "prompt": "p",
        "where": "folder", "cwd": "/w", "settings": ["add-dir": ["/a", "/b"], "model": ["opus"]], "run_missed": true,
    ])
    let draft = JobDraft(job)
    #expect(draft.settings["add-dir"] == "/a\n/b" && draft.promptSource == .text && !draft.isNew)
    let back = draft.job
    #expect(back.settings == job.settings && back.prompt == "p" && back.cwd == "/w" && back.runMissed)

    // Only the chosen prompt and place go; blank settings and lines don't.
    var edited = draft
    edited.promptSource = .file
    edited.promptFile = "brief.md"
    edited.place = .session
    edited.sessionID = "S"
    edited.settings["model"] = "  "
    edited.settings["add-dir"] = "/a\n\n  /c  \n"
    edited.name = "  spaced  "
    let sent = edited.job
    #expect(sent.prompt.isEmpty && sent.promptFile == "brief.md")
    #expect(sent.cwd.isEmpty && sent.sessionID == "S" && sent.place == "session")
    #expect(sent.settings == ["add-dir": ["/a", "/c"]])
    #expect(sent.name == "spaced")

    job.promptFile = "f.md"
    #expect(JobDraft(job).promptSource == .file)
    job.place = "nonsense"
    #expect(JobDraft(job).place == .folder)

    let copy = draft.duplicate()
    #expect(copy.isNew && copy.name == "n copy" && copy.prompt == draft.prompt)
    #expect(JobDraft(folder: "/w").cwd == "/w" && JobDraft().isNew)
}

@Test func timesReadAsTheListShowsThem() throws {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(identifier: "UTC")!
    let now = try #require(JobTime.parse("2026-11-12T10:00:00Z"))
    func show(_ text: String) -> String { JobTime.describe(text, now: now, calendar: calendar) }
    #expect(show("2026-11-12T08:00:00Z") == "today 08:00")
    #expect(show("2026-11-13T08:00:00.123456Z") == "tomorrow 08:00")
    #expect(show("2026-11-11T23:30:00Z") == "yesterday 23:30")
    #expect(show("2026-11-16T09:30:00Z") == "Mon 09:30")
    #expect(show("2026-11-25T09:30:00Z") == "25 Nov 09:30")
    #expect(show("2027-01-02T09:30:00Z") == "2 Jan 2027 09:30")
    #expect(show("") == "" && show("not a time") == "")
    #expect(JobTime.tilde("") == "no folder")
    #expect(JobTime.tilde(NSHomeDirectory() + "/x") == "~/x")
}

@Test func checksAndSettingsDecode() throws {
    let check = try JSONDecoder().decode(JobCheck.self, from: Data(#"{"cron":"0 8 * * *","words":"every day at 08:00","next":["2026-11-13T08:00:00Z"],"errors":{"name":"Give the job a name."}}"#.utf8))
    #expect(check.cron == "0 8 * * *" && check.next.count == 1 && !check.isValid)
    #expect(try JSONDecoder().decode(JobCheck.self, from: Data("{}".utf8)).isValid)
    let settings = try JSONDecoder().decode([AgentSetting].self, from: Data(#"[{"key":"effort","label":"Effort","kind":"choice","choices":["low","high"],"flag":"--effort"},{"key":"add-dir","kind":"paths"}]"#.utf8))
    #expect(settings[0].choices == ["low", "high"] && !settings[0].isList)
    #expect(settings[1].label == "add-dir" && settings[1].isList && settings[1].help.isEmpty)
    #expect(JobPlace.allCases.allSatisfy { !$0.label.isEmpty && !$0.help.isEmpty })
    #expect(JobDraft.PromptSource.file.label == "Markdown file")
}

@Test func aGroupFromTheDaemon() throws {
    let groups = try JSONDecoder().decode([SessionGroup].self, from: Data(#"[{"ID":"default","Name":"Default","IsDefault":true,"ArchivedAt":"0001-01-01T00:00:00Z"},{"ID":"G","Name":"Old","ArchivedAt":"2026-11-01T00:00:00Z"}]"#.utf8))
    #expect(groups[0].isDefault && !groups[0].isArchived)
    #expect(groups[1].isArchived)
    #expect(!SessionGroup(id: "x", name: "x").isArchived)
}

@Test func aJobWakesTheMacWhenItSaysSo() throws {
    var job = try decodeJob(["id": "J", "wake": true])
    #expect(job.wake && JobDraft(job).wake)
    var draft = JobDraft(job)
    draft.wake = false
    #expect(!draft.job.wake)
    let sent = try JSONSerialization.jsonObject(with: JSONEncoder().encode(job)) as? [String: Any]
    #expect(sent?["wake"] as? Bool == true)
    #expect(try !decodeJob(["id": "J"]).wake)

    // What the detail says, by the setting.
    #expect(job.wakeLine(nil) == "Once waking the Mac is turned on")
    #expect(job.wakeLine(WakeState()) == "Once waking the Mac is turned on")
    #expect(job.wakeLine(WakeState(allowed: true)) == "Yes, shortly before each run")
    #expect(job.wakeLine(WakeState(allowed: true, all: true)) == "Yes, as every job does")
    job.wake = false
    #expect(job.wakeLine(nil) == "No" && job.wakeLine(WakeState(allowed: true)) == "No")
}

@Test func theWakeStateReadsWhatTheDaemonSends() throws {
    let data = Data(#"{"allowed":true,"all":true,"next":"2026-11-14T07:58:00Z","error":"no"}"#.utf8)
    let state = try JSONDecoder().decode(WakeState.self, from: data)
    #expect(state == WakeState(allowed: true, all: true, next: "2026-11-14T07:58:00Z", error: "no"))
    #expect(state.nextLine.hasPrefix("Next wake: "))
    #expect(try JSONDecoder().decode(WakeState.self, from: Data("{}".utf8)) == WakeState())
    #expect(WakeState(allowed: true, all: true).nextLine == "No run to wake for yet.")
    #expect(WakeState(allowed: true).nextLine == "Wakes only for jobs set to wake the Mac.")
}

@Test func theWakeWarningSaysWhatToTurnOn() throws {
    var due = Job()
    due.name = "Brief"
    due.nextRun = "2026-11-14T08:00:00Z"
    var paused = due
    paused.paused = true
    var done = due
    done.nextRun = ""

    // Nothing to warn about: no daemon waker, waking on, or no job that will run.
    #expect(WakeAdvice.check(nil, jobs: [due]) == nil)
    #expect(WakeAdvice.check(WakeState(allowed: true), jobs: [due]) == nil)
    #expect(WakeAdvice.check(WakeState(), jobs: []) == nil)
    #expect(WakeAdvice.check(WakeState(), jobs: [paused, done]) == nil)

    // Jobs, none set to wake: turn it on for every job.
    let all = try #require(WakeAdvice.check(WakeState(), jobs: [due, paused]))
    #expect(all.title == "Scheduled jobs can't wake the Mac" && all.args == ["schedule", "wake", "on", "--all"])

    // Jobs set to wake: turn it on for them.
    var waking = due
    waking.wake = true
    let one = try #require(WakeAdvice.check(WakeState(), jobs: [due, waking]))
    #expect(one.args == ["schedule", "wake", "on"] && one.detail.hasPrefix("Brief is set to wake the Mac"))
    let two = try #require(WakeAdvice.check(WakeState(), jobs: [waking, waking]))
    #expect(two.detail.hasPrefix("2 jobs are set to wake the Mac"))
}
