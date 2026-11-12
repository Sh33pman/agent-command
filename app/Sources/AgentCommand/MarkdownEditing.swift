import Foundation

/// What the prompt editor's toolbar and shortcuts do to Markdown.
enum MarkdownCommand: Equatable, CaseIterable {
    case bold, italic, strikethrough, code, link
    case heading1, heading2, heading3
    case bulletList, numberedList, taskList, quote, codeBlock

    var title: String {
        switch self {
        case .bold: "Bold"
        case .italic: "Italic"
        case .strikethrough: "Strikethrough"
        case .code: "Code"
        case .link: "Link"
        case .heading1: "Heading 1"
        case .heading2: "Heading 2"
        case .heading3: "Heading 3"
        case .bulletList: "Bulleted List"
        case .numberedList: "Numbered List"
        case .taskList: "Checklist"
        case .quote: "Quote"
        case .codeBlock: "Code Block"
        }
    }

    /// The toolbar's text, where it shows text instead of a symbol.
    var label: String? {
        switch self {
        case .heading1: "H1"
        case .heading2: "H2"
        case .heading3: "H3"
        default: nil
        }
    }

    /// The toolbar's SF Symbol.
    var symbol: String {
        switch self {
        case .bold: "bold"
        case .italic: "italic"
        case .strikethrough: "strikethrough"
        case .code: "chevron.left.forwardslash.chevron.right"
        case .link: "link"
        case .heading1: "textformat.size.larger"
        case .heading2: "textformat.size"
        case .heading3: "textformat.size.smaller"
        case .bulletList: "list.bullet"
        case .numberedList: "list.number"
        case .taskList: "checklist"
        case .quote: "text.quote"
        case .codeBlock: "curlybraces"
        }
    }

    /// The key that goes with ⌘ (⌥⌘ for headings, ⇧⌘ for lists), as shown in
    /// tooltips.
    var shortcut: String {
        switch self {
        case .bold: "⌘B"
        case .italic: "⌘I"
        case .strikethrough: "⇧⌘X"
        case .code: "⌘E"
        case .link: "⌘K"
        case .heading1: "⌥⌘1"
        case .heading2: "⌥⌘2"
        case .heading3: "⌥⌘3"
        case .bulletList: "⇧⌘8"
        case .numberedList: "⇧⌘7"
        case .taskList: "⇧⌘9"
        case .quote: "⇧⌘."
        case .codeBlock: "⌥⌘C"
        }
    }

    /// The command for a key pressed with modifiers (from NSEvent's
    /// charactersIgnoringModifiers), if it's one.
    static func forKey(_ key: String, command: Bool, shift: Bool, option: Bool) -> MarkdownCommand? {
        guard command else { return nil }
        switch (key.lowercased(), shift, option) {
        case ("b", false, false): return .bold
        case ("i", false, false): return .italic
        case ("x", true, false): return .strikethrough
        case ("e", false, false): return .code
        case ("k", false, false): return .link
        case ("1", false, true), ("¡", false, true): return .heading1
        case ("2", false, true), ("™", false, true): return .heading2
        case ("3", false, true), ("£", false, true): return .heading3
        case ("8", true, false), ("*", true, false): return .bulletList
        case ("7", true, false), ("&", true, false): return .numberedList
        case ("9", true, false), ("(", true, false): return .taskList
        case (".", true, false), (">", true, false): return .quote
        case ("c", false, true), ("ç", false, true): return .codeBlock
        default: return nil
        }
    }
}

/// Applies MarkdownCommands to text. Ranges are NSRanges in UTF-16, as
/// NSTextView has them.
enum MarkdownEdit {
    struct Result: Equatable {
        let text: String
        let selection: NSRange
    }

    static func apply(_ command: MarkdownCommand, to text: String, selection: NSRange) -> Result {
        switch command {
        case .bold: wrap(text, selection, "**")
        case .italic: wrap(text, selection, "*")
        case .strikethrough: wrap(text, selection, "~~")
        case .code: wrap(text, selection, "`")
        case .link: link(text, selection)
        case .heading1: heading(text, selection, 1)
        case .heading2: heading(text, selection, 2)
        case .heading3: heading(text, selection, 3)
        case .bulletList: prefixLines(text, selection) { _ in "- " }
        case .numberedList: prefixLines(text, selection) { "\($0 + 1). " }
        case .taskList: prefixLines(text, selection) { _ in "- [ ] " }
        case .quote: prefixLines(text, selection) { _ in "> " }
        case .codeBlock: codeBlock(text, selection)
        }
    }

