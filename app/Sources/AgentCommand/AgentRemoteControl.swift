import Foundation

/// Whether new agent sessions (Claude and Antigravity) start with Agent Remote Control:
/// the daemon's `agent_remote_control` setting, off by default. The app reads it from the
/// snapshot and changes it with config.set.
enum AgentRemoteControlSetting {
    static var isOn: Bool { HostSettings.shared.bool(key) }

    static let key = "agent_remote_control"

    /// The command palette's entry: what choosing it does.
    static func paletteTitle(isOn: Bool) -> String {
        isOn ? "Turn Agent Remote Control off for new sessions" : "Turn Agent Remote Control on for new sessions"
    }

    /// The change that turns it on or off.
    static func change(on: Bool) -> [String: SettingValue] { [key: .bool(on)] }
}
