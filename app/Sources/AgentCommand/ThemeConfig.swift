import Foundation

/// Reading and changing the theme in the app's Ghostty config, and finding the
/// themes that can be chosen. This has no dependencies, so it can be checked on
/// its own.
enum ThemeConfig {
    /// The theme a config sets, if any. Like Ghostty, the last line wins and an
    /// empty value means none.
    static func theme(inConfig text: String) -> String? {
        var found: String?
        for line in text.split(whereSeparator: \.isNewline) {
            guard let value = themeValue(line) else { continue }
            found = value.isEmpty ? nil : value
        }
        return found
    }

    /// The config with its theme changed and everything else left as it was.
    /// nil removes the theme.
    static func setting(theme: String?, inConfig text: String) -> String {
        var lines = text.isEmpty ? [] : text.components(separatedBy: "\n")
        if lines.last == "" { lines.removeLast() }
        let wanted = theme.map { "theme = \($0)" }
        var placed = false
        lines = lines.compactMap { line in
            guard themeValue(Substring(line)) != nil else { return line }
            // The first theme line keeps its place; the rest would override it.
            if !placed, let wanted {
                placed = true
                return wanted
            }
            return nil
        }
        if !placed, let wanted { lines.append(wanted) }
        return lines.isEmpty ? "" : lines.joined(separator: "\n") + "\n"
    }

    private static func themeValue(_ line: Substring) -> String? {
        let parts = line.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2, parts[0].trimmingCharacters(in: .whitespaces) == "theme" else { return nil }
        return parts[1].trimmingCharacters(in: .whitespaces)
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
