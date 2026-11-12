import Foundation
import Testing

@testable import AgentCommand

/// Applies a command to text where | marks the cursor, or [ and ] the selection,
/// and returns the result marked the same way.
private func edit(_ command: MarkdownCommand, _ marked: String) -> String {
    let (text, selection) = unmark(marked)
    let result = MarkdownEdit.apply(command, to: text, selection: selection)
    return mark(result.text, result.selection)
}

private func unmark(_ marked: String) -> (String, NSRange) {
    let ns = marked as NSString
    if ns.range(of: "|").location != NSNotFound {
        let at = ns.range(of: "|").location
        return (ns.replacingCharacters(in: NSRange(location: at, length: 1), with: ""), NSRange(location: at, length: 0))
    }
    let start = ns.range(of: "[[").location
    let withoutStart = ns.replacingCharacters(in: NSRange(location: start, length: 2), with: "") as NSString
    let end = withoutStart.range(of: "]]").location
    let text = withoutStart.replacingCharacters(in: NSRange(location: end, length: 2), with: "")
    return (text, NSRange(location: start, length: end - start))
}

private func mark(_ text: String, _ selection: NSRange) -> String {
    let ns = text as NSString
    if selection.length == 0 { return ns.replacingCharacters(in: selection, with: "|") }
    let selected = ns.substring(with: selection)
    return ns.replacingCharacters(in: selection, with: "[[" + selected + "]]")
}

@Test func wrapsAndUnwraps() {
    #expect(edit(.bold, "a [[word]] b") == "a **[[word]]** b")
    #expect(edit(.bold, "a **[[word]]** b") == "a [[word]] b", "selected between its markers")
    #expect(edit(.bold, "a [[**word**]] b") == "a [[word]] b", "selected with its markers")
    #expect(edit(.bold, "a |") == "a **|**")
    #expect(edit(.italic, "[[x]]") == "*[[x]]*")
    #expect(edit(.italic, "*[[x]]*") == "[[x]]")
    #expect(edit(.italic, "**[[x]]**") == "***[[x]]***", "italic on bold adds, not takes a star off")
    #expect(edit(.italic, "[[**x**]]") == "*[[**x**]]*")
    #expect(edit(.code, "run [[make ci]]") == "run `[[make ci]]`")
    #expect(edit(.strikethrough, "[[old]]") == "~~[[old]]~~")
}

@Test func links() {
    #expect(edit(.link, "see [[the docs]]") == "see [the docs]([[url]])")
    #expect(edit(.link, "see |") == "see [[[text]]](url)")
}

@Test func headings() {
    #expect(edit(.heading1, "Title|") == "# Title|")
    #expect(edit(.heading2, "# Title|") == "## Title|", "changes level")
    #expect(edit(.heading2, "## Title|") == "Title|", "off again")
    #expect(edit(.heading3, "a\n[[b\nc]]\nd") == "a\n[[### b\n### c]]\nd")
    #expect(edit(.heading1, "#hashtag|") == "# #hashtag|", "not a heading without its space")
}

@Test func lists() {
    #expect(edit(.bulletList, "one|") == "- one|")
    #expect(edit(.bulletList, "- one|") == "one|")
    #expect(edit(.numberedList, "[[a\nb\nc]]") == "[[1. a\n2. b\n3. c]]")
    #expect(edit(.numberedList, "[[1. a\n2. b]]") == "[[a\nb]]")
    #expect(edit(.bulletList, "[[1. a\n2. b]]") == "[[- a\n- b]]", "one kind of list to another")
    #expect(edit(.taskList, "buy milk|") == "- [ ] buy milk|")
    #expect(edit(.taskList, "- [x] done|") == "done|")
    #expect(edit(.quote, "[[a\nb]]") == "[[> a\n> b]]")
    #expect(edit(.quote, "> a|") == "a|")
    #expect(edit(.bulletList, "[[a\n\nb]]") == "[[- a\n\n- b]]", "a blank line stays blank")
    #expect(edit(.bulletList, "|") == "- |")
    #expect(edit(.bulletList, "a\n|b") == "a\n- b|")
    #expect(edit(.bulletList, "[[a\n]]b") == "[[- a]]\nb", "a selection ending at a line's start")
}

