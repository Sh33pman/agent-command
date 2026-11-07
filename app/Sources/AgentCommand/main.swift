import AppKit
import SwiftUI

// Usage: AgentCommand [--session <id>], selecting that session at launch.

/// Writes why the app is going away, to find unexpected exits.
func trace(_ what: String) {
    let stack = Thread.callStackSymbols.prefix(14).joined(separator: "\n")
    FileHandle.standardError.write(Data("[\(Date())] \(what)\n\(stack)\n\n".utf8))
}

/// A window with the session sidebar beside the pane. Closing it only detaches:
/// sessions keep running in their hosts.
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private var window: NSWindow?
    private let store = SessionStore()
    private var pane: PaneView?
    private var lastFolder: URL?
    private var split: NSSplitViewController?
    private var popup: NSView?
    private var pendingPreview: DispatchWorkItem?
    private var pendingSave: DispatchWorkItem?
    private var restored = false
    /// The sidebar's state to return to when a popup closes, if it had the keyboard.
    private var popupReturn: (cursor: String?, marks: Set<String>)?
    private let sidebarKeys = SidebarKeyView()
    private let notifier = AppNotifier()
    private var inbox: InboxModel?
    private lazy var popupWindow = PopupController(store: store)
    private var hotKey: SystemHotKey?
    /// A clicked notification's session, if it arrived before the session list.
    private var pendingOpen: String?

    func applicationDidFinishLaunching(_ notification: Notification) {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1180, height: 720),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = "Agent Command"
        window.delegate = self
        window.titlebarAppearsTransparent = true
        window.center()

        let pane = PaneView(frame: NSRect(x: 0, y: 0, width: 920, height: 720))
        let paneController = NSViewController()
        paneController.view = pane
        self.pane = pane

        let sidebar = NSHostingController(
            rootView: SidebarView(
                store: store,
                keys: sidebarKeys,
                showCommands: { [weak self] in self?.toggleCommands() },
                archive: { [weak self] session in
                    guard let self else { return }
                    self.archive(self.store.actionTargets(forRow: session))
                },
                rename: { [weak self] session in
                    guard let self else { return }
                    self.showRename(self.store.actionTargets(forRow: session))
                },
                openInbox: { [weak self] session in self?.showInbox(.session(session.id)) },
                moveOut: { [weak self] session in self?.pane?.moveOut(sessionID: session.id) },
                openSet: { [weak self] session in self?.pane?.showSet(containing: session.id) },
                clickRow: { [weak self] session in self?.clickSidebarRow(session) },
                openRow: { [weak self] session in self?.openSidebarRow(session.id) }
            ))
        let split = NSSplitViewController()
        split.splitView = ThemedSplitView()
        split.splitView.isVertical = true
        // A plain item: a sidebar item would bring the system's translucency.
        let sidebarItem = NSSplitViewItem(viewController: sidebar)
        sidebarItem.minimumThickness = 200
        sidebarItem.maximumThickness = 360
        sidebarItem.canCollapse = true
        split.addSplitViewItem(sidebarItem)
        split.addSplitViewItem(NSSplitViewItem(viewController: paneController))
        // Popups go beside the split view, not in it: a split view lays out every
        // subview as a pane when the window resizes.
        let root = NSViewController()
        root.view = NSView(frame: NSRect(x: 0, y: 0, width: 1180, height: 720))
        root.addChild(split)
        var splitFrame = root.view.bounds
        splitFrame.origin.y = StatusBarView.height
        splitFrame.size.height -= StatusBarView.height
        split.view.frame = splitFrame
        split.view.autoresizingMask = [.width, .height]
        root.view.addSubview(split.view)
        let statusBar = NSHostingView(rootView: StatusBarView(
            notifier: notifier, store: store, openInbox: { [weak self] in self?.toggleInbox() }))
        statusBar.frame = NSRect(x: 0, y: 0, width: root.view.bounds.width, height: StatusBarView.height)
        statusBar.autoresizingMask = [.width]
        root.view.addSubview(statusBar)
        window.contentViewController = root
        self.split = split
        window.setContentSize(NSSize(width: 1180, height: 720))

        self.window = window
        applyTheme()
        ThemeStore.instance.onChange = { [weak self] in self?.applyTheme() }

        sidebarKeys.onFocus = { [weak store] focused in store?.setSidebarFocused(focused) }
        sidebarKeys.onKey = { [weak self] key in self?.sidebarKey(key) }
        store.onSelect = { [weak self] session in self?.activate(session) }
        pane.session = { [weak store] id in store?.sessions.first { $0.id == id } }
        pane.onActivate = { [weak self] session in self?.activate(session) }
        pane.onChange = { [weak self] sets in self?.panesChanged(sets) }
        store.onListChanged = { [weak pane] ids in pane?.sessionsChanged(listed: ids) }
        store.onLayoutLoaded = { [weak self] json in self?.restorePanes(json) }
        notifier.isLooking = { [weak store] id in NSApp.isActive && store?.visibleIDs.contains(id) == true }
        notifier.onOpen = { [weak self] id in self?.openFromNotification(id) }
        notifier.start()
        store.onNotices = { [weak notifier] notices in notifier?.receive(notices) }
        setUpPopup()
        SpeechController.shared.checkSetup()
        let args = CommandLine.arguments
        offerSetup()
        store.start()

        NSApp.mainMenu = makeMenu()
        window.makeKeyAndOrderFront(nil)
        self.window = window
        NSApp.activate(ignoringOtherApps: true)
        #if DEBUG
        // Opens the popup at launch, to check how it looks.
        if args.contains("--demo-splits") {
            // Splits as the shortcuts do, to check how panes look.
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.splitPane(.right) }
            DispatchQueue.main.asyncAfter(deadline: .now() + 4) { self.splitPane(.down) }
        }
        if args.contains("--open-themes") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.showThemes() }
        }
        if let flag = args.firstIndex(of: "--preview-theme"), flag + 1 < args.count {
            // Previews this after the popup opens, to check live changes.
            DispatchQueue.main.asyncAfter(deadline: .now() + 6) { self.previewTheme(args[flag + 1]) }
        }
        if args.contains("--open-resume") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { self.resumeSession() }
        }
        if args.contains("--open-rename") {
            // Renames up to three sessions, to check how the popup looks.
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                self.showRename(Array(self.store.sessions.prefix(3)))
            }
        }
        if args.contains("--open-commands") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                self.toggleCommands()
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    let responder = self.window?.firstResponder
                    let field = (responder as? NSTextView)?.delegate
                    print("popup focus: \(String(describing: responder.map { Swift.type(of: $0) })) editing \(String(describing: field.map { Swift.type(of: $0) }))")
                    fflush(stdout)
                }
            }
        }
        #endif
    }

    /// Offers to add the Claude Code hooks that live status needs, if they're missing.
    private func offerSetup() {
        guard !DaemonPaths.hooksInstalled else {
            // The hooks were accepted before, and the app has since moved.
            if !DaemonPaths.hooksCurrent { _ = runInit() }
            return
        }
        let alert = NSAlert()
        alert.messageText = "Set up Agent Command?"
        alert.informativeText = """
            This adds hook entries to your Claude Code settings, so sessions can show when they're busy, waiting \
            or done. It backs the file up first, and keeps your other settings. It also saves your login shell's \
            environment for the sessions it starts.
            """
        alert.addButton(withTitle: "Set Up")
        alert.addButton(withTitle: "Not Now")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        if runInit() { return }
        let failed = NSAlert()
        failed.messageText = "Setup didn't finish"
        failed.informativeText = "Run \(DaemonPaths.acmd) init in a terminal to see why."
        failed.runModal()
    }

    private func runInit() -> Bool {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: DaemonPaths.acmd)
        process.arguments = ["init"]
        do {
            try process.run()
            process.waitUntilExit()
            return process.terminationStatus == 0
        } catch {
            return false
        }
    }

    /// The parts AppKit draws. SwiftUI views follow the theme by themselves.
    private func applyTheme() {
        let theme = AppTheme.shared
        // The title bar in the terminal's colors, so the window reads as one.
        window?.backgroundColor = theme.background
        window?.appearance = NSAppearance(named: theme.isDark ? .darkAqua : .aqua)
        pane?.applyTheme()
        split?.splitView.needsDisplay = true
    }

    private func restorePanes(_ json: String) {
        if let data = json.data(using: .utf8), var kept = PaneSets.decode(data) {
            kept.keep(sessions: Set(store.sessions.map(\.id)))
            pane?.restore(kept)
        }
        restored = true
        let args = CommandLine.arguments
        if let flag = args.firstIndex(of: "--session"), flag + 1 < args.count {
            pane?.select(sessionID: args[flag + 1])
        }
        if let sets = pane?.sets { reflect(sets) }
        if let id = pendingOpen {
            pendingOpen = nil
            openFromNotification(id)
        }
    }

    /// A clicked notification brings up the window on its session.
    private func openFromNotification(_ id: String) {
        guard restored else {
            pendingOpen = id
            return
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
        if store.sessions.contains(where: { $0.id == id }) { openSidebarRow(id) }
    }

    private func reflect(_ sets: PaneSets) {
        store.reflect(
            selected: sets.current.focusedSession, visible: sets.current.visibleSessions,
            sets: sets.members, closed: sets.closedSessions)
    }

    private func panesChanged(_ sets: PaneSets) {
        reflect(sets)
        // Until the saved panes are back, there's nothing worth saving.
        guard restored else { return }
        pendingSave?.cancel()
        let work = DispatchWorkItem { [weak self] in
            if let data = try? JSONEncoder().encode(sets), let json = String(data: data, encoding: .utf8) {
                self?.store.saveLayout(json)
            }
        }
        pendingSave = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3, execute: work)
    }

    /// A split starts a new terminal in the same folder, and joins it to the set.
    /// The pane opens once the terminal has started, so a failed start leaves none.
    private func splitPane(_ direction: SplitDirection) {
        let folder = store.selected?.cwd ?? lastFolder?.path ?? NSHomeDirectory()
        store.newSession(harness: "terminal", in: folder) { [weak self] session in
            self?.pane?.split(direction, session: session.id)
        }
    }

    @objc private func splitRight() { splitPane(.right) }
    @objc private func splitDown() { splitPane(.down) }
    /// Gives the sidebar the keyboard, or gives it back to the terminal.
    @objc private func toggleSidebarFocus() {
        if store.sidebarFocused {
            pane?.focusTerminal()
            return
        }
        if let item = split?.splitViewItems.first, item.isCollapsed {
            split?.toggleSidebar(nil)
        }
        window?.makeFirstResponder(sidebarKeys)
    }

    private func sidebarKey(_ key: SidebarKey) {
        switch key {
        case .up: store.moveCursor(by: -1)
        case .down: store.moveCursor(by: 1)
        case .toggleMark: store.toggleMarkAtCursor()
        case .leave: pane?.focusTerminal()
        case .open:
            if let id = store.cursorID { openSidebarRow(id) }
        }
    }

    private func clickSidebarRow(_ session: Session) {
        window?.makeFirstResponder(sidebarKeys)
        store.setCursor(session.id)
    }

    private func openSidebarRow(_ id: String) {
        if store.selectedID == id {
            pane?.focusTerminal()
        } else {
            store.selectedID = id
        }
    }

    @objc private func archiveSelection() {
        archive(store.actionTargets)
    }

    @objc private func renameSelection() {
        showRename(store.actionTargets)
    }

    /// A popup to rename sessions, which goes back to where the keyboard was.
    private func showRename(_ sessions: [Session]) {
        guard !sessions.isEmpty else { return }
        closePopup()
        showPopup(RenameView(
            sessions: sessions,
            close: { [weak self] in self?.closePopup() },
            rename: { [weak self] name in
                self?.store.rename(sessions, to: name)
                self?.closePopup()
                self?.store.clearMarks()
            }
        ))
    }

    @objc private func focusLeft() { pane?.focus(.left) }
    @objc private func focusRight() { pane?.focus(.right) }
    @objc private func focusUp() { pane?.focus(.up) }
    @objc private func focusDown() { pane?.focus(.down) }
    @objc private func equalizePanes() { pane?.equalize() }

    /// Activating a stopped session resumes it; anything else just shows.
    private func activate(_ session: Session?) {
        guard let session else { return }
        guard session.isStopped else {
            pane?.select(sessionID: session.id)
            return
        }
        store.resume(session.id) { [weak self] in
            self?.pane?.select(sessionID: session.id)
        }
    }

    @objc private func newClaudeSession() { newSession(harness: "claude") }

    @objc private func newTerminal() { newSession(harness: "terminal") }

    private func newSession(harness: String) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = harness == "terminal" ? "Open Terminal" : "Start Session"
        panel.message = "Choose the folder for the new session"
        panel.directoryURL = lastFolder
        guard panel.runModal() == .OK, let url = panel.url else { return }
        lastFolder = url
        store.newSession(harness: harness, in: url.path)
    }

    /// A popup to pick an earlier Claude session, or enter its id.
    @objc private func resumeSession() {
        closePopup()
        showPopup(ResumeView(
            store: store,
            close: { [weak self] in self?.closePopup() },
            choose: { [weak self] id, isRunning in
                self?.closePopup()
                DispatchQueue.main.async { self?.resume(id, isRunning: isRunning) }
            }
        ))
    }

    private func resume(_ id: String, isRunning: Bool) {
        if isRunning {
            let alert = NSAlert()
            alert.messageText = "This session looks like it's still running"
            alert.informativeText = """
                Claude reports it as live, possibly in another terminal. Resuming puts a \
                second process on the same conversation. Exit the other one first if you can.
                """
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Resume Anyway")
            guard alert.runModal() == .alertSecondButtonReturn else { return }
        }
        store.resumeConversation(id)
    }

    /// Shows or hides the command popup.
    @objc private func toggleCommands() {
        if popup != nil {
            closePopup()
            return
        }
        // Before the popup takes the keyboard: it's what the commands act on.
        let targets = store.actionTargets
        showPopup(CommandPopupView(
            commands: commands(targets: targets),
            close: { [weak self] in self?.closePopup() },
            run: { [weak self] command in
                self?.closePopup()
                // After closing, as a command can open a dialog of its own.
                DispatchQueue.main.async { command.action() }
            }
        ))
    }

    /// A popup to choose a theme, previewing each one as it's reached.
    @objc private func showThemes() {
        closePopup()
        let saved = ThemeConfig.theme(inConfig: Self.configText())
        showPopup(ThemePickerView(
            names: ThemeConfig.themeNames(in: GhosttyAppearance.themeFolders),
            current: saved,
            preview: { [weak self] name in self?.previewTheme(name) },
            commit: { [weak self] name in
                self?.closePopup()
                self?.saveTheme(name)
            },
            cancel: { [weak self] in
                self?.closePopup()
                self?.pendingPreview?.cancel()
                GhosttyRuntime.shared.reloadConfig()
            }
        ))
    }

    /// Slightly delayed, so holding an arrow key doesn't reload for every row.
    private func previewTheme(_ name: String?) {
        pendingPreview?.cancel()
        let work = DispatchWorkItem {
            GhosttyRuntime.shared.reloadConfig(themeOverride: .init(name: name))
        }
        pendingPreview = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.05, execute: work)
    }

    private func saveTheme(_ name: String?) {
        pendingPreview?.cancel()
        let path = AppFont.configPath
        do {
            try FileManager.default.createDirectory(
                atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
            try ThemeConfig.setting(theme: name, inConfig: Self.configText())
                .write(toFile: path, atomically: true, encoding: .utf8)
            GhosttyRuntime.shared.reloadConfig()
        } catch {
            GhosttyRuntime.shared.reloadConfig()
            let alert = NSAlert()
            alert.messageText = "Couldn't save the theme"
            alert.informativeText = error.localizedDescription
            alert.runModal()
        }
    }

    private static func configText() -> String {
        (try? String(contentsOfFile: AppFont.configPath, encoding: .utf8)) ?? ""
    }

    private func setUpPopup() {
        popupWindow.willShow = { [weak self] in
            if self?.inbox != nil { self?.closePopup() }
        }
        popupWindow.configure = { [weak self] model in
            model.goToSession = { id in self?.openFromNotification(id) }
            model.rename = { sessions in
                self?.popupWindow.close()
                NSApp.activate(ignoringOtherApps: true)
                self?.showRename(sessions)
            }
            model.archive = { sessions in
                self?.popupWindow.close()
                NSApp.activate(ignoringOtherApps: true)
                self?.archive(sessions)
            }
            model.moveOut = { session in self?.pane?.moveOut(sessionID: session.id) }
        }
        store.onNewItems = { [weak self] items in self?.popupWindow.itemsArrived(items) }
        notifier.popupShows = { [weak self] kind in self?.popupWindow.replacesBanner(noticeKind: kind) ?? false }
        hotKey = SystemHotKey { [weak self] in self?.popupShortcut() }
    }

    /// In the app, the Inbox. Elsewhere, the popup: the first press gives an open one
    /// the keyboard, and the next closes it.
    private func popupShortcut() {
        if popupWindow.hasKeyboard {
            popupWindow.close()
        } else if NSApp.isActive {
            toggleInbox()
        } else {
            popupWindow.show(focus: true)
        }
    }

    @objc private func toggleReading() {
        store.toggleReadingSelected()
    }

    @objc private func togglePopupEnabled() {
        PopupSettings.shared.enabled.toggle()
    }

    @objc private func toggleInbox() {
        if inbox == nil { showInbox(.queue) } else { closePopup() }
    }

    /// The Inbox goes over the whole window, and your panes are as you left them after.
    private func showInbox(_ filter: InboxFilter) {
        closePopup()
        popupWindow.close()
        let model = InboxModel(store: store, filter: filter)
        model.close = { [weak self] in self?.closePopup() }
        model.goToSession = { [weak self] id in self?.openSidebarRow(id) }
        model.rename = { [weak self] sessions in self?.showRename(sessions) }
        model.archive = { [weak self] sessions in self?.archive(sessions) }
        model.moveOut = { [weak self] session in self?.pane?.moveOut(sessionID: session.id) }
        showPopup(InboxView(model: model), aboveStatusBar: true)
        window?.makeFirstResponder(nil)
        store.inboxOpen = true
        model.start()
        inbox = model
    }

    /// Popups go over the whole window, so they stay centered across panes.
    private func showPopup(_ view: some View, aboveStatusBar: Bool = false) {
        guard let content = window?.contentView else { return }
        popupReturn = store.sidebarState
        let hosting = NSHostingView(rootView: view)
        hosting.frame = content.bounds
        if aboveStatusBar {
            hosting.frame.origin.y = StatusBarView.height
            hosting.frame.size.height -= StatusBarView.height
        }
        hosting.autoresizingMask = [.width, .height]
        content.addSubview(hosting, positioned: .above, relativeTo: nil)
        popup = hosting
    }

    private func closePopup() {
        guard let shown = popup else { return }
        shown.removeFromSuperview()
        popup = nil
        if let inbox {
            inbox.stop()
            self.inbox = nil
            store.inboxOpen = false
        }
        if let state = popupReturn {
            window?.makeFirstResponder(sidebarKeys)
            store.restoreSidebar(state)
        } else {
            pane?.focusTerminal()
        }
        popupReturn = nil
        store.markLookedAtSeen()
    }

    /// What can be done right now: what the popup lists depends on what's showing.
    private func commands(targets: [Session]) -> [PopupCommand] {
        var list: [PopupCommand] = []
        let current = targets.count == 1 ? targets[0] : nil
        if targets.count > 1 {
            let section = "\(targets.count) sessions"
            list.append(PopupCommand(section: section, title: "Rename…", shortcut: "⌘R") { [weak self] in
                self?.showRename(targets)
            })
            list.append(PopupCommand(section: section, title: "Archive…", shortcut: "⇧⌘⌫") { [weak self] in
                self?.archive(targets)
            })
        }
        if let session = current {
            let section = session.isTerminal ? "Terminal session" : "Claude session"
            list.append(PopupCommand(section: section, title: "Rename…", shortcut: "⌘R") { [weak self] in
                self?.showRename([session])
            })
            list.append(PopupCommand(section: section, title: "Archive…", shortcut: "⇧⌘⌫") { [weak self] in
                self?.archive([session])
            })
            if store.setMembers.contains(where: { $0.contains(session.id) }) {
                list.append(PopupCommand(section: section, title: "Move out of set") { [weak self] in
                    self?.pane?.moveOut(sessionID: session.id)
                })
            }
            list.append(PopupCommand(section: section, title: "Copy folder path") {
                Self.copyToPasteboard(session.cwd)
            })
            if !session.isTerminal {
                list.append(PopupCommand(section: section, title: "Copy Claude session ID") {
                    Self.copyToPasteboard(session.claudeSessionID)
                })
            }
        }
        for session in store.sessions where session.id != current?.id {
            list.append(PopupCommand(section: "Go to", title: session.displayName, shortcut: session.status, key: session.id) {
                [weak self] in self?.store.selectedID = session.id
            })
        }
        list.append(PopupCommand(section: "New", title: "Claude session", shortcut: "⌘N") { [weak self] in
            self?.newSession(harness: "claude")
        })
        list.append(PopupCommand(section: "New", title: "Terminal", shortcut: "⌘T") { [weak self] in
            self?.newSession(harness: "terminal")
        })
        list.append(PopupCommand(section: "New", title: "Resume a session…", shortcut: "⇧⌘O") { [weak self] in
            self?.resumeSession()
        })
        let paneCount = pane?.tree.shownLeaves.count ?? 0
        list.append(PopupCommand(section: "Panes", title: "Split right", shortcut: "⌘↩") { [weak self] in
            self?.splitPane(.right)
        })
        list.append(PopupCommand(section: "Panes", title: "Split down", shortcut: "⇧⌘↩") { [weak self] in
            self?.splitPane(.down)
        })
        list.append(PopupCommand(section: "Panes", title: "Close pane", shortcut: "⌘W") { [weak self] in
            self?.closePane()
        })
        if paneCount > 1 {
            for (title, shortcut, direction) in [
                ("Focus left", "⌃⌘H", FocusDirection.left), ("Focus down", "⌃⌘J", .down),
                ("Focus up", "⌃⌘K", .up), ("Focus right", "⌃⌘L", .right),
            ] {
                list.append(PopupCommand(section: "Panes", title: title, shortcut: shortcut) { [weak self] in
                    self?.pane?.focus(direction)
                })
            }
            list.append(PopupCommand(section: "Panes", title: "Equalize panes", shortcut: "⌃⌘=") { [weak self] in
                self?.equalizePanes()
            })
        }
        if let surface = pane?.shown {
            for (title, shortcut, binding) in [
                ("Clear screen", "⌘K", "clear_screen"),
                ("Scroll to top", "⌘↖", "scroll_to_top"),
                ("Scroll to bottom", "⌘↘", "scroll_to_bottom"),
                ("Select all", "⌘A", "select_all"),
                ("Paste", "⌘V", "paste_from_clipboard"),
                ("Increase font size", "⌘=", "increase_font_size:1"),
                ("Decrease font size", "⌘-", "decrease_font_size:1"),
                ("Reset font size", "⌘0", "reset_font_size"),
            ] {
                list.append(PopupCommand(section: "Screen", title: title, shortcut: shortcut) { [weak surface] in
                    surface?.perform(binding: binding)
                })
            }
        }
        list.append(PopupCommand(section: "App", title: "Inbox", shortcut: "⇧⌘I") { [weak self] in
            self?.showInbox(.queue)
        })
        let speech = SpeechController.shared
        list.append(PopupCommand(section: "App", title: speech.isEnabled ? "Turn speech off" : "Turn speech on") {
            speech.setEnabled(!speech.isEnabled)
        })
        if speech.isEnabled, current?.isAgent == true || speech.isSpeaking {
            let title = SpeechController.shared.isSpeaking ? "Stop reading" : "Read the latest message aloud"
            list.append(PopupCommand(section: "App", title: title, shortcut: "⇧⌘S") { [weak self] in
                self?.store.toggleReadingSelected()
            })
        }
        let popup = PopupSettings.shared
        list.append(PopupCommand(section: "App", title: popup.autoPlay ? "Stop reading aloud in the popup" : "Read aloud in the popup") {
            popup.autoPlay.toggle()
        })
        list.append(PopupCommand(section: "App", title: popup.enabled ? "Turn popup off" : "Turn popup on", shortcut: "⌥⌘I") {
            popup.enabled.toggle()
        })
        for style in ReplyPopupStyle.allCases where style != popup.style {
            list.append(PopupCommand(section: "App", title: "Popup style: \(style.label)") {
                popup.style = style
            })
        }
        for opens in PopupOpens.allCases where opens != popup.opens {
            list.append(PopupCommand(section: "App", title: "Popup opens for: \(opens.label)") {
                popup.opens = opens
            })
        }
        if let current {
            list.append(PopupCommand(section: "App", title: "Inbox for \(current.displayName)") { [weak self] in
                self?.showInbox(.session(current.id))
            })
        }
        list.append(PopupCommand(section: "App", title: "Change theme…", shortcut: "⇧⌘T") { [weak self] in
            self?.showThemes()
        })
        list.append(PopupCommand(section: "App", title: "Focus sidebar", shortcut: "⇧⌘B") { [weak self] in
            self?.toggleSidebarFocus()
        })
        list.append(PopupCommand(section: "App", title: "Toggle sidebar") { [weak self] in
            self?.split?.toggleSidebar(nil)
        })
        list.append(PopupCommand(section: "App", title: "Quit", shortcut: "⌘Q") {
            NSApp.terminate(nil)
        })
        return list
    }

    private static func copyToPasteboard(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    /// ⌘W closes the pane and leaves its session running and in its set, so
    /// selecting the session again brings the pane back.
    @objc private func closePane() {
        pane?.closeFocused()
    }

    private func archive(_ sessions: [Session]) {
        guard let first = sessions.first else { return }
        let alert = NSAlert()
        alert.messageText = sessions.count == 1
            ? "Archive \(first.displayName)?"
            : "Archive \(sessions.count) sessions?"
        var notes: [String] = []
        if sessions.contains(where: \.isTerminal) {
            notes.append("Terminals end, along with everything running in them, and can't be brought back.")
        }
        if sessions.contains(where: { !$0.isTerminal }) {
            notes.append("Agents stop. You can unarchive them later and continue the conversation.")
        }
        alert.informativeText = notes.joined(separator: " ")
        alert.addButton(withTitle: "Archive")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            store.archive(sessions)
        }
    }

    private func makeMenu() -> NSMenu {
        let menu = NSMenu()
        let app = NSMenuItem()
        app.submenu = NSMenu()
        app.submenu?.addItem(
            withTitle: "Quit Agent Command", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(app)
        let file = NSMenuItem()
        file.submenu = NSMenu(title: "File")
        for (title, action, key) in [
            ("New Claude Session", #selector(newClaudeSession), "n"),
            ("New Terminal", #selector(newTerminal), "t"),
            ("Resume Session…", #selector(resumeSession), "O"),
            ("Commands…", #selector(toggleCommands), "P"),
            ("Change Theme…", #selector(showThemes), "T"),
            ("Focus Sidebar", #selector(toggleSidebarFocus), "B"),
            ("Inbox", #selector(toggleInbox), "I"),
            ("Read Aloud", #selector(toggleReading), "S"),
        ] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
            item.target = self
            file.submenu?.addItem(item)
        }
        let renameItem = NSMenuItem(title: "Rename…", action: #selector(renameSelection), keyEquivalent: "r")
        renameItem.target = self
        file.submenu?.addItem(renameItem)
        let popupItem = NSMenuItem(title: "Turn Popup On or Off", action: #selector(togglePopupEnabled), keyEquivalent: "i")
        popupItem.keyEquivalentModifierMask = [.command, .option]
        popupItem.target = self
        file.submenu?.addItem(popupItem)
        let archiveItem = NSMenuItem(title: "Archive…", action: #selector(archiveSelection), keyEquivalent: "\u{8}")
        archiveItem.keyEquivalentModifierMask = [.command, .shift]
        archiveItem.target = self
        file.submenu?.addItem(archiveItem)
        menu.addItem(file)
        let view = NSMenuItem()
        view.submenu = NSMenu(title: "Panes")
        let items: [(String, Selector, String, NSEvent.ModifierFlags)] = [
            ("Split Right", #selector(splitRight), "\r", [.command]),
            ("Split Down", #selector(splitDown), "\r", [.command, .shift]),
            ("Close Pane", #selector(closePane), "w", [.command]),
            ("Focus Left", #selector(focusLeft), "h", [.command, .control]),
            ("Focus Down", #selector(focusDown), "j", [.command, .control]),
            ("Focus Up", #selector(focusUp), "k", [.command, .control]),
            ("Focus Right", #selector(focusRight), "l", [.command, .control]),
            ("Equalize", #selector(equalizePanes), "=", [.command, .control]),
        ]
        for (title, action, key, modifiers) in items {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: key)
            item.keyEquivalentModifierMask = modifiers
            item.target = self
            view.submenu?.addItem(item)
        }
        menu.addItem(view)
        // Text fields get paste and the rest through these, from the responder
        // chain. A terminal surface answers them too.
        let edit = NSMenuItem()
        edit.submenu = NSMenu(title: "Edit")
        edit.submenu?.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.submenu?.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.submenu?.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.submenu?.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        menu.addItem(edit)
        return menu
    }

    /// Opening a running app shows its window.
    /// A click on the Dock icon: the popup if it's open, even behind other windows,
    /// otherwise the main window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if popupWindow.isOpen {
            popupWindow.show(focus: true)
            return false
        }
        window?.makeKeyAndOrderFront(nil)
        return true
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        popupWindow.appBecameActive()
        notifier.refreshPermission()
        GhosttyRuntime.shared.setFocus(true)
        store.markLookedAtSeen()
    }

    func applicationDidResignActive(_ notification: Notification) {
        GhosttyRuntime.shared.setFocus(false)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        trace("last window closed")
        return true
    }

    func windowWillClose(_ notification: Notification) {
        trace("window will close: \(String(describing: notification.object))")
    }

    func applicationDockMenu(_ sender: NSApplication) -> NSMenu? {
        let menu = NSMenu()
        let item = NSMenuItem(title: "Inbox", action: #selector(openPopupFromDock), keyEquivalent: "")
        item.target = self
        menu.addItem(item)
        return menu
    }

    @objc private func openPopupFromDock() {
        popupWindow.show(focus: true)
    }

    func applicationWillTerminate(_ notification: Notification) {
        trace("application will terminate")
        SpeechController.shared.stop()
    }
}

GhosttyAppearance.locateResources()

// One app at a time, unless asked for another: opening it again shows the window.
if !CommandLine.arguments.contains("--new-instance"), let id = Bundle.main.bundleIdentifier,
   NSRunningApplication.runningApplications(withBundleIdentifier: id).contains(where: { $0 != .current }) {
    let open = Process()
    open.executableURL = URL(fileURLWithPath: "/usr/bin/open")
    open.arguments = ["-b", id]
    try? open.run()
    open.waitUntilExit()
    exit(0)
}

guard ghostty_init_ok() else {
    FileHandle.standardError.write(Data("agent-command: failed to initialize libghostty\n".utf8))
    exit(1)
}

atexit { trace("process exit") }

AppFont.register()

let app = NSApplication.shared
app.setActivationPolicy(.regular)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
