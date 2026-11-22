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
    let openInEditor: (Session) -> Void
    let moveOut: (Session) -> Void
    let openSet: (Session) -> Void
    /// A click gives the sidebar the keyboard, with its cursor on that row.
    let clickRow: (Session) -> Void
    /// A double click, like return, shows the session.
    let openRow: (Session) -> Void
    /// Turns Remote on or off for a session on this Mac.
    var setRemote: (Session, Bool) -> Void = { _, _ in }
    /// Removes a session added from a host, here only.
    /// Asks for a new group's name, then moves these sessions into it.
    var newGroup: ([Session]) -> Void = { _ in }
    /// Asks for a group's new name.
    var renameGroup: (SessionGroup) -> Void = { _ in }
    @State private var showArchived = false

    private var theme: AppTheme { .shared }

    /// Group headers and the sessions the filter lists, with each set's together.
    private var rows: [SidebarRow] { store.sidebarRows }

    /// Groups or a list, and the sort: this app's own view.
    private var viewMenu: some View {
        Menu {
            Picker("Show", selection: $store.options.mode) {
                ForEach(SidebarOptions.Mode.allCases, id: \.self) { Text($0.title).tag($0) }
            }
            .pickerStyle(.inline)
            Picker("Sort by", selection: $store.options.sort) {
                ForEach(SidebarOptions.Sort.allCases, id: \.self) { Text($0.title).tag($0) }
            }
            .pickerStyle(.inline)
            Toggle("Descending", isOn: $store.options.descending)
            Divider()
            Button("New Group…") { newGroup([]) }
        } label: {
            Text(store.options.mode == .groups ? "groups ▾" : "list ▾").font(AppFont.ui(11)).foregroundStyle(theme.dim)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("Show groups or one list, and how sessions are sorted")
    }

    /// Takes sessions or a group dropped on a group.
    private func drop(_ items: [String], onto group: String) -> Bool {
        guard let action = SidebarDrop.action(items: items, onto: group, groups: store.groups, marked: store.markedIDs) else { return false }
        store.perform(action)
        return true
    }

    /// From the daemon's snapshot: it changes as clients attach.
    private var attached: [String: Int] { store.remote?.attached ?? [:] }

    /// All, Local, Remote or a host: only once there's a host to tell apart.
    @ViewBuilder private var filterMenu: some View {
        let hosts = store.filterHosts
        if !hosts.isEmpty {
            Menu {
                ForEach(SidebarFilter.choices(hosts: hosts)) { choice in
                    Button {
                        store.filter = choice
                    } label: {
                        if choice == store.shownFilter { Label(choice.title, systemImage: "checkmark") } else { Text(choice.title) }
                    }
                }
            } label: {
                Text(store.shownFilter.title.lowercased() + " ▾").font(AppFont.ui(11)).foregroundStyle(theme.dim)
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("Which sessions to list: all, this Mac's, or a Remote host's")
        }
    }

    /// The number keys 1 to 9, by session.
    /// The keys the sidebar answers while it has the keyboard.
    private var sidebarHint: String {
        let keys = Keymap.shared
        return ["1-9", keys.label(.nextNeedingYou), keys.label(.mark), keys.label(.open), keys.label(.back), keys.label(.shortcuts)]
            .filter { !$0.isEmpty }.joined(separator: " ")
    }

    private var numbers: [String: Int] {
        Dictionary(uniqueKeysWithValues: store.sidebarIDs.prefix(9).enumerated().map { ($1, $0 + 1) })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ScrollViewReader { scroll in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        heading("Sessions")
                        filterMenu.padding(.bottom, 2)
                        viewMenu.padding(.bottom, 2)
                        Spacer()
                        if store.sidebarFocused {
                            Text(sidebarHint)
                                .font(AppFont.ui(11))
                                .foregroundStyle(theme.dim)
                                .padding(.horizontal, 12)
                                .lineLimit(1)
                        }
                    }
                    if rows.isEmpty {
                        Text(store.sessions.isEmpty ? "none" : "none here: \(store.sessions.count) in All")
                            .foregroundStyle(theme.dim).padding(.horizontal, 12)
                    }
                    ForEach(rows) { sidebarRow in
                        switch sidebarRow {
                        case .header(let header):
                            groupHeader(header)
                        case .session(let session, let link):
                            sessionRow(session, link: link)
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
            footer
        }
        .font(AppFont.ui(13))
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(theme.bg)
        .background(SidebarKeyHost(view: keys))
    }

    /// A group's header: click to collapse; drop sessions on it to move them in,
    /// or another group to put that one here.
    private func groupHeader(_ header: GroupHeader) -> some View {
        let group = header.group
        return GroupHeaderRow(header: header)
            .contentShape(Rectangle())
            .onTapGesture { store.toggleCollapsed(group.id) }
            .draggable(SidebarDrop.groupItem(group.id))
            .dropDestination(for: String.self) { items, _ in drop(items, onto: group.id) }
            .contextMenu {
                Button(header.collapsed ? "Expand" : "Collapse") { store.toggleCollapsed(group.id) }
                if !group.isDefault {
                    Button("Rename…") { renameGroup(group) }
                    Menu("Colour") {
                        ForEach(SessionGroup.colors, id: \.self) { color in
                            Button(color.capitalized) { store.setGroupColor(group.id, color) }
                        }
                    }
                    Button("Move Up") { store.moveGroup(group.id, by: -1) }
                    Button("Move Down") { store.moveGroup(group.id, by: 1) }
                    Divider()
                    Button("Delete Group") { store.deleteGroup(group.id) }
                }
            }
    }

    /// The groups a session can move to, and a new one.
    @ViewBuilder private func moveToGroupMenu(_ session: Session) -> some View {
        Menu("Move to Group") {
            ForEach(SidebarLayout.groups(for: session, in: store.groups)) { group in
                if group.id != session.groupID {
                    Button(group.title) { store.moveSessions(store.actionTargets(forRow: session).map(\.id), to: group.id) }
                }
            }
            Divider()
            Button("New Group…") { newGroup(store.actionTargets(forRow: session)) }
        }
    }

    @ViewBuilder private func sessionRow(_ session: Session, link: SetLink) -> some View {
                        let row = (session: session, link: link)
                        let place = store.place(of: session, clients: attached)
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
                            showsFolder: true,
                            place: place,
                            scheduled: session.scheduledNote(jobs: store.jobs))
                            .id(session.id)
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { clickRow(session) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { openRow(session) })
                            .contextMenu {
                                if row.link != .none {
                                    Button("Move Out of Set") { moveOut(session) }
                                }
                                Button("Inbox") { openInbox(session) }
                                if !session.isOnHost {
                                    Button("Open in Editor") { openInEditor(session) }
                                }
                                Button("Rename…") { rename(session) }
                                if place.canToggleRemote {
                                    Button(session.isRemote ? "Turn Remote Off" : "Turn Remote On") {
                                        setRemote(session, !session.isRemote)
                                    }
                                }
                                if place.canArchive {
                                    Button("Archive…") { archive(session) }
                                }
                                moveToGroupMenu(session)
                            }
                            .draggable(SidebarDrop.sessionItem(session.id))
                            .dropDestination(for: String.self) { items, _ in
                                drop(items, onto: session.groupID ?? "default")
                            }
    }

    private var footer: some View {
        VStack(spacing: 0) {
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
                        isCursor: false, isMarked: false, unread: 0, showsFolder: true,
                        scheduled: session.scheduledNote(jobs: store.jobs))
                        .contextMenu {
                            if session.can(.unarchive) {
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

/// A group's line in the sidebar: open or collapsed, its colour and name, how
/// many sessions it lists, and, collapsed, what in it needs you.
struct GroupHeaderRow: View {
    let header: GroupHeader

    private var theme: AppTheme { .shared }

    static func color(_ name: String, theme: AppTheme) -> Color {
        switch name {
        case "blue": theme.blue
        case "magenta": theme.magenta
        case "cyan": theme.cyan
        case "yellow": theme.yellow
        case "green": theme.green
        case "red": theme.red
        default: theme.fg
        }
    }

    var body: some View {
        HStack(spacing: 6) {
            Text(header.collapsed ? "▸" : "▾").foregroundStyle(theme.dim)
            Text(header.group.title).fontWeight(.bold).foregroundStyle(Self.color(header.group.color, theme: theme)).lineLimit(1)
            Spacer(minLength: 0)
            if header.collapsed, let urgent = header.urgent {
                Text(urgent.reason == "unseen_done" ? "done" : urgent.reason.replacingOccurrences(of: "_", with: " "))
                    .font(AppFont.ui(11))
                    .foregroundStyle(urgent.rank == 1 ? theme.yellow : urgent.rank == 2 ? theme.red : theme.green)
            }
            Text("\(header.count)").foregroundStyle(theme.dim)
        }
        .padding(.horizontal, 12)
        .padding(.top, 10)
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
    /// Where it runs: a host's badge, or shared from this Mac.
    var place: SessionPlace? = nil
    /// A scheduled job started or continues it (Session.scheduledNote): a clock
    /// beside its name, with this as its tooltip.
    var scheduled: String? = nil

    private var theme: AppTheme { .shared }

    private var help: String {
        [session.details, scheduled, place?.help].compactMap { $0 }.joined(separator: "\n")
    }

    private var accessibilityName: String {
        [session.displayName, scheduled == nil ? nil : "scheduled", place?.hostBadge].compactMap { $0 }.joined(separator: ", ")
    }

    /// A host's folder is as it gave it: this Mac's home means nothing there.
    private var folder: String? {
        guard showsFolder, !session.cwd.isEmpty || session.isOnHost else { return nil }
        return session.isOnHost ? session.cwd : (session.cwd as NSString).abbreviatingWithTildeInPath
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
                    if let scheduled {
                        Image(systemName: "clock")
                            .font(AppFont.ui(11))
                            .foregroundStyle(isSelected ? theme.bg : theme.magenta)
                            .help(scheduled)
                            .accessibilityHidden(true)
                    }
                    Spacer(minLength: 4)
                    if unread > 0 {
                        Text("\(unread)").fontWeight(.bold).foregroundStyle(isSelected ? theme.bg : theme.blue)
                    } else if session.unseen && !isSelected {
                        Text("•").foregroundStyle(theme.blue)
                    }
                    if let shared = place?.shared {
                        Text(shared)
                            .font(AppFont.ui(11))
                            .foregroundStyle(isSelected ? theme.bg : theme.cyan)
                            .lineLimit(1)
                    }
                    Text(place?.note ?? session.status)
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
                    HStack(spacing: 6) {
                        if let badge = place?.hostBadge {
                            Text(badge)
                                .foregroundStyle(isSelected ? theme.bg : theme.cyan)
                                .lineLimit(1)
                                .layoutPriority(1)
                        }
                        Text(folder)
                            .foregroundStyle(isSelected ? theme.bg : theme.dim)
                            .lineLimit(1)
                            .truncationMode(.head)
                            .help(session.cwd)
                    }
                    .font(AppFont.ui(11))
                    .padding(.leading, 14)
                }
            }
            .opacity(isClosed || place?.dimmed == true ? 0.4 : 1)
        }
        .help(help)
        .accessibilityLabel(accessibilityName)
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
