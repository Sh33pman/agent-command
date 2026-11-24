import AppKit
import CoreGraphics
import Foundation

/// Who this app is to the daemon: an id it keeps, and what it is. It says so in
/// the hello on every connection (internal/ipc.ClientHello), so the daemon knows
/// who's there and who shows each notice.
nonisolated enum ClientIdentity {
    /// This app's id, made once and kept in its defaults.
    static func id(_ defaults: UserDefaults = .standard) -> String {
        let key = "clientID"
        if let id = defaults.string(forKey: key), !id.isEmpty { return id }
        let id = UUID().uuidString
        defaults.set(id, forKey: key)
        return id
    }

    static func hello(_ defaults: UserDefaults = .standard) -> ClientHello {
        ClientHello(id: id(defaults), kind: "mac", device: Host.current().localizedName ?? "",
                    caps: ["notify", "sound", "attach", "speech"], on_host: true)
    }
}

nonisolated struct ClientHello: Codable, Equatable {
    let id: String
    let kind: String
    let device: String
    let caps: [String]
    let on_host: Bool
}

/// Tells the daemon how the app is used: active in front, idle after a few
/// minutes without input, or in the background, and the session it shows. It
/// says so only when that changes.
final class PresenceReporter {
    /// No input for this long is idle.
    static let idleAfter: TimeInterval = 180

    var calls: DaemonCalls
    /// Seconds since the last input, anywhere on this Mac.
    var secondsIdle: () -> TimeInterval = {
        CGEventSource.secondsSinceLastEventType(.combinedSessionState, eventType: CGEventType(rawValue: ~0)!)
    }
    /// The session the app shows, if any.
    var focus: () -> String = { "" }
    private(set) var active = false
    private(set) var sent: (presence: String, focus: String)?

    init(calls: DaemonCalls = DaemonSocketCalls()) { self.calls = calls }

    /// The app came to the front, or went behind another.
    func setActive(_ active: Bool) {
        self.active = active
        update()
    }

    /// How it's used now: the app in front and used, idle, or in the background.
    var presence: String {
        guard active else { return "background" }
        return secondsIdle() >= Self.idleAfter ? "idle" : "active"
    }

    /// Checks again, from a timer and when the shown session changes.
    func update() {
        let now = (presence: presence, focus: focus())
        if let sent, sent == now { return }
        sent = now
        nonisolated struct Presence: Encodable { let presence: String; let focus: String }
        let data = (try? JSONEncoder().encode(Presence(presence: now.presence, focus: now.focus))) ?? Data()
        calls.call("client.presence", data) { [weak self] result in
            if case .failure = result { self?.sent = nil } // tries again next time
        }
    }
}
