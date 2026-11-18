import Foundation

/// Whether new agent sessions (Claude and Antigravity) start with Agent Remote Control:
/// `agent_remote_control` in acmd's config.toml (or its old name, `remote_control`), off by
/// default. The CLI writes it, so the file keeps one writer that knows how to keep its comments.
enum AgentRemoteControlSetting {
    static var configPath: String {
        ((AppFont.configPath as NSString).deletingLastPathComponent as NSString)
            .appendingPathComponent("config.toml")
    }

    static var isOn: Bool {
        isOn(inConfig: (try? String(contentsOfFile: configPath, encoding: .utf8)) ?? "")
    }

    /// Reads the top-level `agent_remote_control`, or `remote_control` when it's missing;
    /// a missing key is off.
    static func isOn(inConfig text: String) -> Bool {
        var old: Bool?
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("[") { break } // past the top level
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            guard parts.count == 2, parts[0] == "agent_remote_control" || parts[0] == "remote_control" else { continue }
            let value = parts[1].split(separator: "#", maxSplits: 1).first?.trimmingCharacters(in: .whitespaces) == "true"
            if parts[0] == "agent_remote_control" { return value }
            if old == nil { old = value }
        }
        return old ?? false
    }

    /// The command palette's entry: what choosing it does.
    static func paletteTitle(isOn: Bool) -> String {
        isOn ? "Turn Agent Remote Control off for new sessions" : "Turn Agent Remote Control on for new sessions"
    }

    /// The arguments for `acmd` that turn it on or off.
    static func arguments(on: Bool) -> [String] { ["agent-remote-control", on ? "on" : "off"] }
}
