import Foundation
import Testing

@testable import AgentCommand

private var utc: Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "UTC")!
    return c
}

/// The controls read the daemon's form and write it back the same: the daemon
/// does the cron.
@Test func theFormReadsIntoTheControlsAndBack() {
    let forms = [
        ScheduleForm(kind: "minutes", every: 15, hour: 0, minute: 0),
        ScheduleForm(kind: "hours", every: 6, hour: 0, minute: 5),
        ScheduleForm(kind: "daily", hour: 9, minute: 0),
        ScheduleForm(kind: "weekdays", hour: 8, minute: 30),
        ScheduleForm(kind: "weekly", hour: 17, minute: 0, weekDays: [1, 5]),
        ScheduleForm(kind: "monthly", hour: 7, minute: 0, monthDays: [1, 15]),
        ScheduleForm(kind: "custom", hour: 0, minute: 0, cron: "0 9 1 * 1"),
    ]
    for f in forms {
        #expect(ScheduleBuilder(form: f).form() == f, "\(f.kind)")
    }
    #expect(ScheduleBuilder(form: ScheduleForm(kind: "fortnightly")).kind == .custom, "a kind the app doesn't know")
}

@Test func oneOffsCarryTheirDayAndTime() {
    let f = ScheduleForm(kind: "once", hour: 14, minute: 30, date: "2026-12-01")
    let b = ScheduleBuilder(form: f, calendar: utc)
    #expect(b.kind == .once && b.form(calendar: utc) == f)
    var next = ScheduleBuilder()
    next.hour = 0
    next.minute = 0
    next.change(to: .once)
    #expect(next.once > Date(), "a new one-off is in the future")
}

@Test func switchingKeepsWhatWasChosen() {
    var b = ScheduleBuilder()
    b.change(to: .minutes)
    #expect(b.every == 15, "a step that divides the hour")
    b.change(to: .hours)
    #expect(b.every == 1, "15 isn't an hour step, so it becomes 1")
    b.change(to: .daily)
    b.hour = 17
    #expect(b.form() == ScheduleForm(kind: "daily", hour: 17, minute: 0), "keeps the time")
    b.change(to: .custom, cron: "0 17 * * *")
    #expect(b.cron == "0 17 * * *" && b.pinned, "custom starts from the cron the daemon read")
    b.change(to: .weekly)
    #expect(!b.pinned && !b.usesTime == false)
    #expect(ScheduleBuilder.Kind.allCases.allSatisfy { !$0.label.isEmpty })
}

/// Typed cron is Custom until the daemon reads it; then the controls follow,
/// unless Custom was chosen.
@Test func typedCronMovesTheControlsOnceTheDaemonReadsIt() throws {
    var draft = JobDraft()
    #expect(draft.builder.kind == .weekdays)
    draft.typeCron("0 8 * * 1,5")
    #expect(draft.builder.kind == .custom && draft.job.schedule == "0 8 * * 1,5")
    draft.takeForm(ScheduleForm(kind: "weekly", hour: 8, minute: 0, weekDays: [1, 5]))
    #expect(draft.builder.kind == .weekly && draft.builder.weekDays == [1, 5], "the daemon's reading sets the controls")
    #expect(draft.job.schedule.isEmpty && draft.job.form?.kind == "weekly", "a form, not cron, is sent")

    var pinned = draft.builder
    pinned.change(to: .custom, cron: "0 8 * * 1,5")
    draft.builder = pinned
    draft.typeCron("0 9 * * 1")
    draft.takeForm(ScheduleForm(kind: "weekly", hour: 9, minute: 0, weekDays: [1]))
    #expect(draft.builder.kind == .custom && draft.builder.cron == "0 9 * * 1", "chosen Custom stays as typed")

    // A saved job opens as the daemon's form, or as its cron when there's none.
    var job = Job()
    job.form = ScheduleForm(kind: "daily", hour: 7, minute: 0)
    #expect(JobDraft(job).builder.kind == .daily)
    job.form = nil
    job.cron = "0 9 1 * 1"
    #expect(JobDraft(job).builder.kind == .custom && JobDraft(job).builder.cron == "0 9 1 * 1")
}

@Test func theFormDecodesWhatTheDaemonSends() throws {
    let f = try JSONDecoder().decode(ScheduleForm.self, from: Data(#"{"kind": "weekly", "hour": 9, "minute": 30, "week_days": [1, 3]}"#.utf8))
    #expect(f == ScheduleForm(kind: "weekly", hour: 9, minute: 30, weekDays: [1, 3]))
    let bare = try JSONDecoder().decode(ScheduleForm.self, from: Data("{}".utf8))
    #expect(bare.kind == "custom" && bare.hour == 0)
    let encoded = try JSONSerialization.jsonObject(with: JSONEncoder().encode(f)) as? [String: Any]
    #expect(encoded?["week_days"] as? [Int] == [1, 3] && encoded?["kind"] as? String == "weekly")
}
