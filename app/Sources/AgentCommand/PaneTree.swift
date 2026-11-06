import CoreGraphics
import Foundation

enum SplitDirection: String, Codable {
    case right, down
}

enum FocusDirection {
    case left, right, up, down
}

indirect enum PaneNode: Codable, Equatable {
    case leaf(id: String, session: String?)
    case split(direction: SplitDirection, ratio: Double, first: PaneNode, second: PaneNode)
}

/// Where a pane or a divider goes. y grows upward, as in AppKit.
struct PaneFrame: Equatable {
    let id: String
    let rect: CGRect
}

struct DividerFrame: Equatable {
    /// How to reach this split from the root: false is first, true is second.
    let path: [Bool]
    let direction: SplitDirection
    let rect: CGRect
    /// The area the split divides, to turn a drag into a ratio.
    let parent: CGRect
}

/// The layout of the panes: a tree of splits whose leaves each show at most one
/// session. This has no dependencies, so it can be checked on its own.
struct PaneTree: Codable, Equatable {
    static let minRatio = 0.1

    private(set) var root: PaneNode
    private(set) var focused: String
    /// Panes closed with their session kept in the tree. They take up no space.
    private(set) var hidden: Set<String> = []

    init(id: String = UUID().uuidString) {
        root = .leaf(id: id, session: nil)
        focused = id
    }

    private enum CodingKeys: String, CodingKey {
        case root, focused, hidden
    }