    /// Wraps the selection in marker, or unwraps it when it's wrapped already.
    /// With nothing selected, it puts the cursor between a pair.
    private static func wrap(_ text: String, _ sel: NSRange, _ marker: String) -> Result {
        let ns = text as NSString
        let m = (marker as NSString).length
        let selected = ns.substring(with: sel)
        // **word** selected with its markers, or word selected between them.
        if selected.count >= 2 * marker.count, selected.hasPrefix(marker), selected.hasSuffix(marker),
           !(marker == "*" && selected.hasPrefix("**") && !selected.hasPrefix("***")) {
            let inner = (selected as NSString).substring(with: NSRange(location: m, length: sel.length - 2 * m))
            return Result(text: ns.replacingCharacters(in: sel, with: inner), selection: NSRange(location: sel.location, length: sel.length - 2 * m))
        }
        if sel.location >= m, sel.location + sel.length + m <= ns.length,
           ns.substring(with: NSRange(location: sel.location - m, length: m)) == marker,
           ns.substring(with: NSRange(location: sel.location + sel.length, length: m)) == marker,
           !isPartOfLongerMarker(ns, sel, marker) {
            let outer = NSRange(location: sel.location - m, length: sel.length + 2 * m)
            return Result(text: ns.replacingCharacters(in: outer, with: selected), selection: NSRange(location: sel.location - m, length: sel.length))
        }
        let wrapped = marker + selected + marker
        return Result(text: ns.replacingCharacters(in: sel, with: wrapped), selection: NSRange(location: sel.location + m, length: sel.length))
    }

    /// Italic's * next to bold's **: don't take one star off bold.
    private static func isPartOfLongerMarker(_ ns: NSString, _ sel: NSRange, _ marker: String) -> Bool {
        guard marker == "*" else { return false }
        let before = sel.location >= 2 ? ns.substring(with: NSRange(location: sel.location - 2, length: 1)) : ""
        let afterIndex = sel.location + sel.length + 1
        let after = afterIndex < ns.length ? ns.substring(with: NSRange(location: afterIndex, length: 1)) : ""
        return before == "*" || after == "*"
    }

    /// [selection](url), with "url" selected to type over. With nothing selected,
    /// [text](url) with "text" selected.
    private static func link(_ text: String, _ sel: NSRange) -> Result {
        let ns = text as NSString
        if sel.length == 0 {
            let inserted = "[text](url)"
            return Result(text: ns.replacingCharacters(in: sel, with: inserted), selection: NSRange(location: sel.location + 1, length: 4))
        }
        let label = ns.substring(with: sel)
        let inserted = "[\(label)](url)"
        let urlAt = sel.location + (label as NSString).length + 3
        return Result(text: ns.replacingCharacters(in: sel, with: inserted), selection: NSRange(location: urlAt, length: 3))
    }

    /// The lines the selection touches, as one range of whole lines.
    private static func lineRange(_ ns: NSString, _ sel: NSRange) -> NSRange {
        var range = ns.lineRange(for: sel)
        // Not the newline that ends the last line: prefixes go on lines, not after them.
        if range.length > 0, ns.substring(with: NSRange(location: NSMaxRange(range) - 1, length: 1)) == "\n",
           NSMaxRange(range) > NSMaxRange(sel) || sel.length == 0 || NSMaxRange(sel) == NSMaxRange(range) {
            range.length -= 1
        }
        return range
    }

    /// Makes the lines headings of a level, or plain again when they already are.
    private static func heading(_ text: String, _ sel: NSRange, _ level: Int) -> Result {
        let marker = String(repeating: "#", count: level) + " "
        return mapLines(text, sel) { lines in
            let allAtLevel = lines.allSatisfy { $0.hasPrefix(marker) }
            return lines.map { line in
                let plain = stripHeading(line)
                return allAtLevel ? plain : marker + plain
            }
        }
    }

    private static func stripHeading(_ line: String) -> String {
        var rest = Substring(line)
        let hashes = rest.prefix(while: { $0 == "#" })
        guard !hashes.isEmpty, hashes.count <= 6 else { return line }
        rest = rest.dropFirst(hashes.count)
        guard rest.first == " " else { return line }
        return String(rest.dropFirst())
    }

