import AppKit
import SwiftUI

/// The popup: the queue on the left, and the message on the right with its session,
/// next and previous, and a place to reply.
struct PopupInboxView: View {
    @Bindable var model: InboxModel

    @AppStorage("popup.listWidth") private var listWidth = 260.0
    @AppStorage("popup.replyHeight") private var replyHeight = 130.0

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { window in
                HStack(spacing: 0) {
                    queue.frame(width: min(listWidth, max(160, window.size.width - 320)))
                    InboxDivider(vertical: true, value: $listWidth, range: 160...max(160, window.size.width - 320))
                    VStack(spacing: 0) {
                        header
                        Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                        InboxNotice(model: model)
                        message.frame(maxWidth: .infinity, maxHeight: .infinity)
                        InboxDivider(
                            vertical: false, value: $replyHeight, inverted: true,
                            range: 60...max(60, window.size.height - 120))
                        answer.frame(height: min(replyHeight, max(60, window.size.height - 120)))
                    }
                }
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            Text(Keymap.shared.popupHint)
                .font(AppFont.ui(11))
                .foregroundStyle(theme.dim)
                .lineLimit(1)
                .padding(.horizontal, 16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .frame(height: 22)
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
        .overlay { Rectangle().strokeBorder(Color(nsColor: theme.line), lineWidth: 1) }
    }

    @ViewBuilder private var queue: some View {
        if model.items.isEmpty {
            Text("Nothing to deal with.")
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { scroll in
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(model.items) { item in
                            row(item)
                                .id(item.id)
                                .contentShape(Rectangle())
                                .onTapGesture { model.click(item: item) }
                        }
                    }
                }
                .onChange(of: model.selectedID) { _, id in
                    if let id { scroll.scrollTo(id) }
                }
            }
        }
    }

    private func row(_ item: InboxItem) -> some View {
        let isSelected = item.id == model.selectedID
        return HStack(spacing: 8) {
            Circle()
                .fill(item.read ? Color.clear : (isSelected ? theme.bg : theme.blue))
                .frame(width: 6, height: 6)
                .help(item.read ? "Read" : "Unread")
            Image(systemName: item.kindIcon)
                .foregroundStyle(isSelected ? theme.bg : kindColor(item))
                .frame(width: 16)
                .help(item.kindLabel)
            Text(model.sessionName(item.sessionID))
                .fontWeight(item.read ? .regular : .bold)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 8)
            Text(item.age).font(AppFont.ui(11)).foregroundStyle(isSelected ? theme.bg : theme.dim)
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .frame(height: 26)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isSelected ? theme.yellow : Color.clear)
    }

    private var header: some View {
        HStack(spacing: 10) {
            if let item = model.selected {
                Image(systemName: item.kindIcon).foregroundStyle(kindColor(item)).help(item.kindLabel)
                Text(model.sessionName(item.sessionID)).fontWeight(.bold).lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            if let position = model.position {
                iconButton("chevron.up", help: Keymap.shared.tip("Previous", .up), enabled: position.index > 1) { model.step(-1) }
                Text("\(position.index) of \(position.count)").font(AppFont.ui(11)).foregroundStyle(theme.dim)
                iconButton("chevron.down", help: Keymap.shared.tip("Next", .down), enabled: position.index < position.count) { model.step(1) }
            }
            if let item = model.selected {
                SpeakButton(item: item)
                textButton("Dismiss") { model.dismiss(item) }
                textButton("Go to session") { model.go(item) }
            }
            iconButton("xmark", help: Keymap.shared.tip("Close", .back), enabled: true) { model.close() }
        }
        .padding(.horizontal, 16)
        .frame(height: 34)
    }

    private func textButton(_ title: String, action: @escaping () -> Void) -> some View {
        Button(title, action: action).buttonStyle(.plain).font(AppFont.ui(11))
    }

    private func iconButton(_ icon: String, help: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon).foregroundStyle(enabled ? theme.fg : theme.dim)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .help(help)
    }

    @ViewBuilder private var message: some View {
        if let item = model.selected {
            ScrollView {
                Group {
                    if item.isMarkdown {
                        MarkdownView(item.text, dim: theme.dim, panel: Color(nsColor: theme.line), code: theme.yellow)
                    } else {
                        Text(item.rendered).textSelection(.enabled)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
            }
        } else {
            Color.clear
        }
    }

    @ViewBuilder private var answer: some View {
        if let item = model.selected {
            InboxAnswerView(model: model, item: item)
        } else {
            Color.clear
        }
    }

    private func kindColor(_ item: InboxItem) -> Color {
        switch item.kind {
        case "question", "permission": return theme.yellow
        case "error": return theme.red
        default: return theme.green
        }
    }
}
