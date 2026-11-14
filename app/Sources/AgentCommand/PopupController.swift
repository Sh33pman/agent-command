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

/// How the popup appears.
enum ReplyPopupStyle: String, CaseIterable, Identifiable {
    /// Opens over everything, and drops behind when you click something else.
    case window
    /// A panel floating over a dimmed screen, which stays in front of every app.
    case overlay

    var id: String { rawValue }

    var label: String {
        switch self {
        case .window: "Window that can go behind"
        case .overlay: "Always on top, dimmed"
        }
    }
}

/// The popup's settings, which the status bar and the command palette change.
@Observable
final class PopupSettings {
    static let shared = PopupSettings()

    private static let enabledKey = "popup.enabled"
    private static let opensKey = "popup.opens"
    private static let autoPlayKey = "popup.autoplay"
    private static let styleKey = "popup.style"

    var enabled: Bool {
        didSet { UserDefaults.standard.set(enabled, forKey: Self.enabledKey) }
    }

    var opens: PopupOpens {
        didSet { UserDefaults.standard.set(opens.rawValue, forKey: Self.opensKey) }
    }

    /// Whether the popup reads each message as it's shown.
    var autoPlay: Bool {
        didSet { UserDefaults.standard.set(autoPlay, forKey: Self.autoPlayKey) }
    }

    /// Takes effect the next time it opens.
    var style: ReplyPopupStyle {
        didSet { UserDefaults.standard.set(style.rawValue, forKey: Self.styleKey) }
    }

    private init() {
        style = UserDefaults.standard.string(forKey: Self.styleKey).flatMap(ReplyPopupStyle.init) ?? .window
        autoPlay = UserDefaults.standard.bool(forKey: Self.autoPlayKey)
        enabled = UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool ?? true
        opens = UserDefaults.standard.string(forKey: Self.opensKey).flatMap(PopupOpens.init) ?? .everything
    }
}

/// A panel that can have the keyboard without bringing the app forward.
private final class PopupPanel: NSPanel {
    var onBecomeKey: () -> Void = {}
    var onClose: () -> Void = {}

    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { false }

    override func becomeKey() {
        super.becomeKey()
        onBecomeKey()
    }

    override func close() {
        super.close()
        onClose()
    }
}

/// A click on an unfocused panel acts straight away.
private final class PopupHostingView<Content: View>: NSHostingView<Content> {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
}