    /// Puts a prefix (for each line, by its index) on the lines, or takes them
    /// all off when every line has one. A line's other list prefix is replaced.
    private static func prefixLines(_ text: String, _ sel: NSRange, prefix: @escaping (Int) -> String) -> Result {
        mapLines(text, sel) { lines in
            // Any number for a numbered list, a checked box for a checklist.
            let pattern: String? = switch prefix(0) {
            case "1. ": #"^\d+\. "#
            case "- [ ] ": #"^- \[[ xX]\] "#
            default: nil
            }
            let has: (String, Int) -> Bool = { line, i in
                pattern.map { line.range(of: $0, options: .regularExpression) != nil } ?? line.hasPrefix(prefix(i))
            }
            let all = lines.enumerated().allSatisfy { has($1, $0) || $1.isEmpty }
                && lines.contains { !$0.isEmpty }
            return lines.enumerated().map { i, line in
                let plain = stripListPrefix(line)
                if all { return plain }
                return line.isEmpty && lines.count > 1 ? line : prefix(i) + plain
            }
        }
    }

    private static func stripListPrefix(_ line: String) -> String {
        for pattern in [#"^- \[[ xX]\] "#, #"^[-*+] "#, #"^\d+\. "#, #"^> ?"#] {
            if let r = line.range(of: pattern, options: .regularExpression) {
                return String(line[r.upperBound...])
            }
        }
        return line
    }

    /// Replaces the selection's lines with transform's, and selects them.
    private static func mapLines(_ text: String, _ sel: NSRange, _ transform: ([String]) -> [String]) -> Result {
        let ns = text as NSString
        let range = lineRange(ns, sel)
        let lines = ns.substring(with: range).components(separatedBy: "\n")
        let replaced = transform(lines).joined(separator: "\n")
        let out = ns.replacingCharacters(in: range, with: replaced)
        let length = (replaced as NSString).length
        // A cursor on one line stays at the end of that line.
        let selection = sel.length == 0 && lines.count == 1
            ? NSRange(location: range.location + length, length: 0)
            : NSRange(location: range.location, length: length)
        return Result(text: out, selection: selection)
    }

    /// Fences the selected lines as code, or takes the fences off.
    private static func codeBlock(_ text: String, _ sel: NSRange) -> Result {
        let ns = text as NSString
        let range = lineRange(ns, sel)
        let body = ns.substring(with: range)
        if body.hasPrefix("```"), body.hasSuffix("```"), body.count >= 6 {
            var lines = body.components(separatedBy: "\n")
            lines.removeFirst()
            lines.removeLast()
            let inner = lines.joined(separator: "\n")
            return Result(text: ns.replacingCharacters(in: range, with: inner), selection: NSRange(location: range.location, length: (inner as NSString).length))
        }
        let fenced = "```\n" + body + "\n```"
        let cursor = NSRange(location: range.location + 4, length: (body as NSString).length)
        return Result(text: ns.replacingCharacters(in: range, with: fenced), selection: cursor)
    }

    /// Enter in a list continues it: the next line gets the same kind of item. On
    /// an empty item, it ends the list instead. nil: Enter does what it always does.
    static func newline(in text: String, at cursor: Int) -> Result? {
        let ns = text as NSString
        let line = ns.lineRange(for: NSRange(location: cursor, length: 0))
        let start = line.location
        let before = ns.substring(with: NSRange(location: start, length: cursor - start))
        let patterns = [#"^\s*- \[[ xX]\] "#, #"^\s*[-*+] "#, #"^\s*\d+\. "#, #"^\s*> "#]
        for pattern in patterns {
            guard let match = before.range(of: pattern, options: .regularExpression) else { continue }
            let marker = String(before[match])
            let rest = before[match.upperBound...]
            if rest.trimmingCharacters(in: .whitespaces).isEmpty {
                // An empty item ends the list: take its marker off.
                let out = ns.replacingCharacters(in: NSRange(location: start, length: cursor - start), with: "")
                return Result(text: out, selection: NSRange(location: start, length: 0))
            }
            let next = nextMarker(marker)
            let out = ns.replacingCharacters(in: NSRange(location: cursor, length: 0), with: "\n" + next)
            return Result(text: out, selection: NSRange(location: cursor + 1 + (next as NSString).length, length: 0))
        }
        return nil
    }

    /// The marker for the item after one: the next number, an unchecked box, the same bullet.
    private static func nextMarker(_ marker: String) -> String {
        if let r = marker.range(of: #"\d+"#, options: .regularExpression), let n = Int(marker[r]) {
            return marker.replacingCharacters(in: r, with: String(n + 1))
        }
        if marker.contains("[") {
            return marker.replacingOccurrences(of: "[x]", with: "[ ]").replacingOccurrences(of: "[X]", with: "[ ]")
        }
        return marker
    }
}

/// How the editor styles Markdown as you type: what each stretch of text is.
enum MarkdownStyle: Equatable {
    case heading(Int)
    case bold, italic, strikethrough, code, link
    case codeBlock, quote, listMarker
    /// The syntax itself (**, #, `), drawn faintly.
    case syntax
}

struct MarkdownSpan: Equatable {
    let range: NSRange
    let style: MarkdownStyle
}

enum MarkdownHighlighter {
    /// The spans to style in text, in UTF-16 ranges. Later spans go over earlier
    /// ones where they overlap.
    static func spans(_ text: String) -> [MarkdownSpan] {
        let ns = text as NSString
        var spans: [MarkdownSpan] = []
        var inFence = false
        var location = 0
        for line in text.components(separatedBy: "\n") {
            let length = (line as NSString).length
            let range = NSRange(location: location, length: length)
            defer { location += length + 1 }
            if line.hasPrefix("```") {
                spans.append(MarkdownSpan(range: range, style: .codeBlock))
                spans.append(MarkdownSpan(range: range, style: .syntax))
                inFence.toggle()
                continue
            }
            if inFence {
                spans.append(MarkdownSpan(range: range, style: .codeBlock))
                continue
            }
            if let level = headingLevel(line) {
                spans.append(MarkdownSpan(range: range, style: .heading(level)))
                spans.append(MarkdownSpan(range: NSRange(location: location, length: level + 1), style: .syntax))
                spans += inline(ns, range)
                continue
            }
            if let marker = line.range(of: #"^\s*(- \[[ xX]\]|[-*+]|\d+\.) "#, options: .regularExpression) {
                let n = (String(line[marker]) as NSString).length
                spans.append(MarkdownSpan(range: NSRange(location: location, length: n), style: .listMarker))
            } else if line.hasPrefix(">") {
                spans.append(MarkdownSpan(range: range, style: .quote))
                spans.append(MarkdownSpan(range: NSRange(location: location, length: 1), style: .syntax))
            }
            spans += inline(ns, range)
        }
        return spans
    }

