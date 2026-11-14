import AppKit
import SwiftUI

/// One thing the command popup can do.
struct PopupCommand: Identifiable {
    let section: String
    let title: String
    var shortcut = ""
    /// Tells apart commands whose section and title are the same.
    var key = ""
    let action: () -> Void

    var id: String { section + "/" + title + "/" + key }

    func matches(_ query: String) -> Bool {
        title.localizedCaseInsensitiveContains(query) || section.localizedCaseInsensitiveContains(query)
    }
}

/// The sizes popups share. Their colors come from the theme.
private enum PopupStyle {
    static let rowHeight: CGFloat = 19
}

/// The frame every popup shares: a flat box centered over the whole window,
/// with a title, a search line and a list, like a full-screen terminal program.
struct PopupBox<Content: View>: View {
    let title: String
    let placeholder: String
    var dimsBackground = true
    /// Whether there's a list to move through, so ⌃J and ⌃K move rather than edit.
    var hasList = true
    @Binding var search: String
    let close: () -> Void
    let onMove: (Int) -> Void
    let onSubmit: () -> Void
    @ViewBuilder let content: () -> Content

    private var theme: AppTheme { .shared }

    var body: some View {
        ZStack {
            (dimsBackground ? theme.backdrop : Color.clear)
                .contentShape(Rectangle())
                .onTapGesture(perform: close)
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text(title).fontWeight(.bold).foregroundStyle(theme.fg)
                    Spacer()
                    Text("esc").foregroundStyle(theme.dim)
                }
                .padding(.horizontal, 24)
                .padding(.bottom, PopupStyle.rowHeight)

                PopupSearchField(
                    text: $search,
                    placeholder: placeholder,
                    hasList: hasList,
                    onMove: onMove,
                    onSubmit: onSubmit,
                    onCancel: close
                )
                .frame(height: PopupStyle.rowHeight)
                .padding(.horizontal, 24)
                .padding(.bottom, PopupStyle.rowHeight)

                content()
            }
            .font(AppFont.ui(13))
            .padding(.vertical, 18)
            .frame(maxWidth: 600)
            .background(theme.bg)
            // Without dimming behind it, the box needs an edge to stand out.
            .overlay {
                if !dimsBackground {
                    Rectangle().strokeBorder(Color(nsColor: theme.line), lineWidth: 1)
                }
            }
            .padding(32)
        }
    }
}

/// A searchable list of commands.
struct CommandPopupView: View {
    let commands: [PopupCommand]
    let close: () -> Void
    let run: (PopupCommand) -> Void

    @State private var search = ""
    @State private var selected = 0

    private var theme: AppTheme { .shared }

    private var matches: [PopupCommand] {
        let query = search.trimmingCharacters(in: .whitespaces)
        return query.isEmpty ? commands : commands.filter { $0.matches(query) }
    }

    var body: some View {
        let list = matches
        PopupBox(
            title: "Commands",
            placeholder: "Search",
            search: $search,
            close: close,
            onMove: { move($0, in: list.count) },
            onSubmit: {
                if list.indices.contains(selected) { run(list[selected]) }
            }
        ) {
            rows(list)
        }
        .onChange(of: search) { selected = 0 }
    }

    private func rows(_ list: [PopupCommand]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if list.isEmpty {
                        Text("No matching commands")
                            .foregroundStyle(theme.dim)
                            .padding(.horizontal, 24)
                    }
                    ForEach(list) { command in
                        let index = list.firstIndex { $0.id == command.id } ?? 0
                        VStack(alignment: .leading, spacing: 0) {
                            if index == 0 || list[index - 1].section != command.section {
                                sectionHeader(command.section, first: index == 0)
                            }
                            row(command, isSelected: index == selected)
                                .contentShape(Rectangle())
                                .onHover { if $0 { selected = index } }
                                .onTapGesture { run(command) }
                        }
                    }
                }
            }
            .frame(maxHeight: 620)
            .fixedSize(horizontal: false, vertical: true)
            .onChange(of: selected) {
                if list.indices.contains(selected) { proxy.scrollTo(list[selected].id) }
            }
        }
    }

    private func sectionHeader(_ title: String, first: Bool) -> some View {
        Text(title)
            .fontWeight(.bold)
            .foregroundStyle(theme.magenta)
            .frame(height: PopupStyle.rowHeight)
            .padding(.horizontal, 24)
            .padding(.top, first ? 0 : PopupStyle.rowHeight)
    }

    private func row(_ command: PopupCommand, isSelected: Bool) -> some View {
        HStack {
            Text(command.title)
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
                .lineLimit(1)
            Spacer(minLength: 16)
            Text(command.shortcut)
                .foregroundStyle(isSelected ? theme.bg : theme.dim)
                .lineLimit(1)
        }
        .padding(.horizontal, 8)
        .frame(height: PopupStyle.rowHeight)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }

    private func move(_ delta: Int, in count: Int) {
        if count > 0 { selected = (selected + delta + count) % count }
    }
}

