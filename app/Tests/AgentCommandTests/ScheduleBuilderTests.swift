import Foundation
import Testing

@testable import AgentCommand

private var utc: Calendar {
    var c = Calendar(identifier: .gregorian)
    c.timeZone = TimeZone(identifier: "UTC")!
    return c
}

/// Cron the controls can show reads into them, and writes back the same.
@Test func cronReadsIntoTheControlsAndBack() {
    for (cron, kind) in [
        ("* * * * *", ScheduleBuilder.Kind.minutes),
        ("*/15 * * * *", .minutes),
        ("30 * * * *", .hours),
        ("5 */6 * * *", .hours),
        ("0 9 * * *", .daily),
        ("30 8 * * 1-5", .weekdays),
        ("0 17 * * 1,5", .weekly),
        ("0 10 * * 0,6", .weekly),
        ("0 7 1,15 * *", .monthly),
    ] {
        let b = ScheduleBuilder(schedule: cron)
        #expect(b.kind == kind, "\(cron)")
        #expect(b.schedule() == cron, "\(cron) round trip")
    }
    let fri = ScheduleBuilder(schedule: "45 7 * * 1-3,5")
    #expect(fri.kind == .weekly && fri.weekDays == [1, 2, 3, 5] && fri.hour == 7 && fri.minute == 45)
    #expect(fri.schedule() == "45 7 * * 1,2,3,5")
    #expect(ScheduleBuilder(schedule: "0 9 * * 7").weekDays == [0], "7 is Sunday")
    #expect(ScheduleBuilder(schedule: "0 9 1-3 * *").monthDays == [1, 2, 3])
}

/// Cron they can't show is Custom, as typed.
@Test func otherCronIsCustom() {
    for cron in ["*/7 * * * *", "0,30 9 * * *", "0 9 1 * 1", "0 9 * 6 *", "0 */5 * * *", "0 9 * * x", "0 9 31-1 * *",
                 "0 9 * * 1-2-3", "61 9 * * *", "not cron", "", "0 9 * *"] {
        let b = ScheduleBuilder(schedule: cron)
        #expect(b.kind == .custom, "\(cron)")
        #expect(b.schedule() == cron.trimmingCharacters(in: .whitespaces))
    }
}

@Test func theControlsWriteCron() {
    var b = ScheduleBuilder()
    #expect(b.schedule() == "0 9 * * 1-5", "weekdays at 09:00 to start")
    b.change(to: .minutes)
    #expect(b.schedule() == "*/15 * * * *", "a step that divides the hour")
    b.every = 1
    #expect(b.schedule() == "* * * * *")
    b.change(to: .hours)
    b.minute = 30
    #expect(b.schedule() == "30 * * * *")
    b.every = 2
    #expect(b.schedule() == "30 */2 * * *")
    b.change(to: .daily)
    b.hour = 17
    #expect(b.schedule() == "30 17 * * *", "keeps the time")
    b.change(to: .weekly)
    b.weekDays = [5, 1]
    #expect(b.schedule() == "30 17 * * 1,5")
    b.weekDays = []
    #expect(b.schedule() == "30 17 * * 1", "never no days")
    b.change(to: .monthly)
    b.monthDays = [15, 1]
    #expect(b.schedule() == "30 17 1,15 * *")
    b.monthDays = []
    #expect(b.schedule() == "30 17 1 * *")
    b.change(to: .custom)
    #expect(b.schedule() == "30 17 1 * *", "custom starts from what was chosen")
    #expect(b.usesTime == false)
    var steps = ScheduleBuilder()
    steps.change(to: .minutes)
    steps.change(to: .hours)
    #expect(steps.every == 1, "15 isn't an hour step, so it becomes 1")
    #expect(ScheduleBuilder.Kind.allCases.allSatisfy { !$0.label.isEmpty })
}

@Test func oneOffs() throws {
    let b = ScheduleBuilder(schedule: "once on 2026-12-01 at 14:30", calendar: utc)
    #expect(b.kind == .once && b.hour == 14 && b.minute == 30)
    #expect(b.schedule(calendar: utc) == "once on 2026-12-01 at 14:30")
    var next = ScheduleBuilder()
    next.hour = 0
    next.minute = 0
    next.change(to: .once)
    #expect(next.once > Date(), "a new one-off is in the future")
    #expect(ScheduleBuilder(schedule: "once on someday").kind == .custom)
}

/// The form keeps Custom once chosen, even for cron the controls could show.
@Test func theDraftKeepsCustom() throws {
    var draft = JobDraft()
    #expect(draft.builder.kind == .weekdays)
    var b = draft.builder
    b.change(to: .custom)
    draft.builder = b
    #expect(draft.customSchedule && draft.builder.kind == .custom && draft.schedule == "0 9 * * 1-5")
    b = draft.builder
    b.change(to: .daily)
    draft.builder = b
    #expect(!draft.customSchedule && draft.schedule == "0 9 * * *")
    draft.schedule = "0 8 * * 1,5" // typed into the cron field
    #expect(draft.builder.kind == .weekly && draft.builder.weekDays == [1, 5], "typing cron sets the controls")

    // A saved job opens as its cron, or its one-off.
    var job = Job()
    job.cron = "0 7 * * 1"
    job.schedule = "every Monday at 07:00"
    #expect(JobDraft(job).schedule == "0 7 * * 1")
    job.cron = ""
    job.once = "2026-12-01T14:30:00Z"
    let once = JobDraft(job)
    #expect(once.builder.kind == .once && once.schedule.hasPrefix("once on 2026-12-0"))
}
