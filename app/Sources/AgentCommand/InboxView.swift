import AppKit
import SwiftUI

/// The full-window Inbox: sessions on the left; on the right the messages, the
/// selected one and a place to reply. The dividers drag, and are remembered.
struct InboxView: View {
    @Bindable var model: InboxModel

    @AppStorage("inbox.sessionsWidth") private var sessionsWidth = 260.0
    @AppStorage("inbox.listWidth") private var listWidth = 360.0
    @AppStorage("inbox.messageShare") private var messageShare = 0.5
    @AppStorage("inbox.replyHeight") private var replyHeight = 140.0
    @AppStorage(InboxLayout.key) private var layout = InboxLayout.columns

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { window in
                HStack(spacing: 0) {
                    sessions.frame(width: sessionsWidth)
                    InboxDivider(vertical: true, value: $sessionsWidth, range: 180...max(180, window.size.width - 500))
                    if layout == .columns {
                        columns(width: window.size.width - sessionsWidth, height: window.size.height)
                    } else {
                        stacked(height: window.size.height)
                    }
                }
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            footer
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
    }

    /// The messages, then the message box beside them.
    private func columns(width: CGFloat, height: CGFloat) -> some View {
        HStack(spacing: 0) {
            messages.frame(width: min(listWidth, max(200, width - 260)))
            InboxDivider(vertical: true, value: $listWidth, range: 200...max(200, width - 260))
            messageBox(height: height)
        }
    }

    /// The messages, then the message box under them.
    private func stacked(height: CGFloat) -> some View {
        let share = Binding(
            get: { messageShare * height },
            set: { messageShare = height > 0 ? $0 / height : 0.5 })
        return VStack(spacing: 0) {
            messages.frame(maxWidth: .infinity, maxHeight: .infinity)
            InboxDivider(vertical: false, value: share, inverted: true, range: 200...max(200, height - 80))
            messageBox(height: min(share.wrappedValue, max(200, height - 80)))
                .frame(height: min(share.wrappedValue, max(200, height - 80)))
        }
    }

    /// The selected message, and a place to reply under it.
    private func messageBox(height: CGFloat) -> some View {
        VStack(spacing: 0) {
            reader.frame(maxWidth: .infinity, maxHeight: .infinity)
            InboxDivider(vertical: false, value: $replyHeight, inverted: true, range: 60...max(60, height - 80))
            reply.frame(height: min(replyHeight, max(60, height - 80)))
        }
    }

    private var footer: some View {
        Text(Keymap.shared.inboxHint)
            .font(AppFont.ui(11))
            .foregroundStyle(theme.dim)
            .lineLimit(1)
            .padding(.horizontal, 16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: 22)
    }

    // MARK: Sessions