    private static func headingLevel(_ line: String) -> Int? {
        let hashes = line.prefix(while: { $0 == "#" }).count
        guard (1...6).contains(hashes), line.dropFirst(hashes).first == " " else { return nil }
        return hashes
    }

    /// Bold, italic, strikethrough, code and links within one line.
    private static func inline(_ ns: NSString, _ line: NSRange) -> [MarkdownSpan] {
        var spans: [MarkdownSpan] = []
        let text = ns.substring(with: line)
        var code: [NSRange] = []
        let rules: [(String, MarkdownStyle, Int)] = [
            (#"`[^`\n]+`"#, .code, 1),
            (#"\*\*[^*\n]+\*\*"#, .bold, 2),
            (#"__[^_\n]+__"#, .bold, 2),
            (#"(?<![*\w])\*[^*\n]+\*(?!\*)"#, .italic, 1),
            (#"(?<![_\w])_[^_\n]+_(?![_\w])"#, .italic, 1),
            (#"~~[^~\n]+~~"#, .strikethrough, 2),
        ]
        for (pattern, style, marker) in rules {
            guard let regex = try? NSRegularExpression(pattern: pattern) else { continue }
            for match in regex.matches(in: text, range: NSRange(location: 0, length: (text as NSString).length)) {
                let r = NSRange(location: line.location + match.range.location, length: match.range.length)
                if style != .code, code.contains(where: { NSIntersectionRange($0, r).length > 0 }) { continue }
                if style == .code { code.append(r) }
                spans.append(MarkdownSpan(range: r, style: style))
                spans.append(MarkdownSpan(range: NSRange(location: r.location, length: marker), style: .syntax))
                spans.append(MarkdownSpan(range: NSRange(location: NSMaxRange(r) - marker, length: marker), style: .syntax))
            }
        }
        if let regex = try? NSRegularExpression(pattern: #"\[([^\]\n]+)\]\(([^)\s]+)\)"#) {
            for match in regex.matches(in: text, range: NSRange(location: 0, length: (text as NSString).length)) {
                let r = NSRange(location: line.location + match.range.location, length: match.range.length)
                if code.contains(where: { NSIntersectionRange($0, r).length > 0 }) { continue }
                let label = match.range(at: 1)
                spans.append(MarkdownSpan(range: NSRange(location: line.location + label.location, length: label.length), style: .link))
                spans.append(MarkdownSpan(range: NSRange(location: r.location, length: 1), style: .syntax))
                let urlPart = NSRange(location: line.location + NSMaxRange(label), length: NSMaxRange(r) - (line.location + NSMaxRange(label)))
                spans.append(MarkdownSpan(range: urlPart, style: .syntax))
            }
        }
        return spans
    }
}
