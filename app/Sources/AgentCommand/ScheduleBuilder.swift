import Foundation

/// The schedule form as the daemon reads and writes it (internal/schedule.Form):
/// the daemon maps it to and from cron, so the app has no cron of its own.
nonisolated struct ScheduleForm: Codable, Equatable {
    var kind = "weekdays"
    var every: Int?
    var hour = 9
    var minute = 0
    var weekDays: [Int]?
    var monthDays: [Int]?
    /// A one-off's day, "2026-12-01".
    var date: String?
    /// Custom's cron, as typed.
    var cron: String?

    enum CodingKeys: String, CodingKey {
        case kind, every, hour, minute, date, cron
        case weekDays = "week_days", monthDays = "month_days"
    }

    init(kind: String = "weekdays", every: Int? = nil, hour: Int = 9, minute: Int = 0, weekDays: [Int]? = nil,
         monthDays: [Int]? = nil, date: String? = nil, cron: String? = nil) {
        (self.kind, self.every, self.hour, self.minute) = (kind, every, hour, minute)
        (self.weekDays, self.monthDays, self.date, self.cron) = (weekDays, monthDays, date, cron)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "custom"
        every = try c.decodeIfPresent(Int.self, forKey: .every)
        hour = try c.decodeIfPresent(Int.self, forKey: .hour) ?? 0
        minute = try c.decodeIfPresent(Int.self, forKey: .minute) ?? 0
        weekDays = try c.decodeIfPresent([Int].self, forKey: .weekDays)
        monthDays = try c.decodeIfPresent([Int].self, forKey: .monthDays)
        date = try c.decodeIfPresent(String.self, forKey: .date)
        cron = try c.decodeIfPresent(String.self, forKey: .cron)
    }
}

/// A schedule as the form's controls set it: how often, which days, what time.
/// The daemon turns it into cron or a one-off (job.check), and back, so typing
/// cron moves the controls once the daemon has read it. Cron the controls
/// can't show is Custom, and stays as typed.
nonisolated struct ScheduleBuilder: Equatable {
    enum Kind: String, CaseIterable, Identifiable {
        case minutes, hours, daily, weekdays, weekly, monthly, once, custom
        var id: String { rawValue }

        var label: String {
            switch self {
            case .minutes: "Every few minutes"
            case .hours: "Every few hours"
            case .daily: "Every day"
            case .weekdays: "Weekdays"
            case .weekly: "On days of the week"
            case .monthly: "On days of the month"
            case .once: "Once"
            case .custom: "Custom (cron)"
            }
        }
    }

    /// Steps that divide the hour and the day evenly, so "every 15 minutes" is
    /// what it says (cron's */7 restarts each hour).
    static let minuteSteps = [1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30]
    static let hourSteps = [1, 2, 3, 4, 6, 8, 12]
    /// Monday first, as a week is said; Sunday is 0.
    static let weekOrder = [1, 2, 3, 4, 5, 6, 0]
    static let dayNames = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]

    var kind = Kind.weekdays
    var every = 15
    var hour = 9
    var minute = 0
    /// Days of the week, 0 = Sunday.
    var weekDays: Set<Int> = [1]
    /// Days of the month, 1-31.
    var monthDays: Set<Int> = [1]
    var once = Date()
    /// The cron as typed, for Custom.
    var cron = ""
    /// Custom was chosen in the picker: the cron stays as typed, even when the
    /// controls could show it.
    var pinned = false

    init() {}

    /// Custom, with cron as typed.
    static func custom(_ cron: String) -> ScheduleBuilder {
        var b = ScheduleBuilder()
        b.kind = .custom
        b.cron = cron
        return b
    }

    /// The controls for the daemon's form.
    init(form: ScheduleForm, calendar: Calendar = .current) {
        kind = Kind(rawValue: form.kind) ?? .custom
        every = form.every ?? 15
        hour = form.hour
        minute = form.minute
        if let days = form.weekDays, !days.isEmpty { weekDays = Set(days) }
        if let days = form.monthDays, !days.isEmpty { monthDays = Set(days) }
        cron = form.cron ?? ""
        if kind == .once, let date = form.date, let day = Self.dayFormatter(calendar).date(from: date) {
            once = calendar.date(bySettingHour: hour, minute: minute, second: 0, of: day) ?? day
        }
    }

    /// The form to send the daemon.
    func form(calendar: Calendar = .current) -> ScheduleForm {
        var f = ScheduleForm(kind: kind.rawValue, hour: hour, minute: minute)
        switch kind {
        case .minutes, .hours: f.every = every
        case .weekly: f.weekDays = weekDays.sorted()
        case .monthly: f.monthDays = monthDays.sorted()
        case .once:
            f.date = Self.dayFormatter(calendar).string(from: once)
            (f.hour, f.minute) = (calendar.component(.hour, from: once), calendar.component(.minute, from: once))
        case .custom: f.cron = cron
        case .daily, .weekdays: break
        }
        return f
    }

    /// Switches to another kind, keeping the time and days already chosen. Custom
    /// starts from the cron the daemon last read, when there's one.
    mutating func change(to next: Kind, cron current: String = "") {
        if next == .custom, kind != .custom { cron = current }
        pinned = next == .custom
        if next == .minutes, !Self.minuteSteps.contains(every) { every = 15 }
        if next == .hours, !Self.hourSteps.contains(every) { every = 1 }
        if next == .once, kind != .once {
            // The next time at the chosen hour and minute.
            let cal = Calendar.current
            var day = cal.date(bySettingHour: hour, minute: minute, second: 0, of: Date()) ?? Date()
            if day <= Date() { day = cal.date(byAdding: .day, value: 1, to: day) ?? day }
            once = day
        }
        kind = next
    }

    var usesTime: Bool { [.daily, .weekdays, .weekly, .monthly].contains(kind) }

    private static func dayFormatter(_ calendar: Calendar) -> DateFormatter {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        f.dateFormat = "yyyy-MM-dd"
        return f
    }
}
