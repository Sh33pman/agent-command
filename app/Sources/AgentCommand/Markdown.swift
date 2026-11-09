import Foundation
import SwiftUI

/// One block of a Markdown message, as the popup and the Inbox show it. Inline
/// styles (bold, italics, code, links) stay in the attributed text.
enum MarkdownBlock: Equatable {
    case heading(level: Int, text: AttributedString)
    case paragraph(AttributedString)
    /// marker is "•", "◦" or "3." and is empty for a list item's later paragraphs.
    case listItem(depth: Int, marker: String, text: AttributedString)
    case quote(AttributedString)
    case code(language: String?, text: String)
    case table(header: [AttributedString], rows: [[AttributedString]])
    case rule

    /// Splits Markdown into blocks with Foundation's parser. Text it can't parse is
    /// one paragraph, as it is.
    static func parse(_ markdown: String) -> [MarkdownBlock] {
        guard let doc = try? AttributedString(markdown: markdown, options: .init(interpretedSyntax: .full)) else {
            return markdown.isEmpty ? [] : [.paragraph(AttributedString(markdown))]
        }
        var blocks: [MarkdownBlock] = []
        var seenItems = Set<Int>()
        var table: (id: Int, header: [AttributedString], rows: [Int: [AttributedString]], order: [Int])?

        func flushTable() {
            if let t = table {
                blocks.append(.table(header: t.header, rows: t.order.compactMap { t.rows[$0] }))
            }
            table = nil
        }

        for (intent, range) in doc.runs[\.presentationIntent] {
            var text = AttributedString(doc[range])
            text.presentationIntent = nil
            let components = intent?.components ?? []
            // A table's cells come one by one: gather them into rows.
            if let tableComponent = components.first(where: { if case .table = $0.kind { true } else { false } }) {
                if table?.id != tableComponent.identity {
                    flushTable()
                    table = (tableComponent.identity, [], [:], [])
                }
                if components.contains(where: { $0.kind == .tableHeaderRow }) {
                    table?.header.append(text)
                } else if let row = components.first(where: { if case .tableRow = $0.kind { true } else { false } }) {
                    if table?.rows[row.identity] == nil { table?.order.append(row.identity) }
                    table?.rows[row.identity, default: []].append(text)
                }
                continue
            }
            flushTable()
            guard let first = components.first else {
                blocks.append(.paragraph(text))
                continue
            }
            switch first.kind {
            case .header(let level):
                blocks.append(.heading(level: level, text: text))
            case .codeBlock(let language):
                var code = String(text.characters)
                if code.hasSuffix("\n") { code.removeLast() }
                blocks.append(.code(language: language, text: code))
            case .thematicBreak:
                blocks.append(.rule)
            default:
                let items = components.filter { if case .listItem = $0.kind { true } else { false } }
                if let item = items.first {
                    var marker = ""
                    if seenItems.insert(item.identity).inserted {
                        marker = listMarker(components, depth: items.count)
                    }
                    blocks.append(.listItem(depth: items.count, marker: marker, text: text))
                } else if components.contains(where: { $0.kind == .blockQuote }) {
                    blocks.append(.quote(text))
                } else {
                    blocks.append(.paragraph(text))
                }
            }
        }
        flushTable()
        return blocks
    }

    /// The marker of the innermost list item: its number, or a bullet by depth.
    private static func listMarker(_ components: [PresentationIntent.IntentType], depth: Int) -> String {
        guard let index = components.firstIndex(where: { if case .listItem = $0.kind { true } else { false } }),
              case .listItem(let ordinal) = components[index].kind else { return "•" }
        let ordered = components[(index + 1)...].first {
            $0.kind == .orderedList || $0.kind == .unorderedList
        }?.kind == .orderedList
        return ordered ? "\(ordinal)." : (depth % 2 == 1 ? "•" : "◦")
    }

    /// The text without Markdown, for a one-line preview.
    var plainText: String {
        switch self {
        case .heading(_, let text), .paragraph(let text), .listItem(_, _, let text), .quote(let text):
            String(text.characters)
        case .code(_, let text):
            text
        case .table(let header, _):
            header.map { String($0.characters) }.joined(separator: " | ")
        case .rule:
            ""
        }
    }
}

/// Shows a Markdown message: headings, lists, quotes, code blocks and tables.
struct MarkdownView: View {
    let blocks: [MarkdownBlock]
    /// dim is for list markers and quotes, panel is behind code blocks, and code
    /// colors inline code (the app's font is monospaced, so that alone wouldn't show it).
    let dim, panel, code: Color

    init(_ markdown: String, dim: Color, panel: Color, code: Color) {
        blocks = MarkdownBlock.parse(markdown)
        self.dim = dim
        self.panel = panel
        self.code = code
    }

    /// Text with its inline code colored.
    private func styled(_ text: AttributedString) -> AttributedString {
        var text = text
        for run in text.runs where run.inlinePresentationIntent?.contains(.code) == true {
            text[run.range].foregroundColor = code
        }
        return text
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, block in
                view(block)
            }
        }
        .textSelection(.enabled)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    @ViewBuilder private func view(_ block: MarkdownBlock) -> some View {
        switch block {
        case .heading(let level, let text):
            Text(styled(text))
                .font(AppFont.ui(level == 1 ? 18 : level == 2 ? 16 : 14, weight: .heavy))
                .padding(.top, 4)
        case .paragraph(let text):
            Text(styled(text)).fixedSize(horizontal: false, vertical: true)
        case .listItem(let depth, let marker, let text):
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(marker).foregroundStyle(dim).frame(minWidth: 14, alignment: .trailing)
                Text(styled(text)).fixedSize(horizontal: false, vertical: true)
            }
            .padding(.leading, CGFloat(depth - 1) * 18)
        case .quote(let text):
            Text(styled(text))
                .foregroundStyle(dim)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.leading, 10)
                .overlay(alignment: .leading) { Rectangle().fill(dim).frame(width: 2) }
        case .code(_, let text):
            // Long lines wrap: the message already scrolls, and a second scroll inside it is fiddly.
            Text(text)
                .font(AppFont.ui(12))
                .fixedSize(horizontal: false, vertical: true)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
            .background(panel)
            .clipShape(RoundedRectangle(cornerRadius: 4))
        case .table(let header, let rows):
            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 4) {
                GridRow { ForEach(Array(header.enumerated()), id: \.offset) { Text(styled($1)).bold() } }
                Divider()
                ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                    GridRow { ForEach(Array(row.enumerated()), id: \.offset) { Text(styled($1)) } }
                }
            }
        case .rule:
            Divider()
        }
    }
}
