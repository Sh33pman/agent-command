import AppKit

/// The area that shows sessions in panes, split as a PaneTree says. Each session
/// keeps its surface while the app runs, so switching back is instant and the
/// host keeps its client.
final class PaneView: NSView {
    private(set) var sets = PaneSets()
    /// The panes showing now.
    var tree: PaneTree { sets.current }
    private var surfaces: [String: SurfaceView] = [:]
    private var slots: [String: PaneSlot] = [:]
    private var dividers: [PaneDivider] = []

    /// Looks up a listed session, to show its name or see if it has stopped.
    var session: (String) -> Session? = { _ in nil }
    /// Called when the panes change, to save them and update the sidebar.
    var onChange: ((PaneSets) -> Void)?
    /// Called to activate a stopped session shown in a pane.
    var onActivate: ((Session) -> Void)?
    /// Makes the line over a pane for a session; tapping it calls the closure.
    var makeHeader: ((String, @escaping () -> Void) -> NSView)?

    /// The surface in the focused pane.
    var shown: SurfaceView? { slots[tree.focused]?.surface }

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        applyTheme()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    func applyTheme() {
        layer?.backgroundColor = AppTheme.shared.background.cgColor
        slots.values.forEach { $0.applyTheme() }
        dividers.forEach { $0.needsDisplay = true }
    }

    // MARK: Changes

    /// Shows a session with the rest of its set, or alone if it has none.
    func select(sessionID id: String) {
        change { $0.select(session: id) }
    }

    /// Shows the set a session is in, focused as it was left.
    func showSet(containing id: String) {
        change { $0.showSet(containing: id) }
    }

    func split(_ direction: SplitDirection, session: String) {
        change { $0.split(direction, session: session) }
    }

    /// Takes a session out of its set. It keeps running.
    func moveOut(sessionID id: String) {
        change { $0.moveOut(session: id) }
    }

    /// Closes the focused pane. Its session keeps running.
    func closeFocused() {
        change { $0.closeFocused() }
    }

    func focus(_ direction: FocusDirection) {
        change { $0.editCurrent { $0.focus(direction) } }
    }

    func equalize() {
        change { $0.editCurrent { $0.equalize() } }
    }

    /// Brings back saved panes.
    func restore(_ saved: PaneSets) {
        change { $0 = saved }
    }

    /// Follows the listed sessions: drops what's gone, and picks up status changes.
    func sessionsChanged(listed ids: Set<String>) {
        for (id, surface) in surfaces where !ids.contains(id) {
            surfaces[id] = nil
            surface.removeFromSuperview()
        }
        var next = sets
        next.keep(sessions: ids)
        apply(next, takeFocus: false)
    }

    private func change(_ edit: (inout PaneSets) -> Void) {
        var next = sets
        edit(&next)
        apply(next, takeFocus: true)
    }

    private func apply(_ next: PaneSets, takeFocus: Bool) {
        let changed = next != sets
        sets = next
        sync()
        if takeFocus { focusTerminal() }
        if changed { onChange?(sets) }
    }

    /// Puts the keyboard in the focused pane.
    func focusTerminal() {
        if let slot = slots[tree.focused] {
            window?.makeFirstResponder(slot.surface ?? slot)
        }
    }

    // MARK: Showing the tree

    private func sync() {
        let leaves = tree.shownLeaves
        let ids = Set(leaves.map(\.id))
        for (id, slot) in slots where !ids.contains(id) {
            slot.setSurface(nil)
            slot.removeFromSuperview()
            slots[id] = nil
        }
        for leaf in leaves where slots[leaf.id] == nil {
            let id = leaf.id
            let slot = PaneSlot()
            slot.onClick = { [weak self] in self?.paneClicked(id) }
            addSubview(slot, positioned: .below, relativeTo: dividers.first)
            slots[id] = slot
        }
        placeSlots()
        for leaf in leaves {
            show(leaf.session, in: slots[leaf.id])
            slots[leaf.id]?.setDimmed(leaves.count > 1 && leaf.id != tree.focused)
        }
        needsLayout = true
    }

    private func show(_ sessionID: String?, in slot: PaneSlot?) {
        guard let slot else { return }
        slot.setHeader(for: sessionID.flatMap { session($0) == nil ? nil : $0 }, make: makeHeader)
        guard let sessionID, let session = session(sessionID) else {
            slot.setSurface(nil)
            slot.setMessage("Pick a session, or press ⌘N or ⌘T for a new one")
            return
        }
        if session.isStopped && surfaces[sessionID] == nil {
            slot.setSurface(nil)
            slot.setMessage("\(session.displayName) has stopped. Click to resume it")
            return
        }
        slot.setSurface(surfaces[sessionID] ?? makeSurface(sessionID, in: slot))
    }

    private func paneClicked(_ id: String) {
        change { $0.editCurrent { $0.focus(id) } }
        if let sessionID = tree.focusedSession, surfaces[sessionID] == nil,
           let session = session(sessionID), session.isStopped {
            onActivate?(session)
        }
    }

    private func makeSurface(_ id: String, in slot: PaneSlot) -> SurfaceView {
        let surface = SurfaceView(
            frame: slot.bounds,
            scale: window?.backingScaleFactor ?? 2,
            command: "'\(DaemonPaths.acmd)' host attach --session \(id)"
        )
        surface.onClose = { [weak self, weak surface] in
            guard let self, self.surfaces[id] === surface else { return }
            self.surfaces[id] = nil
            self.sync()
        }
        surface.onFocused = { [weak self] in self?.surfaceFocused(id) }
        surfaces[id] = surface
        return surface
    }

