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