    /// Trees saved by earlier builds have no hidden panes.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        root = try container.decode(PaneNode.self, forKey: .root)
        focused = try container.decode(String.self, forKey: .focused)
        hidden = try container.decodeIfPresent(Set<String>.self, forKey: .hidden) ?? []
    }

    /// Every pane, shown or hidden.
    var leaves: [(id: String, session: String?)] { Self.leaves(root) }

    var shownLeaves: [(id: String, session: String?)] { leaves.filter { !hidden.contains($0.id) } }

    var focusedSession: String? { shownLeaves.first { $0.id == focused }?.session }

    var hiddenSessions: Set<String> { sessions.subtracting(visibleSessions) }

    /// Every session in the tree, shown or hidden.
    var sessions: Set<String> { Set(leaves.compactMap(\.session)) }

    var visibleSessions: Set<String> { Set(shownLeaves.compactMap(\.session)) }

    // MARK: Changes

    /// Shows a session in the focused pane. A session shows in one pane at a
    /// time, so one that's already here comes back if hidden, and gets the focus.
    mutating func show(session: String) {
        if let existing = leaves.first(where: { $0.session == session }) {
            hidden.remove(existing.id)
            focused = existing.id
            return
        }
        let target = focused
        root = Self.replacing(target, in: root) { _ in .leaf(id: target, session: session) }
    }

    /// Splits the focused pane. The new pane shows the session and gets the focus.
    mutating func split(_ direction: SplitDirection, session: String, newID: String = UUID().uuidString) {
        let target = focused
        root = Self.replacing(target, in: root) { leaf in
            .split(direction: direction, ratio: 0.5, first: leaf, second: .leaf(id: newID, session: session))
        }
        focused = newID
    }

    /// Closes the focused pane, leaving its session running. The last pane
    /// stays, empty.
    mutating func closeFocused() {
        close(focused)
    }

    /// Hides the focused pane, keeping its session in the tree. The focus moves
    /// to a neighbour, if any pane is left showing.
    mutating func hideFocused() {
        let index = shownLeaves.firstIndex { $0.id == focused } ?? 0
        hidden.insert(focused)
        moveFocus(near: index)
    }

    mutating func showAll() {
        hidden = []
    }

    private mutating func close(_ id: String) {
        guard leaves.count > 1 else {
            root = Self.replacing(id, in: root) { _ in .leaf(id: id, session: nil) }
            hidden = []
            return
        }
        let index = shownLeaves.firstIndex { $0.id == id } ?? 0
        root = Self.removing(id, from: root) ?? root
        hidden.remove(id)
        if focused == id { moveFocus(near: index) }
    }

    /// Focuses the showing pane at an index, or the nearest one. With none
    /// showing, any pane will do: a tree always has a focused pane.
    private mutating func moveFocus(near index: Int) {
        let showing = shownLeaves.map(\.id)
        if showing.isEmpty {
            focused = leaves.first?.id ?? focused
        } else {
            focused = showing[min(index, showing.count - 1)]
        }
    }

    /// Closes the pane showing a session.
    mutating func remove(session: String) {
        if let leaf = leaves.first(where: { $0.session == session }) { close(leaf.id) }
    }

    /// Closes panes showing nothing, as long as another pane is left.
    mutating func removeEmptyPanes() {
        for leaf in leaves where leaf.session == nil && leaves.count > 1 { close(leaf.id) }
    }

    /// Closes the panes of sessions that are gone.
    mutating func keep(sessions: Set<String>) {
        for leaf in leaves {
            if let session = leaf.session, !sessions.contains(session) {
                close(leaf.id)
            }
        }
    }

    mutating func focus(_ id: String) {
        if shownLeaves.contains(where: { $0.id == id }) { focused = id }
    }

    /// Moves the focus to the nearest pane in a direction, if there is one.
    mutating func focus(_ direction: FocusDirection) {
        let frames = layout(in: CGRect(x: 0, y: 0, width: 1000, height: 1000), thickness: 0).panes
        guard let from = frames.first(where: { $0.id == focused })?.rect else { return }
        let candidates = frames.filter { frame in
            guard frame.id != focused else { return false }
            let r = frame.rect
            switch direction {
            case .left: return r.maxX <= from.minX && overlap(r.minY...r.maxY, from.minY...from.maxY)
            case .right: return r.minX >= from.maxX && overlap(r.minY...r.maxY, from.minY...from.maxY)
            case .up: return r.minY >= from.maxY && overlap(r.minX...r.maxX, from.minX...from.maxX)
            case .down: return r.maxY <= from.minY && overlap(r.minX...r.maxX, from.minX...from.maxX)
            }
        }
        let nearest = candidates.min { a, b in
            distance(from, a.rect, direction) < distance(from, b.rect, direction)
        }
        if let nearest { focused = nearest.id }
    }

    mutating func setRatio(at path: [Bool], to ratio: Double) {
        root = Self.settingRatio(root, path[...], max(Self.minRatio, min(1 - Self.minRatio, ratio)))
    }

    mutating func equalize() {
        root = Self.equalized(root)
    }

    /// Checks a tree read back from storage: it must have the focused pane.
    func isValid() -> Bool { leaves.contains { $0.id == focused } }

    // MARK: Layout

    func layout(in rect: CGRect, thickness: CGFloat) -> (panes: [PaneFrame], dividers: [DividerFrame]) {
        var panes: [PaneFrame] = []
        var dividers: [DividerFrame] = []
        place(root, in: rect, thickness: thickness, path: [], &panes, &dividers)
        return (panes, dividers)
    }

    private func place(
        _ node: PaneNode, in rect: CGRect, thickness: CGFloat, path: [Bool],
        _ panes: inout [PaneFrame], _ dividers: inout [DividerFrame]
    ) {
        switch node {
        case .leaf(let id, _):
            panes.append(PaneFrame(id: id, rect: rect))
        case .split(let direction, let ratio, let first, let second):
            // A side with nothing showing gives the other side all its space.
            guard isShowing(first) else {
                place(second, in: rect, thickness: thickness, path: path + [true], &panes, &dividers)
                return
            }
            guard isShowing(second) else {
                place(first, in: rect, thickness: thickness, path: path + [false], &panes, &dividers)
                return
            }
            var firstRect = rect, secondRect = rect, line = rect
            switch direction {
            case .right:
                let width = ((rect.width - thickness) * ratio).rounded()
                firstRect.size.width = width
                line.origin.x = rect.minX + width
                line.size.width = thickness
                secondRect.origin.x = line.maxX
                secondRect.size.width = rect.maxX - line.maxX
            case .down:
                // The first pane is on top, and y grows upward.
                let height = ((rect.height - thickness) * ratio).rounded()
                firstRect.origin.y = rect.maxY - height
                firstRect.size.height = height
                line.origin.y = firstRect.minY - thickness
                line.size.height = thickness
                secondRect.size.height = line.minY - rect.minY
            }
            dividers.append(DividerFrame(path: path, direction: direction, rect: line, parent: rect))
            place(first, in: firstRect, thickness: thickness, path: path + [false], &panes, &dividers)
            place(second, in: secondRect, thickness: thickness, path: path + [true], &panes, &dividers)
        }
    }

    // MARK: Tree helpers

    private func isShowing(_ node: PaneNode) -> Bool {
        Self.leaves(node).contains { !hidden.contains($0.id) }
    }

    private func overlap(_ a: ClosedRange<CGFloat>, _ b: ClosedRange<CGFloat>) -> Bool {
        a.lowerBound < b.upperBound && b.lowerBound < a.upperBound
    }

    private func distance(_ from: CGRect, _ to: CGRect, _ direction: FocusDirection) -> CGFloat {
        switch direction {
        case .left: from.minX - to.maxX
        case .right: to.minX - from.maxX
        case .up: to.minY - from.maxY
        case .down: from.minY - to.maxY
        }
    }

    private static func leaves(_ node: PaneNode) -> [(id: String, session: String?)] {
        switch node {
        case .leaf(let id, let session): [(id, session)]
        case .split(_, _, let first, let second): leaves(first) + leaves(second)
        }
    }

    private static func replacing(_ id: String, in node: PaneNode, with change: (PaneNode) -> PaneNode) -> PaneNode {
        switch node {
        case .leaf(let leaf, _):
            return leaf == id ? change(node) : node
        case .split(let direction, let ratio, let first, let second):
            return .split(
                direction: direction, ratio: ratio,
                first: replacing(id, in: first, with: change),
                second: replacing(id, in: second, with: change))
        }
    }

    /// nil when the whole node was the pane being removed.
    private static func removing(_ id: String, from node: PaneNode) -> PaneNode? {
        switch node {
        case .leaf(let leaf, _):
            return leaf == id ? nil : node
        case .split(let direction, let ratio, let first, let second):
            guard let keptFirst = removing(id, from: first) else { return second }
            guard let keptSecond = removing(id, from: second) else { return first }
            return .split(direction: direction, ratio: ratio, first: keptFirst, second: keptSecond)
        }
    }

    private static func settingRatio(_ node: PaneNode, _ path: ArraySlice<Bool>, _ value: Double) -> PaneNode {
        guard case .split(let direction, let ratio, let first, let second) = node else { return node }
        guard let step = path.first else {
            return .split(direction: direction, ratio: value, first: first, second: second)
        }
        return .split(
            direction: direction, ratio: ratio,
            first: step ? first : settingRatio(first, path.dropFirst(), value),
            second: step ? settingRatio(second, path.dropFirst(), value) : second)
    }

    private static func equalized(_ node: PaneNode) -> PaneNode {
        guard case .split(let direction, _, let first, let second) = node else { return node }
        return .split(direction: direction, ratio: 0.5, first: equalized(first), second: equalized(second))
    }
}