/// The search line. It's an AppKit field because it has to take keyboard focus
/// from a terminal surface, which a SwiftUI focus request doesn't do.
private struct PopupSearchField: NSViewRepresentable {
    @Binding var text: String
    let placeholder: String
    let hasList: Bool
    let onMove: (Int) -> Void
    let onSubmit: () -> Void
    let onCancel: () -> Void

    func makeNSView(context: Context) -> FocusingTextField {
        let field = FocusingTextField()
        field.delegate = context.coordinator
        field.isBordered = false
        field.drawsBackground = false
        field.focusRingType = .none
        field.font = AppFont.appKit(13)
        // The lists' keys, as in the sidebar and the Inbox, except what would type.
        let coordinator = context.coordinator
        field.onKey = { combo in
            let parent = coordinator.parent
            let moves: [KeyAction] = parent.hasList ? [.up, .down] : []
            switch Keymap.shared.action(for: combo, among: moves + [.open, .back]) {
            case .up: parent.onMove(-1)
            case .down: parent.onMove(1)
            case .open: parent.onSubmit()
            case .back: parent.onCancel()
            default: return false
            }
            return true
        }
        return field
    }

    func updateNSView(_ field: FocusingTextField, context: Context) {
        context.coordinator.parent = self
        if field.stringValue != text { field.stringValue = text }
        // AppKit colors don't follow the theme by themselves.
        let theme = AppTheme.shared
        field.textColor = theme.foreground
        field.placeholderAttributedString = NSAttributedString(
            string: placeholder,
            attributes: [
                .foregroundColor: theme.foreground.withAlphaComponent(0.5),
                .font: AppFont.appKit(13),
            ]
        )
        (field.currentEditor() as? NSTextView)?.insertionPointColor = theme.foreground
    }

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    final class Coordinator: NSObject, NSTextFieldDelegate {
        var parent: PopupSearchField

        init(parent: PopupSearchField) { self.parent = parent }

        func controlTextDidChange(_ notification: Notification) {
            guard let field = notification.object as? NSTextField else { return }
            parent.text = field.stringValue
        }

        func control(_ control: NSControl, textView: NSTextView, doCommandBy selector: Selector) -> Bool {
            switch selector {
            case #selector(NSResponder.moveUp(_:)):
                parent.onMove(-1)
            case #selector(NSResponder.moveDown(_:)):
                parent.onMove(1)
            case #selector(NSResponder.insertNewline(_:)):
                parent.onSubmit()
            case #selector(NSResponder.cancelOperation(_:)):
                parent.onCancel()
            default:
                return false
            }
            return true
        }
    }
}

/// A text field that takes keyboard focus as soon as it's in a window. While it
/// has the keyboard, onKey sees each key first, except what would type.
final class FocusingTextField: NSTextField {
    var onKey: ((KeyCombo) -> Bool)?
    private var monitor: Any?

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        guard window != nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, let onKey = self.onKey, event.window === self.window,
                  let editor = self.currentEditor(), self.window?.firstResponder === editor
            else { return event }
            let combo = KeyCombo(event: event)
            return !combo.types && onKey(combo) ? nil : event
        }
        DispatchQueue.main.async { [weak self] in
            guard let self, let window = self.window else { return }
            window.makeFirstResponder(self)
            (self.currentEditor() as? NSTextView)?.insertionPointColor = AppTheme.shared.foreground
        }
    }
}