    private var sessions: some View {
        ScrollViewReader { scroll in
            ScrollView {
                LazyVStack(spacing: 0) {
                    filterRow("Queue", count: model.queueCount, filter: .queue)
                    filterRow("All", count: nil, filter: .all)
                    Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1).padding(.vertical, 4)
                    ForEach(model.rows, id: \.session.id) { row in
                        if row.link == .first {
                            let members = model.setMembers(of: row.session.id)
                            SetLabelRow(
                                isSelected: model.filter == .set(members),
                                unread: model.unreadCount(set: members),
                                isCursor: isCursor(.set(members))
                            )
                            .id(InboxFilter.set(members))
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { model.click(filter: .set(members)) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(.set(members)) })
                        }
                        sessionRow(row.session, link: row.link == .first ? .middle : row.link)
                            .id(InboxFilter.session(row.session.id))
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { model.click(filter: .session(row.session.id)) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(.session(row.session.id)) })
                            .contextMenu {
                                if model.isMoveOutable(row.session) {
                                    Button("Move Out of Set") { model.moveOut(row.session) }
                                }
                                Button("Rename…") { model.rename(model.targets(forRow: row.session)) }
                                Button("Archive…") { model.archive(model.targets(forRow: row.session)) }
                            }
                    }
                }
            }
            .onChange(of: model.sessionCursor) { _, cursor in scroll.scrollTo(cursor) }
        }
    }

    private func isCursor(_ filter: InboxFilter) -> Bool {
        model.panel == .sessions && !model.typing && model.sessionCursor == filter
    }

    private func filterRow(_ title: String, count: Int?, filter: InboxFilter) -> some View {
        let isSelected = model.filter == filter
        return HStack {
            Text(title).fontWeight(isSelected ? .bold : .regular)
            Spacer()
            if let count, count > 0 { Text("\(count)").fontWeight(.bold) }
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .frame(height: 22)
        .background(isSelected ? theme.yellow : Color.clear)
        .overlay {
            if isCursor(filter) {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
        .id(filter)
        .contentShape(Rectangle())
        .simultaneousGesture(TapGesture().onEnded { model.click(filter: filter) })
        .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(filter) })
    }

    private func sessionRow(_ session: Session, link: SetLink) -> some View {
        let isSelected = model.filter == .session(session.id)
        let unread = model.unreadCount(session.id)
        let open = model.openItem(session.id)
        return VStack(alignment: .leading, spacing: 0) {
            SessionRow(
                session: session, link: link, isSelected: isSelected, isVisible: false, isClosed: false,
                isCursor: isCursor(.session(session.id)), isMarked: model.markedSessions.contains(session.id), unread: 0)
            if unread > 0 || open != nil {
                HStack(spacing: 6) {
                    // Keeps a set's line running past this row's icons.
                    if link == .middle {
                        SetLinkMark(link: .middle).stroke(isSelected ? theme.bg : theme.fg, lineWidth: 1).frame(width: 8)
                    } else if link == .last {
                        Color.clear.frame(width: 8)
                    }
                    HStack(spacing: 10) {
                        if unread > 0 {
                            Label("\(unread)", systemImage: "envelope.badge")
                                .help(unread == 1 ? "1 unread message" : "\(unread) unread messages")
                        }
                        if let open {
                            Image(systemName: open.kindIcon).help("Waiting: \(open.kindLabel)")
                        }
                    }
                    .padding(.leading, 14)
                    .padding(.bottom, 4)
                }
                .font(AppFont.ui(11))
                .foregroundStyle(isSelected ? theme.bg : theme.blue)
                .padding(.horizontal, 12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(isSelected ? theme.yellow : Color.clear)
            }
        }
    }

    // MARK: Messages

    @ViewBuilder private var messages: some View {
        if model.items.isEmpty {
            Text(model.filter == .queue ? "Nothing to deal with." : "No messages yet.")
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { scroll in
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(model.items) { item in
                            messageRow(item)
                                .id(item.id)
                                .contentShape(Rectangle())
                                .simultaneousGesture(TapGesture().onEnded { model.click(item: item) })
                                .simultaneousGesture(TapGesture(count: 2).onEnded { model.open(item) })
                        }
                    }
                }
                .onChange(of: model.messageCursor) { _, id in
                    if let id { scroll.scrollTo(id) }
                }
            }
        }
    }

    @ViewBuilder private func messageRow(_ item: InboxItem) -> some View {
        let isSelected = item.id == model.selectedID
        let dim = isSelected ? theme.bg : theme.dim
        Group {
            if layout == .columns {
                // A narrow list, so the first line of the message gets its own line.
                HStack(alignment: .top, spacing: 8) {
                    rowMarks(item, isSelected: isSelected)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 8) {
                            rowName(item)
                            Spacer(minLength: 8)
                            rowEnd(item, dim: dim)
                        }
                        Text(item.preview).font(AppFont.ui(11)).foregroundStyle(dim).lineLimit(1)
                    }
                }
                .padding(.vertical, 6)
            } else {
                HStack(spacing: 8) {
                    rowMarks(item, isSelected: isSelected)
                    rowName(item).frame(width: 130, alignment: .leading)
                    Text(item.preview).foregroundStyle(dim).lineLimit(1)
                    Spacer(minLength: 8)
                    rowEnd(item, dim: dim)
                }
                .frame(height: 24)
            }
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .opacity(item.isOpen || isSelected ? 1 : 0.6)
        .padding(.horizontal, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isSelected ? theme.yellow : Color.clear)
        .overlay {
            if model.panel == .messages && !model.typing && model.messageCursor == item.id {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
    }

    private func rowMarks(_ item: InboxItem, isSelected: Bool) -> some View {
        HStack(spacing: 8) {
            if model.markedMessages.contains(item.id) {
                Text("x").fontWeight(.bold).foregroundStyle(isSelected ? theme.bg : theme.red)
            }
            Circle()
                .fill(item.read ? Color.clear : (isSelected ? theme.bg : theme.blue))
                .frame(width: 6, height: 6)
                .help(item.read ? "Read" : "Unread")
            Image(systemName: item.kindIcon)
                .foregroundStyle(isSelected ? theme.bg : kindColor(item))
                .frame(width: 16)
                .help(item.kindLabel)
        }
    }

    private func rowName(_ item: InboxItem) -> some View {
        Text(model.sessionName(item.sessionID))
            .fontWeight(item.read ? .regular : .bold)
            .lineLimit(1)
            .truncationMode(.middle)
    }

    private func rowEnd(_ item: InboxItem, dim: Color) -> some View {
        HStack(spacing: 8) {
            if let icon = item.stateIcon {
                Image(systemName: icon).foregroundStyle(dim).help(item.stateLabel)
            }
            Text(item.age).font(AppFont.ui(11)).foregroundStyle(dim)
        }
    }

    private func kindColor(_ item: InboxItem) -> Color {
        guard item.isOpen else { return theme.dim }
        switch item.kind {
        case "question", "permission": return theme.yellow
        case "error": return theme.red
        default: return theme.green
        }
    }

    // MARK: The selected message

    @ViewBuilder private var reader: some View {
        if let item = model.selected {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 8) {
                    Image(systemName: item.kindIcon).foregroundStyle(kindColor(item)).help(item.kindLabel)
                    Text(model.sessionName(item.sessionID)).fontWeight(.bold).lineLimit(1)
                    if let icon = item.stateIcon {
                        Image(systemName: icon).foregroundStyle(theme.dim).help(item.stateLabel)
                        Text(item.stateLabel).font(AppFont.ui(11)).foregroundStyle(theme.dim).lineLimit(1)
                    }
                    Spacer()
                    SpeakButton(item: item)
                    actionButton("Dismiss", enabled: item.isOpen) { model.dismiss(item) }
                    actionButton(item.read ? "Mark unread" : "Mark read", enabled: true) {
                        model.setRead(item, !item.read)
                    }
                    actionButton("Go to session", enabled: model.session(of: item)?.isArchived == false) {
                        model.go(item)
                    }
                }
                .padding(.horizontal, 16)
                .frame(height: 30)
                Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                InboxNotice(model: model)
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
            }
        } else {
            Text(model.items.isEmpty ? "" : "Press return or double-click a message to open it.")
                .font(AppFont.ui(11))
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func actionButton(_ title: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(title, action: action)
            .buttonStyle(.plain)
            .font(AppFont.ui(11))
            .foregroundStyle(enabled ? theme.fg : theme.dim)
            .disabled(!enabled)
    }

    // MARK: Reply

    @ViewBuilder private var reply: some View {
        if let item = model.selected {
            InboxAnswerView(model: model, item: item)
        } else {
            Color.clear
        }
    }
}