    /// A click in a terminal gives it the keyboard: the panes follow.
    private func surfaceFocused(_ sessionID: String) {
        guard let leaf = tree.shownLeaves.first(where: { $0.session == sessionID }), leaf.id != tree.focused else {
            return
        }
        var next = sets
        next.editCurrent { $0.focus(leaf.id) }
        apply(next, takeFocus: false)
    }

    // MARK: Layout

    override func layout() {
        super.layout()
        placeSlots()
    }

    private func placeSlots() {
        let (panes, lines) = tree.layout(in: safeAreaRect, thickness: 1)
        for pane in panes {
            slots[pane.id]?.frame = pane.rect
        }
        if dividers.count != lines.count {
            dividers.forEach { $0.removeFromSuperview() }
            dividers = lines.map { _ in
                let divider = PaneDivider()
                addSubview(divider)
                return divider
            }
        }
        for (divider, line) in zip(dividers, lines) {
            divider.place(line) { [weak self] ratio in
                self?.dividerMoved(path: line.path, ratio: ratio)
            }
        }
    }

    private func dividerMoved(path: [Bool], ratio: Double) {
        sets.editCurrent { $0.setRatio(at: path, to: ratio) }
        placeSlots()
        onChange?(sets)
    }
}

/// One pane: a surface, or a message when there's nothing to show.
private final class PaneSlot: NSView {
    private(set) var surface: SurfaceView?
    private var header: NSView?
    private var headerSession: String?
    private let message = NSTextField(labelWithString: "")
    private let dim = PassthroughView()
    var onClick: (() -> Void)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        message.font = AppFont.appKit(13)
        message.alignment = .center
        message.lineBreakMode = .byTruncatingTail
        message.translatesAutoresizingMaskIntoConstraints = false
        addSubview(message)
        NSLayoutConstraint.activate([
            message.centerXAnchor.constraint(equalTo: centerXAnchor),
            message.centerYAnchor.constraint(equalTo: centerYAnchor),
            message.widthAnchor.constraint(lessThanOrEqualTo: widthAnchor, constant: -16),
        ])
        dim.wantsLayer = true
        dim.layer?.backgroundColor = NSColor.black.withAlphaComponent(0.25).cgColor
        dim.autoresizingMask = [.width, .height]
        dim.isHidden = true
        addSubview(dim)
        applyTheme()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("not used") }

    override var acceptsFirstResponder: Bool { true }

    func applyTheme() {
        message.textColor = AppTheme.shared.foreground.withAlphaComponent(0.5)
    }

    func setMessage(_ text: String) {
        message.stringValue = text
    }

    /// The line over the pane, for the session it shows (nil: none).
    func setHeader(for sessionID: String?, make: ((String, @escaping () -> Void) -> NSView)?) {
        guard sessionID != headerSession else { return }
        header?.removeFromSuperview()
        header = nil
        headerSession = sessionID
        if let sessionID, let make {
            let view = make(sessionID) { [weak self] in self?.onClick?() }
            view.autoresizingMask = [.width, .minYMargin]
            addSubview(view, positioned: .below, relativeTo: dim)
            header = view
        }
        placeContent()
    }

    /// Below the header, if there is one.
    private var contentFrame: NSRect {
        guard header != nil else { return bounds }
        var rect = bounds
        rect.size.height = max(0, rect.height - PaneHeader.height)
        return rect
    }

    private func placeContent() {
        header?.frame = NSRect(x: 0, y: bounds.height - PaneHeader.height, width: bounds.width, height: PaneHeader.height)
        surface?.frame = contentFrame
    }

    override func layout() {
        super.layout()
        placeContent()
    }

    func setSurface(_ next: SurfaceView?) {
        guard next !== surface else { return }
        surface?.setVisible(false)
        surface?.removeFromSuperview()
        surface = next
        message.isHidden = next != nil
        guard let next else { return }
        next.frame = contentFrame
        next.autoresizingMask = [.width, .height]
        addSubview(next, positioned: .below, relativeTo: dim)
        next.setVisible(true)
    }

    func setDimmed(_ dimmed: Bool) {
        dim.frame = bounds
        dim.isHidden = !dimmed
    }

    override func mouseDown(with event: NSEvent) {
        onClick?()
    }
}

/// Lets clicks through, to the terminal under it.
private final class PassthroughView: NSView {
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

/// The line between two panes, which can be dragged.
private final class PaneDivider: NSView {
    private static let grabMargin: CGFloat = 3
    private var line: DividerFrame?
    private var onDrag: ((Double) -> Void)?

    func place(_ line: DividerFrame, onDrag: @escaping (Double) -> Void) {
        self.line = line
        self.onDrag = onDrag
        // Wider than the line it draws, so it's easy to grab.
        let margin = Self.grabMargin
        frame = line.direction == .right
            ? line.rect.insetBy(dx: -margin, dy: 0)
            : line.rect.insetBy(dx: 0, dy: -margin)
        needsDisplay = true
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let line else { return }
        AppTheme.shared.line.setFill()
        let margin = Self.grabMargin
        let drawn = line.direction == .right
            ? NSRect(x: margin, y: 0, width: line.rect.width, height: bounds.height)
            : NSRect(x: 0, y: margin, width: bounds.width, height: line.rect.height)
        drawn.fill()
    }

    override func resetCursorRects() {
        guard let line else { return }
        addCursorRect(bounds, cursor: line.direction == .right ? .resizeLeftRight : .resizeUpDown)
    }

    override func mouseDragged(with event: NSEvent) {
        guard let line, let parent = superview else { return }
        let point = parent.convert(event.locationInWindow, from: nil)
        let ratio = line.direction == .right
            ? (point.x - line.parent.minX) / line.parent.width
            : (line.parent.maxY - point.y) / line.parent.height
        onDrag?(Double(ratio))
    }

    override func mouseDown(with event: NSEvent) {}
}
