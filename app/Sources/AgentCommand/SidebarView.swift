import SwiftUI

/// The session list, drawn like part of a terminal program.
struct SidebarView: View {
    @Bindable var store: SessionStore
    /// Takes the keyboard for the sidebar.
    let keys: SidebarKeyView
    let showCommands: () -> Void
    let archive: (Session) -> Void
    let rename: (Session) -> Void
    let openInbox: (Session) -> Void
    let moveOut: (Session) -> Void
    let openSet: (Session) -> Void
    /// A click gives the sidebar the keyboard, with its cursor on that row.
    let clickRow: (Session) -> Void
    /// A double click, like return, shows the session.
    let openRow: (Session) -> Void
    @State private var showArchived = false

    private var theme: AppTheme { .shared }

    /// The sessions, with each set's together.
    private var rows: [(session: Session, link: SetLink)] {
        let byID = Dictionary(uniqueKeysWithValues: store.sessions.map { ($0.id, $0) })
        return PaneSets.rows(order: store.sessions.map(\.id), sets: store.setMembers)
            .compactMap { row in byID[row.id].map { ($0, row.link) } }
    }

    /// The number keys 1 to 9, by session.
    /// The keys the sidebar answers while it has the keyboard.
    private var sidebarHint: String {
        let keys = Keymap.shared
        return ["1-9", keys.label(.nextNeedingYou), keys.label(.mark), keys.label(.open), keys.label(.back), keys.label(.shortcuts)]
            .filter { !$0.isEmpty }.joined(separator: " ")
    }

    private var numbers: [String: Int] {
        Dictionary(uniqueKeysWithValues: rows.prefix(9).enumerated().map { ($1.session.id, $0 + 1) })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ScrollViewReader { scroll in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        heading("Sessions")
                        Spacer()
                        if store.sidebarFocused {
                            Text(sidebarHint)
                                .font(AppFont.ui(11))
                                .foregroundStyle(theme.dim)
                                .padding(.horizontal, 12)
                                .lineLimit(1)
                        }
                    }
                    if store.sessions.isEmpty {
                        Text("none").foregroundStyle(theme.dim).padding(.horizontal, 12)
                    }
                    ForEach(rows, id: \.session.id) { row in
                        let session = row.session
                        if row.link == .first {
                            SetLabelRow()
                                .contentShape(Rectangle())
                                .simultaneousGesture(TapGesture().onEnded { clickRow(session) })
                                .simultaneousGesture(TapGesture(count: 2).onEnded { openSet(session) })
                        }
                        SessionRow(
                            session: session,
                            link: row.link == .first ? .middle : row.link,
                            isSelected: store.selectedID == session.id,
                            isVisible: store.visibleIDs.contains(session.id),
                            isClosed: store.closedIDs.contains(session.id),
                            isCursor: store.sidebarFocused && store.cursorID == session.id,
                            isMarked: store.markedIDs.contains(session.id),
                            unread: store.unreadCount(session: session.id),
                            number: store.sidebarFocused ? numbers[session.id] : nil,
                            showsFolder: true)
                            .id(session.id)
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { clickRow(session) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { openRow(session) })
                            .contextMenu {
                                if row.link != .none {
                                    Button("Move Out of Set") { moveOut(session) }
                                }
                                Button("Inbox") { openInbox(session) }
                                Button("Rename…") { rename(session) }
                                Button("Archive…") { archive(session) }
                            }
                    }
                    if !store.archived.isEmpty {
                        archivedSection
                    }
                }
                .padding(.top, 4)
            }
            .onChange(of: store.cursorID) {
                if let id = store.cursorID, store.sidebarFocused { scroll.scrollTo(id) }
            }
            }
            Spacer(minLength: 0)
            if let problem = store.problem {
                Text(problem)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.red)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            HStack {
                Text("commands").foregroundStyle(theme.fg)
                Spacer()
                Text(Keymap.shared.label(.commands)).foregroundStyle(theme.dim)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
            .contentShape(Rectangle())
            .onTapGesture(perform: showCommands)
        }
        .font(AppFont.ui(13))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(theme.bg)
        .background(SidebarKeyHost(view: keys))
    }

    private var archivedSection: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Text(showArchived ? "▾" : "▸").foregroundStyle(theme.dim)
                Text("Archived").fontWeight(.bold).foregroundStyle(theme.magenta)
                Spacer()
                Text("\(store.archived.count)").foregroundStyle(theme.dim)
            }
            .padding(.horizontal, 12)
            .padding(.top, 16)
            .padding(.bottom, 2)
            .contentShape(Rectangle())
            .onTapGesture { showArchived.toggle() }
            if showArchived {
                ForEach(store.archived) { session in
                    SessionRow(session: session, link: .none, isSelected: false, isVisible: false, isClosed: false,
                        isCursor: false, isMarked: false, unread: 0, showsFolder: true)
                        .contextMenu {
                            if !session.isTerminal {
                                Button("Unarchive") { store.unarchive(session.id) }
                            }
                        }
                }
            }
        }
    }

    private func heading(_ title: String) -> some View {
        Text(title)
            .fontWeight(.bold)
            .foregroundStyle(theme.magenta)
            .padding(.horizontal, 12)
            .padding(.bottom, 2)
    }
}

