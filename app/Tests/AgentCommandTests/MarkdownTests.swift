import Foundation
import Testing

@testable import AgentCommand

private func plain(_ blocks: [MarkdownBlock]) -> [String] { blocks.map(\.plainText) }

/// A list item as depth, marker and text (the parser adds attributes of its own).
private func item(_ block: MarkdownBlock) -> String {
    guard case .listItem(let depth, let marker, let text) = block else { return "not a list item: \(block)" }
    return "\(depth) \(marker) \(String(text.characters))"
}

@Test func markdownBlocks() {
    let blocks = MarkdownBlock.parse("""
    # Title
    Some **bold** and `code`
    on two lines.

    - one
    - two
      1. nested

    > quoted

    ```go
    x := 1
    ```

    ---
    end
    """)
    #expect(blocks.count == 9)
    guard blocks.count == 9 else { return }
    if case .heading(1, let t) = blocks[0] { #expect(String(t.characters) == "Title") } else { Issue.record("heading: \(blocks[0])") }
    #expect(blocks[1].plainText == "Some bold and code on two lines.")
    #expect(blocks[2...4].map(item) == ["1 • one", "1 • two", "2 1. nested"])
    #expect(blocks[5] == .quote(AttributedString("quoted")))
    #expect(blocks[6] == .code(language: "go", text: "x := 1"))
    #expect(blocks[7] == .rule)
    #expect(blocks[8].plainText == "end")
}

@Test func markdownKeepsInlineStyles() {
    guard case .paragraph(let text) = MarkdownBlock.parse("a **b** `c`").first else {
        Issue.record("not a paragraph")
        return
    }
    let styles = text.runs.compactMap(\.inlinePresentationIntent)
    #expect(styles.contains(.stronglyEmphasized) && styles.contains(.code))
    #expect(text.runs.allSatisfy { $0.presentationIntent == nil }, "the block's intent is the view's job")
}

@Test func markdownTables() {
    let blocks = MarkdownBlock.parse("| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\nafter")
    #expect(blocks.count == 2)
    guard case .table(let header, let rows) = blocks.first else {
        Issue.record("not a table: \(blocks)")
        return
    }
    #expect(header.map { String($0.characters) } == ["a", "b"])
    #expect(rows.map { $0.map { String($0.characters) } } == [["1", "2"], ["3", "4"]])
    #expect(plain(blocks) == ["a | b", "after"])
}

@Test func markdownListItemsWithSeveralParagraphs() {
    let blocks = MarkdownBlock.parse("1. first\n\n   more of it\n2. second")
    #expect(blocks.map(item) == ["1 1. first", "1  more of it", "1 2. second"])
}

@Test func aQuestionsOptionsAreAList() {
    let blocks = MarkdownBlock.parse("Which one?\n\n1. Red: warm\n\n2. Blue")
    #expect(plain(blocks) == ["Which one?", "Red: warm", "Blue"])
    if case .listItem(_, let marker, _) = blocks.last { #expect(marker == "2.") } else { Issue.record("not a list") }
}

@Test func emptyMarkdownHasNoBlocks() {
    #expect(MarkdownBlock.parse("").isEmpty)
}
