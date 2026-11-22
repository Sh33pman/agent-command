import AppKit

/// Links clicked in a terminal (⌘-click, or a link the program marked with OSC 8).
enum TerminalLinks {
    /// Only web and mail links open. A program can print any link, and a file or
    /// custom-scheme link could start an app.
    static let schemes: Set<String> = ["http", "https", "mailto"]

    /// The URL to open for a clicked link, or nil when it shouldn't open. A bare
    /// "example.com/x" counts as https.
    static func url(_ text: String) -> URL? {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let url = URL(string: text) else { return nil }
        if let scheme = url.scheme?.lowercased() {
            return schemes.contains(scheme) && (scheme == "mailto" || url.host != nil) ? url : nil
        }
        guard let first = text.split(separator: "/").first, first.contains("."), !text.hasPrefix(".") else { return nil }
        return URL(string: "https://" + text).flatMap { $0.host != nil ? $0 : nil }
    }

    /// The text of a C string with a length, as Ghostty passes them.
    static func string(_ pointer: UnsafePointer<CChar>?, _ length: Int) -> String {
        guard let pointer, length > 0 else { return "" }
        return pointer.withMemoryRebound(to: UInt8.self, capacity: length) {
            String(decoding: UnsafeBufferPointer(start: $0, count: length), as: UTF8.self)
        }
    }
}

/// A file a terminal link points at: a path as Ghostty finds them in the output
/// (/abs, ./rel, ../rel, ~/home, src/bare.go), or a file:// link from OSC 8, with
/// the ":line:col" that compilers and Claude print after it.
struct FileLink: Equatable {
    let path: String
    var line = 0
    var column = 0

    /// The file a clicked link names, or nil when it's not a file. Web links aren't.
    static func from(_ text: String) -> FileLink? {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, TerminalLinks.url(text) == nil else { return nil }
        if let url = URL(string: text), let scheme = url.scheme?.lowercased() {
            // Only file:// on this Mac; "C:" or "src:12" aren't schemes worth reading.
            guard scheme == "file", (url.host ?? "").isEmpty || url.host == "localhost", !url.path.isEmpty else {
                return text.contains("/") && !text.contains("://") ? split(text) : nil
            }
            return split(url.path)
        }
        guard text.contains("/") || text.hasPrefix("~") else { return nil }
        return split(text)
    }

    /// Takes a trailing ":line" or ":line:col" off, as acmd edit does.
    private static func split(_ text: String) -> FileLink {
        var path = Substring(text)
        var numbers: [Int] = []
        while numbers.count < 2, let colon = path.lastIndex(of: ":"), let n = Int(path[path.index(after: colon)...]), n > 0 {
            numbers.insert(n, at: 0)
            path = path[..<colon]
        }
        return FileLink(path: String(path), line: numbers.first ?? 0, column: numbers.count > 1 ? numbers[1] : 0)
    }

    /// The file, absolute: ~ is home, and a relative path is in folder.
    func resolved(in folder: String, home: String = NSHomeDirectory()) -> String {
        var full = path
        if full == "~" || full.hasPrefix("~/") {
            full = home + full.dropFirst()
        } else if !full.hasPrefix("/") {
            full = (folder as NSString).appendingPathComponent(full)
        }
        return (full as NSString).standardizingPath
    }

    /// The daemon's editor.open for it.
    func editorOpen(in folder: String) -> EditorOpen {
        EditorOpen(path: resolved(in: folder), in: folder, line: line > 0 ? line : nil, column: line > 0 && column > 0 ? column : nil)
    }
}
