import SwiftUI

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
