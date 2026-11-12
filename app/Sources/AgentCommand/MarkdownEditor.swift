import AppKit
import SwiftUI

/// A Markdown editor: the text stays Markdown, styled as you type (headings,
/// bold, italic, code, lists, quotes, links), with a toolbar, shortcuts, and
/// lists that carry on when you press Enter. The preview shows it rendered.
struct MarkdownEditor: View {
    @Binding var text: String
    var placeholder = ""
    @State private var preview = false
    @State private var command: (MarkdownCommand, UUID)?

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            ZStack(alignment: .topLeading) {
                if preview {
                    ScrollView {
                        MarkdownView(text.isEmpty ? "_Nothing to preview._" : text,
                                     dim: theme.dim, panel: Color(nsColor: theme.line), code: theme.yellow)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(10)
                    }
                } else {
                    MarkdownTextView(text: $text, command: command)
                    if text.isEmpty {
                        Text(placeholder)
                            .foregroundStyle(theme.dim)
                            .padding(.horizontal, 13)
                            .padding(.vertical, 8)
                            .allowsHitTesting(false)
                    }
                }
            }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 2) {
            group([.bold, .italic, .strikethrough, .code, .link])
            divider
            group([.heading1, .heading2, .heading3])
            divider
            group([.bulletList, .numberedList, .taskList, .quote, .codeBlock])
            Spacer()
            Picker("", selection: $preview) {
                Text("Write").tag(false)
                Text("Preview").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help("Show the prompt rendered")
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 4)
    }

    private var divider: some View {
        Rectangle().fill(Color(nsColor: theme.line)).frame(width: 1, height: 14).padding(.horizontal, 4)
    }

    private func group(_ commands: [MarkdownCommand]) -> some View {
        ForEach(commands, id: \.self) { c in
            Button {
                preview = false
                command = (c, UUID())
            } label: {
                if let text = c.label {
                    Text(text).font(.system(size: 11, weight: .bold)).frame(width: 22, height: 20)
                } else {
                    Image(systemName: c.symbol).frame(width: 22, height: 20)
                }
            }
            .buttonStyle(.borderless)
            .help("\(c.title) (\(c.shortcut))")
            .disabled(preview)
        }
    }
}

extension MarkdownCommand: Hashable {}

/// The text view under MarkdownEditor.
private struct MarkdownTextView: NSViewRepresentable {
    @Binding var text: String
    /// A toolbar press: applied once per UUID.
    let command: (MarkdownCommand, UUID)?

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        let view = MarkdownNSTextView()
        view.isRichText = false
        view.allowsUndo = true
        view.isAutomaticQuoteSubstitutionEnabled = false
        view.isAutomaticDashSubstitutionEnabled = false
        view.isAutomaticTextReplacementEnabled = false
        view.isAutomaticSpellingCorrectionEnabled = false
        view.drawsBackground = false
        view.textContainerInset = NSSize(width: 8, height: 8)
        view.isVerticallyResizable = true
        view.isHorizontallyResizable = false
        view.autoresizingMask = [.width]
        view.textContainer?.widthTracksTextView = true
        view.delegate = context.coordinator
        view.onCommand = { [weak coordinator = context.coordinator] c in coordinator?.apply(c) }
        view.onNewline = { [weak coordinator = context.coordinator] in coordinator?.newline() ?? false }
        scroll.documentView = view
        context.coordinator.view = view
        view.string = text
        context.coordinator.restyle()
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        guard let view = coordinator.view else { return }
        if view.string != text {
            view.string = text
            coordinator.restyle()
        }
        if let (c, token) = command, token != coordinator.lastCommand {
            coordinator.lastCommand = token
            view.window?.makeFirstResponder(view)
            coordinator.apply(c)
        }
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: MarkdownTextView
        weak var view: MarkdownNSTextView?
        var lastCommand: UUID?

        init(_ parent: MarkdownTextView) { self.parent = parent }

        func textDidChange(_ notification: Notification) {
            guard let view else { return }
            parent.text = view.string
            restyle()
        }

        /// Applies a command as one undoable change.
        func apply(_ command: MarkdownCommand) {
            guard let view else { return }
            replace(with: MarkdownEdit.apply(command, to: view.string, selection: view.selectedRange()))
        }

