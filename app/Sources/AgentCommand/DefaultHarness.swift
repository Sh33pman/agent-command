import Foundation

/// What a plain new session (⌘N) starts: the daemon's `default_harness` setting,
/// which `acmd new` reads too. Unset, ⌘N asks first.
enum DefaultHarness {
    /// What can be the default: the agents. A terminal has its own shortcut.
    static var choices: [Harness] { Harness.agents }

    static let key = "default_harness"

    static var current: Harness? { value(HostSettings.shared.string(key)) }

    /// The harness the setting names; empty or unknown is unset.
    static func value(_ setting: String) -> Harness? {
        Harness(rawValue: setting).flatMap { choices.contains($0) ? $0 : nil }
    }

    /// The ⌘N menu item's title.
    static func menuTitle(_ harness: Harness?) -> String {
        harness.map { "New \($0.title) Session" } ?? "New Session (Pick a Default)…"
    }

    /// The change that sets it (nil unsets it, so ⌘N asks again).
    static func change(_ harness: Harness?) -> [String: SettingValue] { [key: .string(harness?.rawValue ?? "")] }
}