/// Marks where a set starts, so its line doesn't seem to belong to the row above.
struct SetLabelRow: View {
    var isSelected = false
    /// Unread messages across the set, for the Inbox.
    var unread = 0
    var isCursor = false

    private var theme: AppTheme { .shared }

    var body: some View {
        HStack(spacing: 6) {
            SetLinkMark(link: .first).stroke(isSelected ? theme.bg : theme.fg, lineWidth: 1).frame(width: 8)
            Text("set").font(AppFont.ui(11)).foregroundStyle(isSelected ? theme.bg : theme.dim)
            Spacer(minLength: 0)
            if unread > 0 {
                Label("\(unread)", systemImage: "envelope.badge")
                    .font(AppFont.ui(11))
                    .foregroundStyle(isSelected ? theme.bg : theme.blue)
                    .help(unread == 1 ? "1 unread message in this set" : "\(unread) unread messages in this set")
            }
        }
        .padding(.horizontal, 12)
        .frame(height: 22)
        .background(isSelected ? theme.yellow : Color.clear)
        .overlay {
            if isCursor {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
    }
}

struct SessionRow: View {
    let session: Session
    let link: SetLink
    let isSelected: Bool
    /// Showing in a pane, though not the focused one.
    let isVisible: Bool
    /// In a set, with its pane closed: greyed out, though the set's line isn't.
    let isClosed: Bool
    /// Where the sidebar's keyboard cursor is, and whether it's marked with x.
    let isCursor: Bool
    let isMarked: Bool
    /// Unread items in the Inbox's queue.
    let unread: Int
    /// The number key that shows it, while the sidebar has the keyboard.
    var number: Int?
    /// Shows the session's working directory under its name.
    var showsFolder = false

    private var theme: AppTheme { .shared }

    private var folder: String? {
        showsFolder && !session.cwd.isEmpty ? (session.cwd as NSString).abbreviatingWithTildeInPath : nil
    }

    var body: some View {
        HStack(spacing: 6) {
            if link != .none {
                SetLinkMark(link: link).stroke(isSelected ? theme.bg : theme.fg, lineWidth: 1).frame(width: 8)
            }
            if isMarked {
                Text("x").fontWeight(.bold).foregroundStyle(isSelected ? theme.bg : theme.red)
            }
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    mark
                    Text(session.displayName)
                        .fontWeight(isSelected ? .bold : .regular)
                        .foregroundStyle(isSelected ? theme.bg : theme.fg)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: 4)
                    if unread > 0 {
                        Text("\(unread)").fontWeight(.bold).foregroundStyle(isSelected ? theme.bg : theme.blue)
                    } else if session.unseen && !isSelected {
                        Text("•").foregroundStyle(theme.blue)
                    }
                    Text(session.status)
                        .font(AppFont.ui(11))
                        .foregroundStyle(isSelected ? theme.bg : theme.dim)
                        .lineLimit(1)
                    if let number {
                        Text("\(number)")
                            .font(AppFont.ui(11))
                            .fontWeight(.bold)
                            .foregroundStyle(isSelected ? theme.bg : theme.yellow)
                    }
                }
                if let folder {
                    // Lined up with the name. The end of a long path matters most.
                    Text(folder)
                        .font(AppFont.ui(11))
                        .foregroundStyle(isSelected ? theme.bg : theme.dim)
                        .lineLimit(1)
                        .truncationMode(.head)
                        .padding(.leading, 14)
                        .help(session.cwd)
                }
            }
            .opacity(isClosed ? 0.4 : 1)
        }
        .help(session.details)
        .padding(.horizontal, 12)
        .frame(height: folder == nil ? 22 : 38)
        .background(isSelected ? theme.yellow : (isVisible ? Color(nsColor: theme.line) : Color.clear))
        .overlay {
            if isCursor {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
    }

    private var mark: some View { SessionMark(session: session, onBar: isSelected) }
}

/// The line joining a set's rows. It's drawn rather than typed, so it meets the
/// next row whatever the font's line height.
struct SetLinkMark: Shape {
    let link: SetLink

