import Foundation

/// Whether new sessions start with Claude's Remote Control: `remote_control` in
/// acmd's config.toml, off by default. The CLI writes it, so the file keeps one
/// writer that knows how to keep its comments.
enum RemoteControlSetting {
    static var configPath: String {
        ((AppFont.configPath as NSString).deletingLastPathComponent as NSString)
            .appendingPathComponent("config.toml")
    }

    static var isOn: Bool {
        isOn(inConfig: (try? String(contentsOfFile: configPath, encoding: .utf8)) ?? "")
    }

    /// Reads the top-level `remote_control`; a missing key is off.
    static func isOn(inConfig text: String) -> Bool {
        for raw in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("[") { return false } // past the top level
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            guard parts.count == 2, parts[0] == "remote_control" else { continue }
            let value = parts[1].split(separator: "#", maxSplits: 1).first?.trimmingCharacters(in: .whitespaces)
            return value == "true"
        }
        return false
    }

    /// The arguments for `acmd` that turn it on or off.
    static func arguments(on: Bool) -> [String] { ["remote-control", on ? "on" : "off"] }
}
