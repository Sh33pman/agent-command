import Foundation

/// Reading and changing settings in the app's Ghostty config (the theme, the font),
/// and finding the themes that can be chosen. This has no dependencies, so it can be
/// checked on its own.
enum ThemeConfig {
    /// The theme a config sets, if any. Like Ghostty, the last line wins and an
    /// empty value means none.
    static func theme(inConfig text: String) -> String? {
        value("theme", inConfig: text)
    }

    /// The config with its theme changed and everything else left as it was.
    /// nil removes the theme.
    static func setting(theme: String?, inConfig text: String) -> String {
        setting("theme", to: theme, inConfig: text)
    }

    /// The value the last line for key sets, if any; an empty value means none.
    static func value(_ key: String, inConfig text: String) -> String? {
        var found: String?
        for line in text.split(whereSeparator: \.isNewline) {
            guard let value = value(of: key, in: line) else { continue }
            found = value.isEmpty ? nil : value
        }
        return found
    }

    /// The config with key set to value, in the place of its first line, and
    /// everything else left as it was. Other lines for key go, as they'd override it,
    /// unless keepingOthers: Ghostty reads later font-family lines as fallbacks.
    /// nil removes the key's first line, or all of them.
    static func setting(_ key: String, to value: String?, inConfig text: String, keepingOthers: Bool = false) -> String {
        var lines = text.isEmpty ? [] : text.components(separatedBy: "\n")
        if lines.last == "" { lines.removeLast() }
        let wanted = value.map { "\(key) = \($0)" }
        var placed = false
        lines = lines.compactMap { line in
            guard self.value(of: key, in: Substring(line)) != nil else { return line }
            if placed { return keepingOthers ? line : nil }
            placed = true
            return wanted
        }
        if !placed, let wanted { lines.append(wanted) }
        return lines.isEmpty ? "" : lines.joined(separator: "\n") + "\n"
    }

    private static func value(of key: String, in line: Substring) -> String? {
        let parts = line.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2, parts[0].trimmingCharacters(in: .whitespaces) == key else { return nil }
        return parts[1].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
    }

    /// The names of the themes in these folders, sorted, without duplicates.
    static func themeNames(in folders: [String], fileManager: FileManager = .default) -> [String] {
        var names = Set<String>()
        for folder in folders {
            for name in (try? fileManager.contentsOfDirectory(atPath: folder)) ?? [] where !name.hasPrefix(".") {
                var isFolder: ObjCBool = false
                let path = (folder as NSString).appendingPathComponent(name)
                if fileManager.fileExists(atPath: path, isDirectory: &isFolder), !isFolder.boolValue {
                    names.insert(name)
                }
            }
        }
        return names.sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending }
    }
}