@Test func codeBlocks() {
    #expect(edit(.codeBlock, "[[let x = 1]]") == "```\n[[let x = 1]]\n```")
    #expect(edit(.codeBlock, "[[```\nlet x = 1\n```]]") == "[[let x = 1]]")
}

@Test func enterCarriesListsOn() throws {
    func enter(_ marked: String) -> String? {
        let (text, selection) = unmark(marked)
        return MarkdownEdit.newline(in: text, at: selection.location).map { mark($0.text, $0.selection) }
    }
    #expect(enter("- one|") == "- one\n- |")
    #expect(enter("  * one|") == "  * one\n  * |")
    #expect(enter("9. nine|") == "9. nine\n10. |")
    #expect(enter("- [x] done|") == "- [x] done\n- [ ] |")
    #expect(enter("> quoted|") == "> quoted\n> |")
    #expect(enter("a\n- |") == "a\n|", "an empty item ends the list")
    #expect(enter("plain|") == nil)
    #expect(enter("- one|\n- two") == "- one\n- |\n- two")
}

@Test func commandsHaveKeysAndNames() {
    for command in MarkdownCommand.allCases {
        #expect(!command.title.isEmpty && !command.symbol.isEmpty && !command.shortcut.isEmpty)
    }
    #expect(MarkdownCommand.forKey("b", command: true, shift: false, option: false) == .bold)
    #expect(MarkdownCommand.forKey("B", command: true, shift: false, option: false) == .bold)
    #expect(MarkdownCommand.forKey("b", command: false, shift: false, option: false) == nil)
    #expect(MarkdownCommand.forKey("i", command: true, shift: false, option: false) == .italic)
    #expect(MarkdownCommand.forKey("x", command: true, shift: true, option: false) == .strikethrough)
    #expect(MarkdownCommand.forKey("e", command: true, shift: false, option: false) == .code)
    #expect(MarkdownCommand.forKey("k", command: true, shift: false, option: false) == .link)
    #expect(MarkdownCommand.forKey("1", command: true, shift: false, option: true) == .heading1)
    #expect(MarkdownCommand.forKey("2", command: true, shift: false, option: true) == .heading2)
    #expect(MarkdownCommand.forKey("3", command: true, shift: false, option: true) == .heading3)
    #expect(MarkdownCommand.forKey("1", command: true, shift: false, option: false) == nil, "⌘1 shows a session")
    #expect(MarkdownCommand.forKey("8", command: true, shift: true, option: false) == .bulletList)
    #expect(MarkdownCommand.forKey("7", command: true, shift: true, option: false) == .numberedList)
    #expect(MarkdownCommand.forKey("9", command: true, shift: true, option: false) == .taskList)
    #expect(MarkdownCommand.forKey(">", command: true, shift: true, option: false) == .quote)
    #expect(MarkdownCommand.forKey("c", command: true, shift: false, option: true) == .codeBlock)
    #expect(MarkdownCommand.forKey("c", command: true, shift: false, option: false) == nil, "⌘C copies")
}

@Test func highlightsAsYouType() {
    let text = "# Title\nSome **bold**, *italic*, ~~gone~~ and `code **not bold**`.\n- item\n1. first\n> quote\n[docs](https://x.y)\n```\nlet **x**\n```"
    let spans = MarkdownHighlighter.spans(text)
    let ns = text as NSString
    func styled(_ style: MarkdownStyle) -> [String] {
        spans.filter { $0.style == style }.map { ns.substring(with: $0.range) }
    }
    #expect(styled(.heading(1)) == ["# Title"])
    #expect(styled(.bold) == ["**bold**"], "not inside code")
    #expect(styled(.italic) == ["*italic*"])
    #expect(styled(.strikethrough) == ["~~gone~~"])
    #expect(styled(.code) == ["`code **not bold**`"])
    #expect(styled(.listMarker) == ["- ", "1. "])
    #expect(styled(.quote) == ["> quote"])
    #expect(styled(.link) == ["docs"])
    #expect(styled(.codeBlock) == ["```", "let **x**", "```"], "nothing styled inside a fence")
    #expect(styled(.syntax).contains("# ") && styled(.syntax).contains("**") && styled(.syntax).contains("](https://x.y)"))
    #expect(MarkdownHighlighter.spans("").isEmpty)
    #expect(MarkdownHighlighter.spans("####### seven").filter { if case .heading = $0.style { true } else { false } }.isEmpty)
}