    func path(in rect: CGRect) -> Path {
        var path = Path()
        let x = rect.minX + 0.5
        path.move(to: CGPoint(x: x, y: link == .first ? rect.midY : rect.minY))
        path.addLine(to: CGPoint(x: x, y: link == .last ? rect.midY : rect.maxY))
        path.move(to: CGPoint(x: x, y: rect.midY))
        path.addLine(to: CGPoint(x: rect.maxX, y: rect.midY))
        return path
    }
}

/// What a key does while the sidebar has the keyboard.
enum SidebarKey: Equatable {
    case up, down, toggleMark, open, leave
    /// Shows the session with this number, from 1.
    case number(Int)
    /// Shows the next session that needs you.
    case nextAttention

    /// The number row's key codes for 1 to 9, which don't depend on the layout.
    static let numberKeyCodes = KeyName.digitCodes

    /// The list keys from the keymap: by default the arrows, ⌃J ⌃K or j k move, x
    /// marks, return opens, tab goes to the next that needs you and escape leaves.
    /// 1 to 9 show that session.
    static func from(keyCode: UInt16, flags: NSEvent.ModifierFlags, keys: Keymap = .shared) -> SidebarKey? {
        let combo = KeyCombo(key: keyCode, mods: KeyCombo.Mods(flags))
        if combo.mods.isEmpty, let index = numberKeyCodes.firstIndex(of: keyCode) {
            return .number(index + 1)
        }
        switch keys.action(for: combo, among: [.up, .down, .open, .back, .mark, .nextNeedingYou]) {
        case .up: return .up
        case .down: return .down
        case .open: return .open
        case .back: return .leave
        case .mark: return .toggleMark
        case .nextNeedingYou: return .nextAttention
        default: return nil
        }
    }
}

/// Receives the keyboard for the sidebar. A SwiftUI focus request can't take it
/// from a terminal surface.
final class SidebarKeyView: NSView {
    var onFocus: ((Bool) -> Void)?
    var onKey: ((SidebarKey) -> Void)?

    override var acceptsFirstResponder: Bool { true }

    override func becomeFirstResponder() -> Bool {
        onFocus?(true)
        return true
    }

    override func resignFirstResponder() -> Bool {
        onFocus?(false)
        return true
    }

    override func keyDown(with event: NSEvent) {
        if let key = SidebarKey.from(keyCode: event.keyCode, flags: event.modifierFlags) {
            onKey?(key)
        } else {
            super.keyDown(with: event)
        }
    }
}

private struct SidebarKeyHost: NSViewRepresentable {
    let view: SidebarKeyView

    func makeNSView(context: Context) -> SidebarKeyView { view }
    func updateNSView(_ nsView: SidebarKeyView, context: Context) {}
}
