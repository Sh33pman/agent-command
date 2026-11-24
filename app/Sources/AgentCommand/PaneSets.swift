import Foundation

/// Sessions created together with pane splits. It has no name, only an id.
nonisolated struct SessionSet: Codable, Equatable {
    let id: String
    var tree: PaneTree
}

/// How a row in the session list connects to the rows around it.
nonisolated enum SetLink: Equatable {
    case none, first, middle, last
}

/// What the panes show, and the sets that aren't showing: each set's panes, kept
/// by its id. Which sessions are in which set is the daemon's (reconcile); this
/// keeps only where their panes are, which are open, and the focus. It has no
/// dependencies, so it can be checked on its own.
struct PaneSets: Codable, Equatable {
    /// The panes showing now: a set, one session alone, or one empty pane.
    private(set) var current = PaneTree()
    /// The set showing now, if it is one.
    private(set) var currentSetID: String?
    private(set) var others: [SessionSet] = []

    /// Each set's sessions, in pane order.
    var members: [[String]] {
        let showing = currentSetID == nil ? [] : [current]
        return (showing + others.map(\.tree)).map { $0.leaves.compactMap(\.session) }
    }

    /// Sessions in a set whose pane is closed.
    var closedSessions: Set<String> {
        others.reduce(current.hiddenSessions) { $0.union($1.tree.hiddenSessions) }
    }

    /// Shows a session along with the rest of its set, or alone if it has none.
    /// What was showing is kept as it was, never replaced. A hidden pane comes back.
    mutating func select(session: String) {
        bringForward(session)
        current.show(session: session)
    }

    /// Shows the set a session is in, focused as it was left. If all its panes
    /// were closed, they all come back.
    mutating func showSet(containing session: String) {
        bringForward(session)
        if current.shownLeaves.isEmpty { current.showAll() }
    }

    /// Closes the focused pane. Its session keeps running, in its set if it has one.
    mutating func closeFocused() {
        let inSet = currentSetID != nil
        editCurrent { tree in
            if inSet {
                tree.hideFocused()
            } else {
                tree.closeFocused()
            }
        }
    }

    private mutating func bringForward(_ session: String) {
        if current.sessions.contains(session) { return }
        if let currentSetID { others.append(SessionSet(id: currentSetID, tree: current)) }
        if let index = others.firstIndex(where: { $0.tree.sessions.contains(session) }) {
            let set = others.remove(at: index)
            current = set.tree
            currentSetID = set.id
        } else {
            current = PaneTree()
            currentSetID = nil
        }
    }

    /// Adds a new session next to the focused pane, in the set the daemon put it
    /// in. With nothing showing, the session simply shows alone. One already here
    /// (the daemon's set reached us first) just gets the focus.
    mutating func split(_ direction: SplitDirection, session: String, setID: String? = nil) {
        if current.sessions.contains(session) {
            current.show(session: session)
            return
        }
        guard current.focusedSession != nil else {
            current.show(session: session)
            return
        }
        current.split(direction, session: session)
        if currentSetID == nil { currentSetID = setID ?? UUID().uuidString }
    }

    /// Follows the daemon's sets: each set's id and its sessions, in order. A
    /// set that ended leaves its focused session showing alone; a member that
    /// left loses its pane; one that joined gets a pane beside the member before
    /// it; a set made elsewhere gets panes of its own, aside.
    mutating func reconcile(sets: [String: [String]]) {
        others.removeAll { sets[$0.id] == nil }
        if let id = currentSetID, sets[id] == nil {
            let keep = current.focusedSession ?? current.leaves.compactMap(\.session).first
            current.keep(sessions: keep.map { [$0] } ?? [])
            current.showAll()
            currentSetID = nil
        }
        // A session showing alone that's now in a set: show that set.
        if currentSetID == nil, let shown = current.sessions.first,
            let (id, _) = sets.first(where: { $0.value.contains(shown) }) {
            if let index = others.firstIndex(where: { $0.id == id }) {
                current = others.remove(at: index).tree
            }
            currentSetID = id
        }
        if let id = currentSetID, let members = sets[id] { current.fit(members) }
        for index in others.indices {
            if let members = sets[others[index].id] { others[index].tree.fit(members) }
        }
        let known = Set(others.map(\.id) + [currentSetID].compactMap { $0 })
        for id in sets.keys.sorted() where !known.contains(id) {
            var tree = PaneTree()
            tree.fit(sets[id] ?? [])
            others.append(SessionSet(id: id, tree: tree))
        }
        settleCurrent()
        removeOthersTooSmall()
    }

