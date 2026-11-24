import Foundation

/// How this app's sidebar lists sessions: its own view (D2), kept in
/// UserDefaults. The daemon sends what contains what; this decides the order.
nonisolated struct SidebarOptions: Codable, Equatable {
    enum Mode: String, Codable, CaseIterable {
        case groups, list

        var title: String { self == .groups ? "Groups" : "List" }
    }

    enum Sort: String, Codable, CaseIterable {
        case created, name, activity, status, folder

        var title: String {
            switch self {
            case .created: "Created"
            case .name: "Name"
            case .activity: "Activity"
            case .status: "Status"
            case .folder: "Folder"
            }
        }
    }

    var mode = Mode.groups
    var sort = Sort.created
    var descending = false
    /// The groups collapsed to their header.
    var collapsed: Set<String> = []

    static let key = "sidebarView"

    static func load(_ defaults: UserDefaults = .standard) -> SidebarOptions {
        guard let data = defaults.data(forKey: key), let saved = try? JSONDecoder().decode(SidebarOptions.self, from: data) else {
            return SidebarOptions()
        }
        return saved
    }

    func save(_ defaults: UserDefaults = .standard) {
        if let data = try? JSONEncoder().encode(self) { defaults.set(data, forKey: Self.key) }
    }
}

/// A group's header in the sidebar.
nonisolated struct GroupHeader: Equatable {
    let group: SessionGroup
    /// The sessions listed in it.
    let count: Int
    /// What in it needs you most, shown while it's collapsed.
    let urgent: SessionAttention?
    let collapsed: Bool
}

/// One line of the sidebar.
nonisolated enum SidebarRow: Equatable, Identifiable {
    case header(GroupHeader)
    case session(Session, link: SetLink)

    var id: String {
        switch self {
        case .header(let header): "group:" + header.group.key
        case .session(let session, _): session.id
        }
    }

    var session: Session? {
        if case .session(let session, _) = self { return session }
        return nil
    }
}

/// Lays out the sidebar from the daemon's structure and this app's options. It
/// has no dependencies, so it can be checked on its own.
enum SidebarLayout {
    /// The rows: group headers (in group view, once there's a group besides
    /// Default) and the listed sessions, sorted, with each set's together in its
    /// order. A collapsed group shows only its header. While filtering, groups
    /// with nothing listed are left out.
    static func rows(sessions: [Session], groups: [SessionGroup], sets: [[String]], options: SidebarOptions,
                     filtering: Bool = false) -> [SidebarRow] {
        let sorted = sort(sessions, by: options.sort, descending: options.descending)
        let ordered = orderedGroups(groups)
        let hasGroups = ordered.contains { !$0.isDefault || !$0.host.isEmpty }
        guard options.mode == .groups, hasGroups else { return sessionRows(sorted, sets: sets) }

        // Each session in its group, on its own host: a host's sessions stay in
        // the host's groups, and one whose group isn't known goes to its host's
        // Default.
        // Groups by key: every host's Default is "default".
        let known = Set(ordered.map(\.key))
        var defaults: [String: String] = [:]
        for group in ordered where group.isDefault { defaults[group.host] = group.key }
        var byGroup: [String: [Session]] = [:]
        for session in sorted {
            let fallback = defaults[session.hostName] ?? SessionGroup.key(session.hostName, "default")
            let group = session.groupKey.flatMap { known.contains($0) ? $0 : nil } ?? fallback
            byGroup[group, default: []].append(session)
        }
        var rows: [SidebarRow] = []
        for group in ordered {
            let members = byGroup[group.key] ?? []
            if filtering && members.isEmpty { continue }
            if group.isDefault && members.isEmpty { continue }
            let collapsed = options.collapsed.contains(group.key) && !filtering
            rows.append(.header(GroupHeader(group: group, count: members.count, urgent: mostUrgent(members), collapsed: collapsed)))
            if !collapsed { rows += sessionRows(members, sets: sets) }
        }
        return rows
    }

    /// The sessions you can see, top to bottom: what ⌘1-9 and the cursor go by.
    static func sessionIDs(_ rows: [SidebarRow]) -> [String] { rows.compactMap(\.session?.id) }

    private static func sessionRows(_ sessions: [Session], sets: [[String]]) -> [SidebarRow] {
        let byID = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        return PaneSets.rows(order: sessions.map(\.id), sets: sets).compactMap { row in
            byID[row.id].map { SidebarRow.session($0, link: row.link) }
        }
    }

