import AppKit
import Carbon
import Observation
import SwiftUI

/// What makes the popup open by itself.
enum PopupOpens: String, CaseIterable, Identifiable {
    case needsYou, everything, never

    var id: String { rawValue }

    var label: String {
        switch self {
        case .needsYou: "Questions, permissions and errors"
        case .everything: "Everything, including finished turns"
        case .never: "Only when I ask"
        }
    }

    func opens(forItem kind: String) -> Bool {
        switch self {
        case .needsYou: kind == "question" || kind == "permission" || kind == "error"
        case .everything: true
        case .never: false
        }
    }

    /// The same, for a notification's kind: its names differ from an item's.
    func opens(forNotice kind: String) -> Bool {
        switch self {
        case .needsYou: kind == "waiting" || kind == "error"
        case .everything: kind == "waiting" || kind == "error" || kind == "done"
        case .never: false
        }
    }
}

/// The popup's settings, which the status bar and the command palette change.
@Observable
final class PopupSettings {
    static let shared = PopupSettings()

    private static let enabledKey = "popup.enabled"
    private static let opensKey = "popup.opens"

    var enabled: Bool {
        didSet { UserDefaults.standard.set(enabled, forKey: Self.enabledKey) }
    }

    var opens: PopupOpens {
        didSet { UserDefaults.standard.set(opens.rawValue, forKey: Self.opensKey) }
    }

    private init() {
        enabled = UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool ?? true
        opens = UserDefaults.standard.string(forKey: Self.opensKey).flatMap(PopupOpens.init) ?? .everything
    }
}

/// A panel that can have the keyboard without bringing the app forward.
private final class PopupPanel: NSPanel {
    var onBecomeKey: () -> Void = {}

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }

    override func becomeKey() {
        super.becomeKey()
        onBecomeKey()
    }
}

/// A click on an unfocused panel acts straight away.
private final class PopupHostingView<Content: View>: NSHostingView<Content> {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
}

/// Shows the Inbox over whatever app you're using. It appears without taking the
/// keyboard, so typing can't answer a prompt by accident. A click or the shortcut
/// gives it the keyboard.
final class PopupController {
    private let store: SessionStore
    private var panel: PopupPanel?
    private var dim: NSWindow?
    private var model: InboxModel?

    /// Called before showing: the window's Inbox can't be open at the same time.
    var willShow: () -> Void = {}
    /// Sets up what the Inbox does in the app.
    var configure: (InboxModel) -> Void = { _ in }

    init(store: SessionStore) {
        self.store = store
    }

    var isOpen: Bool { panel != nil }
    var hasKeyboard: Bool { panel?.isKeyWindow == true }

    /// Takes new items: opens if one of them should, unless you're in the app.
    func itemsArrived(_ items: [InboxItem]) {
        let settings = PopupSettings.shared
        guard settings.enabled, !NSApp.isActive, !isOpen,
            let first = items.first(where: { settings.opens.opens(forItem: $0.kind) })
        else { return }
        show(focus: false, cursor: first)
    }

    /// Whether the popup shows this notification instead of a banner.
    func replacesBanner(noticeKind: String) -> Bool {
        let settings = PopupSettings.shared
        return settings.enabled && !NSApp.isActive && settings.opens.opens(forNotice: noticeKind)
    }

    func show(focus: Bool, cursor: InboxItem? = nil) {
        guard panel == nil else {
            if focus { panel?.makeKeyAndOrderFront(nil) }
            return
        }
        willShow()
        let mouse = NSEvent.mouseLocation
        guard let screen = NSScreen.screens.first(where: { $0.frame.contains(mouse) }) ?? NSScreen.main else { return }

        let model = InboxModel(store: store, filter: .queue, showsCursor: true)
        configure(model)
        model.close = { [weak self] in self?.close() }
        model.onQueueEmptied = { [weak self] in self?.close() }

        // The dimming lets clicks through, so what's behind it keeps working.
        let dim = NSWindow(contentRect: screen.frame, styleMask: .borderless, backing: .buffered, defer: false)
        dim.isReleasedWhenClosed = false
        dim.isOpaque = false
        dim.backgroundColor = NSColor.black.withAlphaComponent(0.5)
        dim.ignoresMouseEvents = true
        dim.level = .floating
        dim.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        let area = screen.visibleFrame
        let size = NSSize(width: (area.width * 2 / 3).rounded(), height: (area.height * 2 / 3).rounded())
        let frame = NSRect(x: area.midX - size.width / 2, y: area.midY - size.height / 2, width: size.width, height: size.height)
        let panel = PopupPanel(contentRect: frame, styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.becomesKeyOnlyIfNeeded = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.contentView = PopupHostingView(rootView: PopupInboxView(model: model))
        panel.onBecomeKey = { [weak model] in model?.markShownRead() }
        model.canMarkRead = { [weak panel] in panel?.isKeyWindow == true }

        dim.orderFrontRegardless()
        if focus { panel.makeKeyAndOrderFront(nil) } else { panel.orderFrontRegardless() }
        model.start()
        if let cursor { model.click(item: cursor) }
        self.dim = dim
        self.panel = panel
        self.model = model
    }

    func close() {
        model?.stop()
        model = nil
        panel?.orderOut(nil)
        panel = nil
        dim?.orderOut(nil)
        dim = nil
    }
}

/// A shortcut that works from any app. Carbon's hot keys need no permission.
final class SystemHotKey {
    private static var action: () -> Void = {}
    private var ref: EventHotKeyRef?

    /// ⌃⌘O.
    init(action: @escaping () -> Void) {
        Self.action = action
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, _, _ in
            DispatchQueue.main.async { SystemHotKey.action() }
            return noErr
        }, 1, &spec, nil, nil)
        let id = EventHotKeyID(signature: OSType(0x41434D44), id: 1) // "ACMD"
        let status = RegisterEventHotKey(
            UInt32(kVK_ANSI_O), UInt32(cmdKey | controlKey), id, GetApplicationEventTarget(), 0, &ref)
        if status != noErr { NSLog("popup shortcut: couldn't register (%d)", status) }
    }
}
