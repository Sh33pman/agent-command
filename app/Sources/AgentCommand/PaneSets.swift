import Foundation

/// Sessions created together with pane splits. It has no name, only an id.
struct SessionSet: Codable, Equatable {
    let id: String
    var tree: PaneTree
}

/// How a row in the session list connects to the rows around it.
enum SetLink: Equatable {
    case none, first, middle, last
}

/// What the panes show, and the sets that aren't showing. A session is in one set
/// at most, and a set has at least two sessions. This has no dependencies, so it
/// can be checked on its own.
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

    /// Adds a new session next to the focused pane, creating a set if there isn't
    /// one yet. With nothing showing, the session simply shows alone.
    mutating func split(_ direction: SplitDirection, session: String, setID: String = UUID().uuidString) {
        guard current.focusedSession != nil else {
            current.show(session: session)
            return
        }
        current.split(direction, session: session)
        if currentSetID == nil { currentSetID = setID }
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
