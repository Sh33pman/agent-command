import Foundation

/// What a plain new session (⌘N) starts: `default_harness` in acmd's config.toml,
/// which `acmd new` reads too. Unset, ⌘N asks first.
enum DefaultHarness {
    /// What can be the default: the agents. A terminal has its own shortcut.
    static let choices = Harness.agents

    static var current: Harness? {
        value(inConfig: (try? String(contentsOfFile: AgentRemoteControlSetting.configPath, encoding: .utf8)) ?? "")
    }

    /// Reads the top-level `default_harness`; missing, empty or unknown is unset.
    static func value(inConfig text: String) -> Harness? {
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("[") { return nil } // past the top level
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            guard parts.count == 2, parts[0] == "default_harness" else { continue }
            var value = parts[1]
            if value.hasPrefix("\""), let end = value.dropFirst().firstIndex(of: "\"") {
                value = String(value[value.index(after: value.startIndex)..<end])
            } else {
                value = value.split(separator: "#", maxSplits: 1).first.map { $0.trimmingCharacters(in: .whitespaces) } ?? ""
            }
            return Harness(rawValue: value).flatMap { choices.contains($0) ? $0 : nil }
        }
        return nil
    }

    /// The ⌘N menu item's title.
    static func menuTitle(_ harness: Harness?) -> String {
        harness.map { "New \($0.title) Session" } ?? "New Session (Pick a Default)…"
    }

    /// The arguments for `acmd` that set it ("" unsets it, so ⌘N asks again).
    static func arguments(_ harness: Harness?) -> [String] {
        ["config", "set", "--", "default_harness", harness?.rawValue ?? ""]
    }
}