        /// Enter in a list: carry it on. Returns whether it did anything.
        func newline() -> Bool {
            guard let view, view.selectedRange().length == 0,
                  let result = MarkdownEdit.newline(in: view.string, at: view.selectedRange().location)
            else { return false }
            replace(with: result)
            return true
        }

        private func replace(with result: MarkdownEdit.Result) {
            guard let view else { return }
            let all = NSRange(location: 0, length: (view.string as NSString).length)
            guard view.shouldChangeText(in: all, replacementString: result.text) else { return }
            view.textStorage?.replaceCharacters(in: all, with: result.text)
            view.didChangeText()
            view.setSelectedRange(result.selection)
            view.scrollRangeToVisible(result.selection)
        }

        /// Styles the whole text from its Markdown. Prompts are short, so it's quick.
        func restyle() {
            guard let view, let storage = view.textStorage else { return }
            let theme = AppTheme.shared
            let size: CGFloat = 13
            let base = AppFont.appKit(size)
            let mono = NSFont.monospacedSystemFont(ofSize: size - 1, weight: .regular)
            let all = NSRange(location: 0, length: storage.length)
            let paragraph = NSMutableParagraphStyle()
            paragraph.lineSpacing = 2
            storage.beginEditing()
            storage.setAttributes([.font: base, .foregroundColor: theme.foreground, .paragraphStyle: paragraph], range: all)
            for span in MarkdownHighlighter.spans(view.string) where NSMaxRange(span.range) <= storage.length {
                switch span.style {
                case .heading(let level):
                    let sizes: [CGFloat] = [20, 17, 15, 14, 13, 13]
                    storage.addAttribute(.font, value: NSFont.boldSystemFont(ofSize: sizes[min(level, 6) - 1]), range: span.range)
                case .bold:
                    storage.addAttribute(.font, value: NSFontManager.shared.convert(font(storage, span.range) ?? base, toHaveTrait: .boldFontMask), range: span.range)
                case .italic:
                    storage.addAttribute(.font, value: NSFontManager.shared.convert(font(storage, span.range) ?? base, toHaveTrait: .italicFontMask), range: span.range)
                case .strikethrough:
                    storage.addAttribute(.strikethroughStyle, value: NSUnderlineStyle.single.rawValue, range: span.range)
                case .code, .codeBlock:
                    storage.addAttribute(.font, value: mono, range: span.range)
                    storage.addAttribute(.foregroundColor, value: NSColor(theme.yellow), range: span.range)
                case .link:
                    storage.addAttribute(.foregroundColor, value: NSColor(theme.blue), range: span.range)
                    storage.addAttribute(.underlineStyle, value: NSUnderlineStyle.single.rawValue, range: span.range)
                case .quote:
                    storage.addAttribute(.foregroundColor, value: NSColor(theme.dim), range: span.range)
                case .listMarker:
                    storage.addAttribute(.foregroundColor, value: NSColor(theme.magenta), range: span.range)
                case .syntax:
                    storage.addAttribute(.foregroundColor, value: NSColor(theme.dim), range: span.range)
                }
            }
            storage.endEditing()
            view.typingAttributes = [.font: base, .foregroundColor: theme.foreground, .paragraphStyle: paragraph]
            view.insertionPointColor = theme.foreground
        }

        private func font(_ storage: NSTextStorage, _ range: NSRange) -> NSFont? {
            storage.attribute(.font, at: range.location, effectiveRange: nil) as? NSFont
        }
    }
}

/// Takes the editor's shortcuts before the menus do, and Enter in a list.
final class MarkdownNSTextView: NSTextView {
    var onCommand: ((MarkdownCommand) -> Void)?
    var onNewline: (() -> Bool)?

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        guard window?.firstResponder === self else { return super.performKeyEquivalent(with: event) }
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        if let command = MarkdownCommand.forKey(
            event.charactersIgnoringModifiers ?? "", command: flags.contains(.command),
            shift: flags.contains(.shift), option: flags.contains(.option)) {
            onCommand?(command)
            return true
        }
        return super.performKeyEquivalent(with: event)
    }

    override func insertNewline(_ sender: Any?) {
        if onNewline?() == true { return }
        super.insertNewline(sender)
    }
}
