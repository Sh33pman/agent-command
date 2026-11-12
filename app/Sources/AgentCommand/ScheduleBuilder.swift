import Foundation

/// A schedule as the form's controls set it: how often, which days, what time.
/// It reads and writes the job's schedule, which is cron or a one-off
/// ("once on 2026-12-01 at 14:00"), so choosing in the controls writes the
/// cron, and typing cron sets the controls. Cron the controls can't show is
/// Custom, and stays as typed.
struct ScheduleBuilder: Equatable {
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
    /// Monday first, as a week is said; cron numbers Sunday 0.
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

    init() {}

    /// The controls for a job's schedule: cron, or a one-off.
    init(schedule: String, calendar: Calendar = .current) {
        let text = schedule.trimmingCharacters(in: .whitespaces)
        if let date = Self.parseOnce(text, calendar: calendar) {
            kind = .once
            once = date
            (hour, minute) = (calendar.component(.hour, from: date), calendar.component(.minute, from: date))
            return
        }
        let fields = text.split(separator: " ").map(String.init)
        guard fields.count == 5, let parsed = Self.read(fields) else {
            kind = .custom
            cron = text
            return
        }
        self = parsed
    }

    /// The schedule to save: cron, or "once on … at …".
    func schedule(calendar: Calendar = .current) -> String {
        let at = "\(minute) \(hour)"
        switch kind {
        case .minutes: return every <= 1 ? "* * * * *" : "*/\(every) * * * *"
        case .hours: return every <= 1 ? "\(minute) * * * *" : "\(minute) */\(every) * * *"
        case .daily: return "\(at) * * *"
        case .weekdays: return "\(at) * * 1-5"
        case .weekly:
            let days = weekDays.isEmpty ? [1] : weekDays.sorted()
            return "\(at) * * " + days.map(String.init).joined(separator: ",")
        case .monthly:
            let days = monthDays.isEmpty ? [1] : monthDays.sorted()
            return "\(at) " + days.map(String.init).joined(separator: ",") + " * *"
        case .once:
            let f = DateFormatter()
            f.locale = Locale(identifier: "en_US_POSIX")
            f.calendar = calendar
            f.timeZone = calendar.timeZone
            f.dateFormat = "yyyy-MM-dd 'at' HH:mm"
            return "once on " + f.string(from: once)
        case .custom: return cron
        }
    }

    /// Switches to another kind, keeping the time and days already chosen.
    mutating func change(to next: Kind) {
        if next == .custom, kind != .custom { cron = schedule() }
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

    // MARK: Reading cron

    /// The controls for five cron fields, or nil when they don't fit one.
    private static func read(_ f: [String]) -> ScheduleBuilder? {
        var b = ScheduleBuilder()
        let (minuteField, hourField, dom, month, dow) = (f[0], f[1], f[2], f[3], f[4])
        guard month == "*" else { return nil }
        // Every few minutes.
        if hourField == "*", dom == "*", dow == "*" {
            if minuteField == "*" { b.kind = .minutes; b.every = 1; return b }
            if let n = step(minuteField), minuteSteps.contains(n) { b.kind = .minutes; b.every = n; return b }
            if let m = number(minuteField, 0...59) { b.kind = .hours; b.every = 1; b.minute = m; return b }
            return nil
        }
        guard let m = number(minuteField, 0...59) else { return nil }
        b.minute = m
        if dom == "*", dow == "*", let n = step(hourField), hourSteps.contains(n) {
            b.kind = .hours
            b.every = n
            return b
        }
        guard let h = number(hourField, 0...23) else { return nil }
        b.hour = h
        switch (dom, dow) {
        case ("*", "*"): b.kind = .daily
        case ("*", "1-5"): b.kind = .weekdays
        case ("*", _):
            guard let days = list(dow, 0...7) else { return nil }
            b.kind = .weekly
            b.weekDays = Set(days.map { $0 == 7 ? 0 : $0 })
        case (_, "*"):
            guard let days = list(dom, 1...31) else { return nil }
            b.kind = .monthly
            b.monthDays = Set(days)
        default: return nil
        }
        return b
    }

    private static func number(_ s: String, _ range: ClosedRange<Int>) -> Int? {
        guard let n = Int(s), range.contains(n) else { return nil }
        return n
    }

    private static func step(_ s: String) -> Int? {
        guard s.hasPrefix("*/") else { return nil }
        return Int(s.dropFirst(2))
    }

    /// "1,3,5" or "1-5" or a mix.
    private static func list(_ s: String, _ range: ClosedRange<Int>) -> [Int]? {
        var out: [Int] = []
        for part in s.split(separator: ",") {
            let ends = part.split(separator: "-").map(String.init)
            switch ends.count {
            case 1:
                guard let n = number(ends[0], range) else { return nil }
                out.append(n)
            case 2:
                guard let a = number(ends[0], range), let b = number(ends[1], range), a <= b else { return nil }
                out += Array(a...b)
            default: return nil
            }
        }
        return out.isEmpty ? nil : out
    }

    private static func parseOnce(_ text: String, calendar: Calendar) -> Date? {
        guard text.lowercased().hasPrefix("once on ") else { return nil }
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = calendar
        f.timeZone = calendar.timeZone
        f.dateFormat = "yyyy-MM-dd 'at' HH:mm"
        return f.date(from: String(text.dropFirst("once on ".count)))
    }
}
