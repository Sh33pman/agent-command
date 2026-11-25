import Foundation

/// The app's own log, app.log in the state folder beside daemon.log, kept the same way
/// as internal/cli/daemonlog.go keeps that one. Opened from Finder or the Dock, the app's
/// output goes to /dev/null, which loses trace()'s record of why the app went away, and
/// anything else libghostty or the Swift runtime writes there before an exit.
///
/// It's nonisolated because its timer runs off the main thread. Isolated to the main
/// actor, as the app's types are by default, the timer's first check stopped the app.
nonisolated enum AppLog {
    /// How many days of logs are kept, today's included.
    static let days = 3
    /// A day's log is moved aside early past this, so the app can't fill the disk.
    static let limit = 20 << 20
    /// How often a running app checks the log.
    static let check: TimeInterval = 10 * 60

    /// Set once, by start, on the main thread.
    nonisolated(unsafe) private static var timer: DispatchSourceTimer?

    static func path(_ env: [String: String]) -> String {
        (DaemonPaths.stateDir(env) as NSString).appendingPathComponent("app.log")
    }

    /// Points stdout and stderr at the log, unless the app was started from a terminal,
    /// where the output stays.
    static func start(_ env: [String: String] = ProcessInfo.processInfo.environment) {
        guard isatty(STDERR_FILENO) == 0 else { return }
        let path = path(env)
        _ = rotate(path, now: Date())
        guard pointOutput(at: path) else { return }
        timer = watch(path, every: check) { _ = pointOutput(at: path) }
    }

    /// Rotates the log at path every interval, off the main thread, and calls moved
    /// there after it moves the log. Cancel the timer to stop it.
    static func watch(_ path: String, every interval: TimeInterval,
                      moved: @escaping @Sendable () -> Void) -> DispatchSourceTimer {
        let t = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        t.schedule(deadline: .now() + interval, repeating: interval)
        t.setEventHandler { @Sendable in
            // Without a log to write to, the output keeps going where it was.
            if rotate(path, now: Date()) { moved() }
        }
        t.resume()
        return t
    }

    /// Gives a log from an earlier day, or one that has grown too big, the date of its
    /// last entry, then removes logs from before the days being kept. It reports
    /// whether it moved the log.
    @discardableResult
    static func rotate(_ path: String, now: Date) -> Bool {
        let fm = FileManager.default
        var moved = false
        if let attrs = try? fm.attributesOfItem(atPath: path),
           let last = attrs[.modificationDate] as? Date {
            let size = (attrs[.size] as? NSNumber)?.intValue ?? 0
            if day(last) != day(now) || size >= limit {
                let dated = path + "." + day(last)
                // That day's log replaces what it set aside before.
                try? fm.removeItem(atPath: dated)
                moved = (try? fm.moveItem(atPath: path, toPath: dated)) != nil
            }
        }
        removeOld(path, now: now)
        return moved
    }

    /// Removes dated logs older than the days being kept.
    static func removeOld(_ path: String, now: Date) {
        let dir = (path as NSString).deletingLastPathComponent
        let name = (path as NSString).lastPathComponent + "."
        guard let oldestDate = Calendar.current.date(byAdding: .day, value: -(days - 1), to: now),
              let files = try? FileManager.default.contentsOfDirectory(atPath: dir) else { return }
        let oldest = day(oldestDate)
        for file in files where file.hasPrefix(name) {
            let date = String(file.dropFirst(name.count))
            // Dates in this form sort as text.
            if isDay(date), date < oldest {
                try? FileManager.default.removeItem(atPath: (dir as NSString).appendingPathComponent(file))
            }
        }
    }

    static func day(_ date: Date) -> String { formatter.string(from: date) }

    private static func isDay(_ s: String) -> Bool {
        s.range(of: #"^\d{4}-\d{2}-\d{2}$"#, options: .regularExpression) != nil
    }

    private static let formatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.calendar = Calendar(identifier: .gregorian)
        f.timeZone = .current
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    /// Makes stdout and stderr append to path.
    private static func pointOutput(at path: String) -> Bool {
        try? FileManager.default.createDirectory(
            atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700])
        let fd = open(path, O_WRONLY | O_CREAT | O_APPEND | O_CLOEXEC, 0o600)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        return dup2(fd, STDOUT_FILENO) >= 0 && dup2(fd, STDERR_FILENO) >= 0
    }
}