    /// The sets to make in the daemon from panes saved before it kept sets: each
    /// set's sessions that are still listed, in pane order, one set per host.
    /// hostOf gives a listed session's host ("" for this Mac), nil for one
    /// that's gone. The first host's set keeps the set's id.
    func setsToMove(hostOf: (String) -> String?) -> [(id: String, members: [String])] {
        let showing = currentSetID.map { [SessionSet(id: $0, tree: current)] } ?? []
        var out: [(id: String, members: [String])] = []
        for set in showing + others {
            var hosts: [String] = []
            var byHost: [String: [String]] = [:]
            for session in set.tree.leaves.compactMap(\.session) {
                guard let host = hostOf(session) else { continue }
                if byHost[host] == nil { hosts.append(host) }
                byHost[host, default: []].append(session)
            }
            for (index, host) in hosts.enumerated() {
                guard let members = byHost[host], members.count >= 2 else { continue }
                out.append((index == 0 ? set.id : UUID().uuidString, members))
            }
        }
        return out
    }

    /// The format the app saves now: panes only, with sets kept by the daemon.
    static let savedVersion = 2

    nonisolated private struct Saved: Codable {
        let version: Int
        let panes: PaneSets
    }

    /// The panes as saved now.
    func encodedForSaving() -> Data? { try? JSONEncoder().encode(Saved(version: Self.savedVersion, panes: self)) }

    /// Reads saved panes. fromBefore says they were saved before the daemon
    /// kept sets, so their sets still have to be moved there.
    static func decodeSaved(_ data: Data) -> (panes: PaneSets, fromBefore: Bool)? {
        if let saved = try? JSONDecoder().decode(Saved.self, from: data), saved.version >= savedVersion {
            return saved.panes.isValid() ? (saved.panes, false) : nil
        }
        return decode(data).map { ($0, true) }
    }

    /// Changes the panes showing now, which can end the set.
    mutating func editCurrent(_ edit: (inout PaneTree) -> Void) {
        edit(&current)
        settleCurrent()
    }

    /// Takes a session out of its set. It keeps running, on its own.
    mutating func moveOut(session: String) {
        if current.sessions.contains(session), currentSetID != nil {
            editCurrent { $0.remove(session: session) }
        } else if let index = others.firstIndex(where: { $0.tree.sessions.contains(session) }) {
            others[index].tree.remove(session: session)
            removeOthersTooSmall()
        }
    }

    /// Drops the panes of sessions that are gone.
    mutating func keep(sessions: Set<String>) {
        editCurrent { $0.keep(sessions: sessions) }
        for index in others.indices { others[index].tree.keep(sessions: sessions) }
        removeOthersTooSmall()
    }

    func isValid() -> Bool {
        current.isValid() && others.allSatisfy { $0.tree.isValid() }
    }

    /// Reads saved panes, including the single tree an earlier build saved.
    static func decode(_ data: Data) -> PaneSets? {
        let decoder = JSONDecoder()
        if let saved = try? decoder.decode(PaneSets.self, from: data) {
            return saved.isValid() ? saved : nil
        }
        guard var tree = try? decoder.decode(PaneTree.self, from: data), tree.isValid() else { return nil }
        tree.removeEmptyPanes()
        var sets = PaneSets()
        sets.current = tree
        sets.currentSetID = tree.leaves.count > 1 ? UUID().uuidString : nil
        return sets
    }

    /// The list of sessions with each set's members together, where the first of
    /// them would come, in pane order.
    static func rows(order: [String], sets: [[String]]) -> [(id: String, link: SetLink)] {
        let listed = Set(order)
        var rows: [(id: String, link: SetLink)] = []
        var done = Set<String>()
        for id in order where !done.contains(id) {
            let set = sets.first { $0.contains(id) }?.filter(listed.contains) ?? [id]
            for (index, member) in set.enumerated() {
                done.insert(member)
                let link: SetLink = set.count < 2 ? .none
                    : index == 0 ? .first
                    : index == set.count - 1 ? .last : .middle
                rows.append((member, link))
            }
        }
        return rows
    }

    /// A set with one session left ends, and that session shows. A set with
    /// nothing showing is put aside, leaving an empty pane.
    private mutating func settleCurrent() {
        if current.leaves.count < 2 {
            current.showAll()
            currentSetID = nil
        } else if let currentSetID, current.shownLeaves.isEmpty {
            others.append(SessionSet(id: currentSetID, tree: current))
            current = PaneTree()
            self.currentSetID = nil
        }
    }

    private mutating func removeOthersTooSmall() {
        others.removeAll { $0.tree.leaves.count < 2 }
    }
}

extension PaneTree {
    /// Makes the panes match a set's sessions: a pane for each, in order where
    /// it's new, and none for others. Panes keep their places and sizes.
    mutating func fit(_ members: [String]) {
        let wanted = Set(members)
        for session in sessions where !wanted.contains(session) { remove(session: session) }
        for (index, session) in members.enumerated() where !sessions.contains(session) {
            add(session: session, after: index > 0 ? members[index - 1] : nil)
        }
        if focusedSession == nil, let first = shownLeaves.first(where: { $0.session != nil }) { focus(first.id) }
    }
}