    /// Live groups in the order you arranged them, Default last: this Mac's,
    /// then each host's, as the host arranges them.
    static func orderedGroups(_ groups: [SessionGroup]) -> [SessionGroup] {
        groups.filter { !$0.isArchived }.sorted { a, b in
            if a.host != b.host { return a.host.isEmpty || (!b.host.isEmpty && a.host < b.host) }
            if a.isDefault != b.isDefault { return !a.isDefault }
            return (a.order, a.id) < (b.order, b.id)
        }
    }

    /// The groups a session can be moved to: its own host's.
    static func groups(for session: Session, in groups: [SessionGroup]) -> [SessionGroup] {
        orderedGroups(groups).filter { $0.host == session.hostName }
    }

    static func mostUrgent(_ sessions: [Session]) -> SessionAttention? {
        sessions.compactMap(\.attention).min { $0.rank < $1.rank }
    }

    /// Sessions in the chosen order. Ties keep creation order (ids are
    /// time-ordered), whichever the direction.
    static func sort(_ sessions: [Session], by sort: SidebarOptions.Sort, descending: Bool) -> [Session] {
        sessions.sorted { a, b in
            var c = compare(a, b, by: sort)
            if descending { c = -c }
            return c != 0 ? c < 0 : a.id < b.id
        }
    }

    private static func compare(_ a: Session, _ b: Session, by sort: SidebarOptions.Sort) -> Int {
        func order<T: Comparable>(_ x: T, _ y: T) -> Int { x < y ? -1 : (x > y ? 1 : 0) }
        func names() -> Int {
            switch a.displayName.localizedStandardCompare(b.displayName) {
            case .orderedAscending: -1
            case .orderedDescending: 1
            case .orderedSame: 0
            }
        }
        switch sort {
        case .created: return 0
        case .name: return names()
        case .activity:
            let x = a.lastEventAt.flatMap(Timestamps.date) ?? .distantPast
            let y = b.lastEventAt.flatMap(Timestamps.date) ?? .distantPast
            return order(x, y)
        case .status: return order(statusRank(a), statusRank(b))
        case .folder:
            let x = (a.cwd as NSString).lastPathComponent.lowercased()
            let y = (b.cwd as NSString).lastPathComponent.lowercased()
            return x != y ? order(x, y) : names()
        }
    }

    /// The CLI's status order (internal/core statusRank): what needs you first.
    static func statusRank(_ s: Session) -> Int {
        switch s.status {
        case "waiting": 0
        case "error": 1
        case "busy": 2
        case "done": s.unseen ? 3 : 4
        case "starting", "unknown": 5
        case "paused", "finished": 6
        default: 7
        }
    }
}

/// What dragging something in the sidebar onto a group does.
enum SidebarDrop: Equatable {
    case moveSessions([String], to: String)
    case moveGroup(String, before: String?, after: String?)

    static func sessionItem(_ id: String) -> String { "session:" + id }
    static func groupItem(_ id: String) -> String { "group:" + id }

    /// The change a drop onto group target asks for. A dragged session that's
    /// marked takes every marked session with it. A group dropped on another
    /// goes before it; on Default, after the last group. Anything else is nothing.
    /// Groups are named by key (SessionGroup.key), and a group moves only
    /// among its own host's.
    static func action(items: [String], onto target: String, groups: [SessionGroup], marked: Set<String> = []) -> SidebarDrop? {
        guard let item = items.first else { return nil }
        if item.hasPrefix("session:") {
            var ids = items.compactMap { $0.hasPrefix("session:") ? String($0.dropFirst(8)) : nil }
            if ids.contains(where: marked.contains) { ids = Array(marked).sorted() }
            return ids.isEmpty ? nil : .moveSessions(ids, to: target)
        }
        guard item.hasPrefix("group:") else { return nil }
        let key = String(item.dropFirst(6))
        let ordered = SidebarLayout.orderedGroups(groups)
        guard key != target, let dragged = ordered.first(where: { $0.key == key }), !dragged.isDefault,
              let onto = ordered.first(where: { $0.key == target }), onto.host == dragged.host else { return nil }
        if onto.isDefault {
            guard let last = ordered.last(where: { !$0.isDefault && $0.host == dragged.host }), last.key != key else { return nil }
            return .moveGroup(key, before: nil, after: last.key)
        }
        return .moveGroup(key, before: target, after: nil)
    }
}