/// Shows the queue while you're using another app, in the style set in PopupSettings.
/// It appears without taking the keyboard, so typing can't answer a prompt by accident.
/// A click or the shortcut gives it the keyboard.
final class PopupController {
    private let store: SessionStore
    private var panel: NSWindow?
    private var dim: NSWindow?
    /// See a click elsewhere, so the window can drop behind.
    private var clickMonitors: [Any] = []
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
        guard settings.enabled, !store.isInMainWindow,
            let first = items.first(where: { settings.opens.opens(forItem: $0.kind) })
        else { return }
        if let panel {
            // It may have gone behind another app.
            if !panel.isMiniaturized { raise(panel) }
        } else {
            show(focus: false, cursor: first)
        }
    }

    /// The app came to the front. The overlay is for when you're elsewhere; a window
    /// is yours to keep open.
    func appBecameActive() {
        if dim != nil { close() }
    }

    /// Whether the popup shows this notification instead of a banner.
    func replacesBanner(noticeKind: String) -> Bool {
        let settings = PopupSettings.shared
        // Minimized, it wouldn't show you anything.
        return settings.enabled && !store.isInMainWindow && panel?.isMiniaturized != true
            && settings.opens.opens(forNotice: noticeKind)
    }

    func show(focus: Bool, cursor: InboxItem? = nil) {
        if let panel {
            if focus { bringForward(panel) }
            return
        }
        willShow()
        let mouse = NSEvent.mouseLocation
        guard let screen = NSScreen.screens.first(where: { $0.frame.contains(mouse) }) ?? NSScreen.main else { return }

        let model = InboxModel(store: store, filter: .queue, showsCursor: true)
        configure(model)
        model.close = { [weak self] in self?.close() }
        model.onQueueEmptied = { [weak self] in self?.close() }

        let overlay = PopupSettings.shared.style == .overlay
        let hosting = PopupHostingView(rootView: PopupInboxView(model: model))
        // The window's size is its own: SwiftUI would fit it to the content, and stop
        // it being resized.
        hosting.sizingOptions = []
        let area = screen.visibleFrame
        let size = NSSize(width: (area.width * 2 / 3).rounded(), height: (area.height * 2 / 3).rounded())
        let frame = NSRect(x: area.midX - size.width / 2, y: area.midY - size.height / 2, width: size.width, height: size.height)
        let shown = overlay
            ? makeOverlay(frame: frame, screen: screen, content: hosting, model: model)
            : makeWindow(frame: frame, content: hosting, model: model)
        model.canMarkRead = { [weak shown] in shown?.isKeyWindow == true }
        model.handlesKey = { [weak shown] event in event.window === shown }

        if focus { bringForward(shown) } else { raise(shown) }
        model.start(cursor: cursor)
        panel = shown
        self.model = model
    }

    /// Gives it the keyboard, without bringing the app forward.
    private func bringForward(_ shown: NSWindow) {
        if shown.isMiniaturized { shown.deminiaturize(nil) }
        shown.level = .floating
        shown.makeKeyAndOrderFront(nil)
    }

    /// Puts it over everything, without the keyboard.
    private func raise(_ shown: NSWindow) {
        shown.level = .floating
        shown.orderFrontRegardless()
    }

    private func makeWindow(frame: NSRect, content: NSView, model: InboxModel) -> NSWindow {
        let window = PopupPanel(
            contentRect: frame, styleMask: [.titled, .closable, .miniaturizable, .resizable, .nonactivatingPanel],
            backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.hidesOnDeactivate = false
        window.becomesKeyOnlyIfNeeded = false
        window.title = "Inbox - Agent Command"
        window.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        window.contentView = content
        window.contentMinSize = NSSize(width: 560, height: 320)
        // Two thirds of the screen, title bar included, until you resize it.
        window.setFrame(frame, display: false)
        window.setFrameAutosaveName("ReplyPopup")
        window.onBecomeKey = { [weak model, weak window] in
            window?.level = .floating
            model?.markShownRead()
        }
        window.onClose = { [weak self] in self?.close() }
        // A click on anything else lets that go in front. It's on every Space, so it
        // comes back the moment a new message does.
        let dropBehind = { [weak window] in
            window?.level = .normal
        }
        if let outside = NSEvent.addGlobalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown], handler: { _ in dropBehind() }) {
            clickMonitors.append(outside)
        }
        if let inside = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown], handler: { [weak window] event in
            if event.window !== window { dropBehind() }
            return event
        }) {
            clickMonitors.append(inside)
        }
        return window
    }

    private func makeOverlay(frame: NSRect, screen: NSScreen, content: NSView, model: InboxModel) -> NSWindow {
        // The dimming lets clicks through, so what's behind it keeps working.
        let dim = NSWindow(contentRect: screen.frame, styleMask: .borderless, backing: .buffered, defer: false)
        dim.isReleasedWhenClosed = false
        dim.isOpaque = false
        dim.backgroundColor = NSColor.black.withAlphaComponent(0.5)
        dim.ignoresMouseEvents = true
        dim.level = .floating
        dim.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        let panel = PopupPanel(contentRect: frame, styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
        panel.isReleasedWhenClosed = false
        panel.hidesOnDeactivate = false
        panel.isFloatingPanel = true
        panel.level = .floating
        panel.becomesKeyOnlyIfNeeded = false
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.contentView = content
        panel.onBecomeKey = { [weak model] in model?.markShownRead() }
        dim.orderFrontRegardless()
        self.dim = dim
        return panel
    }

    func close() {
        clickMonitors.forEach(NSEvent.removeMonitor)
        clickMonitors = []
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
    private static var actions: [UInt32: () -> Void] = [:]
    private static var installed = false
    private var ref: EventHotKeyRef?

    /// id tells hot keys apart; the keymap sets the key.
    init(id: UInt32, _ combo: KeyCombo, action: @escaping () -> Void) {
        Self.actions[id] = action
        Self.installHandler()
        let hotKey = EventHotKeyID(signature: OSType(0x41434D44), id: id) // "ACMD"
        let status = RegisterEventHotKey(
            UInt32(combo.key), UInt32(combo.carbonMods), hotKey, GetApplicationEventTarget(), 0, &ref)
        if status != noErr { NSLog("system shortcut %u: couldn't register (%d)", id, status) }
    }

    deinit {
        if let ref { UnregisterEventHotKey(ref) }
    }

    /// One handler for every hot key: it runs the one pressed.
    private static func installHandler() {
        guard !installed else { return }
        installed = true
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, event, _ in
            var id = EventHotKeyID()
            let status = GetEventParameter(
                event, EventParamName(kEventParamDirectObject), EventParamType(typeEventHotKeyID), nil,
                MemoryLayout<EventHotKeyID>.size, nil, &id)
            guard status == noErr else { return status }
            let pressed = id.id
            DispatchQueue.main.async { SystemHotKey.actions[pressed]?() }
            return noErr
        }, 1, &spec, nil, nil)
    }
}
