import AppKit
import SwiftUI

// Usage: AgentCommand [--session <id>], selecting that session at launch.

/// Writes why the app is going away, to find unexpected exits. Started from Finder or
/// the Dock, it lands in app.log in the state folder (see AppLog).
func trace(_ what: String) {
    let stack = Thread.callStackSymbols.prefix(14).joined(separator: "\n")
    FileHandle.standardError.write(Data("[\(Date())] \(what)\n\(stack)\n\n".utf8))
}

/// A window with the session sidebar beside the pane. Closing it only detaches:
/// sessions keep running in their hosts.
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate {
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
    private let hookHealth = HookHealth()
    /// Hosting, its clients and requests: alive with the app, so a request badges and notifies.
    private let remote = RemoteSettingsModel()
    /// The Remote hosts this Mac connects to, for Settings.
    private let knownHosts = KnownHostsModel()
    /// The Remote Hosts browser, while it's open.
    private var browser: RemoteBrowserModel?
    /// Reads who's attached to Remote sessions while anyone is connected.
    private var attachedTimer: Timer?
    private var inbox: InboxModel?
    private var scheduled: ScheduledModel?
    private lazy var popupWindow = PopupController(store: store)
    private var hotKeys: [SystemHotKey] = []
    /// The menu items whose shortcuts come from the keymap.
    private var menuItems: [KeyAction: NSMenuItem] = [:]
    private var shortcuts: ShortcutsModel?
    private var settingsWindow: NSWindow?
    private var settingsModel: SettingsModel?
    private var statusItem: NSStatusItem?
    private var settingsClosed: NSObjectProtocol?
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
                openInEditor: { [weak self] session in self?.openInEditor(session) },
                moveOut: { [weak self] session in self?.pane?.moveOut(sessionID: session.id) },
                openSet: { [weak self] session in self?.pane?.showSet(containing: session.id) },
                clickRow: { [weak self] session in self?.clickSidebarRow(session) },
                openRow: { [weak self] session in self?.openSidebarRow(session.id) },
                remote: remote,
                setRemote: { [weak self] session, on in
                    guard let self else { return }
                    self.store.setRemote(self.store.actionTargets(forRow: session), on)
                },
                removeAdded: { [weak self] session in
                    guard let self else { return }
                    self.store.removeAdded(self.store.actionTargets(forRow: session))
                }
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
            notifier: notifier, store: store, openInbox: { [weak self] in self?.toggleInbox() },
            openScheduled: { [weak self] in self?.toggleScheduled() }))
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
        pane.makeHeader = { [weak store] id, onTap in
            guard let store else { return NSView() }
            return NSHostingView(rootView: PaneHeader(store: store, sessionID: id, onTap: onTap))
        }
        pane.onChange = { [weak self] sets in self?.panesChanged(sets) }
        store.onListChanged = { [weak pane] ids in pane?.sessionsChanged(listed: ids) }
        store.onLayoutLoaded = { [weak self] json in self?.restorePanes(json) }
        notifier.isLooking = { [weak store] id in store?.isInMainWindow == true && store?.visibleIDs.contains(id) == true }
        notifier.onOpen = { [weak self] id in self?.openFromNotification(id) }
        notifier.moreWarnings = { [weak store, weak self] in
            var list: [AppWarning] = []
            if let store, let advice = WakeAdvice.check(store.wake, jobs: store.jobs) {
                list.append(AppWarning(
                    id: "wake", title: advice.title, detail: advice.detail,
                    fix: "Turn on waking the Mac: macOS asks for your password once.",
                    actionTitle: advice.actionTitle, action: { self?.turnWakeOn(advice.args) }))
            }
            if let self { list += hookHealth.warnings(setUp: { [weak self] in self?.setUpHooks() }) }
            if let store, let hosting = RemoteWarning.check(
                sessions: store.sessions, hosting: store.remote, turnOn: { self?.remote.setHosting(true) }) {
                list.append(hosting)
            }
            return list
        }
        notifier.start()
        store.onNotices = { [weak notifier] notices in notifier?.receive(notices) }
        store.onRemote = { [weak remote] status in remote?.update(status) }
        remote.onNewRequests = { [weak notifier] requests in notifier?.notify(requests) }
        remote.onChange = { [weak self] in self?.settingsModel?.load() }
        notifier.onRemoteRequest = { [weak self] answer in self?.answerRemoteRequest(answer) }
        // A client attaching to a session doesn't change Hosting's status: while
        // anyone's connected, read who's attached, for the sidebar's counts.
        attachedTimer = Timer.scheduledTimer(withTimeInterval: 10, repeats: true) { [weak self] _ in
            guard let self, (self.remote.status?.connected ?? 0) > 0, self.store.sessions.contains(where: \.isRemote) else { return }
            self.remote.loadLists()
        }
        setUpPopup()
        SpeechController.shared.checkSetup()
        let args = CommandLine.arguments
        offerSetup()
        hookHealth.start()
        store.start()

        NSApp.mainMenu = makeMenu()
        MenuBarPreference.shared.onChange = { [weak self] in self?.updateMenuBarItem() }
        updateMenuBarItem()
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

    /// Offers to add the hooks that live status needs (Claude Code's, and agy's once
    /// it's installed), if they're missing.
    /// Runs `acmd init` for a hooks warning, then checks again so it goes away.
    private func setUpHooks() {
        Acmd.run(["init"]) { [weak self] ok, output in
            if !ok {
                let failed = NSAlert()
                failed.messageText = "Setup didn't finish"
                failed.informativeText = output.isEmpty ? "Run \(DaemonPaths.acmd) init in a terminal to see why." : output
                failed.runModal()
            }
            self?.hookHealth.check(force: true)
        }
    }

    private func offerSetup() {
        let need = DaemonPaths.setupNeed
        switch need {
        case .none: return
        case .refresh:
            // The hooks were accepted before, and the app has since moved.
            _ = runInit()
            return
        case .offer: break
        }
        let alert = NSAlert()
        alert.messageText = "Set up Agent Command?"
        alert.informativeText = need.offerText
        // Opt-in: it adds a sudoers rule, so macOS asks for the password too.
        let wake = NSButton(checkboxWithTitle: "Also let scheduled jobs wake the Mac (asks for your password)", target: nil, action: nil)
        wake.state = .off
        if need.isFirstSetup { alert.accessoryView = wake }
        alert.addButton(withTitle: "Set Up")
        alert.addButton(withTitle: "Not Now")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        if runInit() {
            if wake.state == .on { turnWakeOn(["schedule", "wake", "on", "--all"]) }
            return
        }
        let failed = NSAlert()
        failed.messageText = "Setup didn't finish"
        failed.informativeText = "Run \(DaemonPaths.acmd) init in a terminal to see why."
        failed.runModal()
    }

    /// Runs `acmd schedule wake on ...`; macOS asks for the password.
    private func turnWakeOn(_ args: [String]) {
        Acmd.run(args) { ok, output in
            guard !ok, !output.contains("canceled") else { return }
            let failed = NSAlert()
            failed.messageText = "Couldn't turn on waking the Mac"
            failed.informativeText = output.isEmpty ? "Run acmd schedule wake on in a terminal to see why." : output
            failed.runModal()
        }
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
        showMainWindow()
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
        // A host's folder isn't on this Mac: the terminal starts here.
        let here = store.selected.flatMap { $0.isOnHost ? nil : $0.cwd }
        let folder = here ?? lastFolder?.path ?? NSHomeDirectory()
        store.newSession(harness: Harness.terminal.rawValue, in: folder) { [weak self] session in
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
        case .number(let n): showSession(number: n)
        case .nextAttention: showNextAttention()
        }
    }

    /// Shows session n, counting down the sidebar.
    private func showSession(number n: Int) {
        guard let id = store.sessionID(number: n) else { return NSSound.beep() }
        store.show(id)
    }

    /// Shows the next session that needs you. A beep says none does.
    @objc private func showNextAttention() {
        guard let id = store.nextAttentionID else { return NSSound.beep() }
        store.show(id)
    }

    @objc private func goToNumber(_ sender: NSMenuItem) {
        showSession(number: sender.tag)
    }

    /// Brings the main window forward from any app, or hides the app when you're in it.
    private func appShortcut() {
        if store.isInMainWindow {
            NSApp.hide(nil)
        } else {
            showMainWindow()
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

    /// Takes the sessions added from a host off this Mac: they keep running there.
    /// Any others are left alone, so it does nothing without one.
    @objc private func removeAddedSelection() {
        store.removeAdded(store.actionTargets)
    }

    @objc private func renameSelection() {
        showRename(store.actionTargets)
    }

    /// A popup to rename sessions, which goes back to where the keyboard was.
    @objc private func reportBug() { showReport(.bug) }
    @objc private func requestFeature() { showReport(.feature) }

    private func showReport(_ kind: ReportKind) {
        closePopup()
        showMainWindow()
        // A terminal keeps the keyboard otherwise, and the form couldn't take it.
        window?.makeFirstResponder(nil)
        showPopup(ReportView(kind: kind, close: { [weak self] in self?.closePopup() }))
    }

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
        // A host resumes its own sessions; this Mac only attaches.
        guard session.isStopped, !session.isOnHost else {
            pane?.select(sessionID: session.id)
            return
        }
        store.resume(session.id) { [weak self] in
            self?.pane?.select(sessionID: session.id)
        }
    }

    /// ⌘N: a session with the default harness. With none chosen yet, it asks which,
    /// with "make it the default" ticked.
    @objc private func newDefaultSession() {
        if let harness = DefaultHarness.current {
            newSession(harness: harness)
        } else {
            askHarness(firstTime: true)
        }
    }

    /// ⇧⌘T: a terminal, with the folder panel's Remote checkbox.
    @objc private func newTerminal() { newSession(harness: .terminal, asksRemote: true) }

    /// A popup asking where and which harness, then the folder.
    @objc private func chooseHarness() { askHarness(firstTime: false) }

    /// host chooses a Remote host to start with, as the browser's New Session does.
    private func askHarness(firstTime: Bool, host: String? = nil) {
        closePopup()
        showMainWindow()
        // ⌘N's first question is only about this Mac.
        let hosts = firstTime ? [] : store.remoteHosts.map(\.name)
        showPopup(HarnessPickerView(
            title: firstTime ? "What should ⌘N start?" : "New session",
            makeDefault: firstTime,
            picker: NewSessionPicker(hosts: hosts, host: host, client: store.remoteClient),
            close: { [weak self] in self?.closePopup() },
            choose: { [weak self] harness, makeDefault, host in
                self?.closePopup()
                if makeDefault { self?.setDefaultHarness(harness) }
                if let host {
                    self?.chooseRemoteFolder(host: host, harness: harness)
                } else {
                    // After closing, as the folder panel is modal. ⌘N's first time doesn't ask about Remote.
                    DispatchQueue.main.async { self?.newSession(harness: harness, asksRemote: !firstTime) }
                }
            }
        ))
    }

    /// The host's folders, for a session on it. It's created there, Remote, and shown here.
    private func chooseRemoteFolder(host: String, harness: Harness) {
        let chooser = RemoteFolderChooser(host: host, client: store.remoteClient)
        chooser.choose = { [weak self, weak chooser] dir in
            self?.store.newRemoteSession(host: host, dir: dir, harness: harness) { failure in
                if let failure, let chooser {
                    chooser.fail(failure)
                } else {
                    self?.closePopup()
                }
            }
        }
        showPopup(RemoteFolderView(model: chooser, harness: harness, close: { [weak self] in self?.closePopup() }))
    }

    /// Saves what ⌘N starts in acmd's config, which `acmd new` reads too.
    private func setDefaultHarness(_ harness: Harness?) {
        Acmd.run(DefaultHarness.arguments(harness)) { ok, output in
            guard !ok else { return }
            let failed = NSAlert()
            failed.messageText = "The default couldn't be saved"
            failed.informativeText = output.isEmpty ? "Run acmd config set default_harness in a terminal to see why." : output
            failed.runModal()
        }
    }

    /// The folder panel, then the session. asksRemote adds the Remote checkbox, which
    /// starts from `new_sessions_remote`; without it (⌘N) the daemon uses that setting.
    private func newSession(harness: Harness, asksRemote: Bool = false) {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = harness.panelPrompt
        panel.message = "Choose the folder for the new session"
        panel.directoryURL = lastFolder
        let remoteBox = NSButton(
            checkboxWithTitle: "Remote: other Agent Commands using this Mac's Hosting can see and use it", target: nil, action: nil)
        if asksRemote {
            remoteBox.state = RemoteDefaults.newSessionsRemote ? .on : .off
            remoteBox.sizeToFit()
            let box = NSView(frame: NSRect(x: 0, y: 0, width: remoteBox.frame.width + 40, height: remoteBox.frame.height + 16))
            remoteBox.frame.origin = NSPoint(x: 20, y: 8)
            box.addSubview(remoteBox)
            panel.accessoryView = box
            panel.isAccessoryViewDisclosed = true
        }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        lastFolder = url
        store.newSession(harness: harness.rawValue, in: url.path, remote: asksRemote ? remoteBox.state == .on : nil)
    }

    /// A popup to pick an earlier Claude or Antigravity conversation, or enter its id.
    @objc private func resumeSession() {
        closePopup()
        showPopup(ResumeView(
            store: store,
            close: { [weak self] in self?.closePopup() },
            choose: { [weak self] target in
                self?.closePopup()
                DispatchQueue.main.async { self?.resume(target) }
            }
        ))
    }

    private func resume(_ target: ResumeTarget) {
        if target.isRunning {
            let alert = NSAlert()
            alert.messageText = "This session looks like it's still running"
            alert.informativeText = """
                \(target.harness.title) reports it as live, possibly in another terminal. Resuming puts a \
                second process on the same conversation. Exit the other one first if you can.
                """
            alert.addButton(withTitle: "Cancel")
            alert.addButton(withTitle: "Resume Anyway")
            guard alert.runModal() == .alertSecondButtonReturn else { return }
        }
        store.resumeConversation(target.id, harness: target.harness)
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
                self?.showMainWindow()
                self?.showRename(sessions)
            }
            model.archive = { sessions in
                self?.popupWindow.close()
                self?.showMainWindow()
                self?.archive(sessions)
            }
            model.moveOut = { session in self?.pane?.moveOut(sessionID: session.id) }
        }
        store.onNewItems = { [weak self] items in self?.popupWindow.itemsArrived(items) }
        notifier.popupShows = { [weak self] kind in self?.popupWindow.replacesBanner(noticeKind: kind) ?? false }
        registerHotKeys()
        Keymap.shared.onChange = { [weak self] in self?.keysChanged() }
        GhosttyRuntime.shared.onOpenFile = { [weak self] file, session in self?.openFile(file, from: session) }
        Keymap.shared.writeExport()
    }

    /// Closing the window keeps the app running, for the popup and the Dock badge. The
    /// Dock icon stays, and a click on it brings the window back.
    private func hideMainWindow() {
        window?.orderOut(nil)
        store.isWindowShown = false
        // Otherwise the app would count as in front with nothing on screen.
        NSApp.deactivate()
    }

    private func showMainWindow() {
        store.isWindowShown = true
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
        store.markLookedAtSeen()
    }

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        guard sender === window else { return true }
        hideMainWindow()
        return false
    }

    /// In the app, the Inbox. Elsewhere, the popup: the first press gives an open one
    /// the keyboard, and the next closes it.
    private func popupShortcut() {
        if popupWindow.hasKeyboard {
            popupWindow.close()
        } else if store.isInMainWindow {
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

    /// Applies to sessions started or resumed after the change.
    @objc func toggleAgentRemoteControl() {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: DaemonPaths.acmd)
        process.arguments = AgentRemoteControlSetting.arguments(on: !AgentRemoteControlSetting.isOn)
        do {
            try process.run()
            process.waitUntilExit()
            if process.terminationStatus == 0 { return }
        } catch {}
        let alert = NSAlert()
        alert.messageText = "Couldn't change Agent Remote Control"
        alert.informativeText = "Run \(DaemonPaths.acmd) agent-remote-control on (or off) in a terminal to see why."
        alert.runModal()
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

    @objc private func toggleScheduled() {
        if scheduled == nil { showScheduled() } else { closePopup() }
    }

    /// The Scheduled view goes over the whole window, like the Inbox.
    private func showScheduled() {
        closePopup()
        popupWindow.close()
        showMainWindow()
        let model = ScheduledModel(store: store)
        model.close = { [weak self] in self?.closePopup() }
        model.goToSession = { [weak self] id in
            self?.closePopup()
            self?.openSidebarRow(id)
        }
        showPopup(ScheduledView(model: model), aboveStatusBar: true)
        window?.makeFirstResponder(nil)
        model.start()
        scheduled = model
    }

    // MARK: Remote hosts

    /// The Remote Hosts browser goes over the whole window, like Scheduled. host
    /// chooses the host it starts on.
    private func showRemoteHosts(host: String? = nil) {
        closePopup()
        popupWindow.close()
        showMainWindow()
        let model = RemoteBrowserModel(client: store.remoteClient, hosts: { [weak store] in store?.remoteHosts ?? [] }, host: host)
        model.close = { [weak self] in self?.closePopup() }
        model.handlesKey = { [weak self] event in event.window === self?.window }
        model.adopt = { [weak self] session in self?.store.adopt(session) }
        model.focus = { [weak self] id in
            self?.closePopup()
            self?.store.show(id)
        }
        model.newSession = { [weak self] host in self?.askHarness(firstTime: false, host: host) }
        showPopup(RemoteBrowserView(model: model, settings: { [weak self] in self?.openSettings(at: .remote) }), aboveStatusBar: true)
        window?.makeFirstResponder(nil)
        model.start()
        browser = model
    }

    // MARK: Your editor

    @objc private func openSelectionInEditor() {
        guard let session = store.actionTargets.first else { return NSSound.beep() }
        openInEditor(session)
    }

    /// The session's folder, in the editor chosen in Settings → Editor. A host's
    /// folder isn't on this Mac.
    private func openInEditor(_ session: Session) {
        guard !session.cwd.isEmpty, !session.isOnHost else { return NSSound.beep() }
        Acmd.run(["open", session.id]) { [weak self] ok, output in
            if !ok { self?.editorFailed(output) }
        }
    }

    /// A file ⌘-clicked in a terminal: a relative path is in that session's folder. A
    /// file that isn't there is only a beep, as text that looks like a path often isn't.
    private func openFile(_ file: FileLink, from sessionID: String?) {
        let folder = sessionID.flatMap { id in store.sessions.first { $0.id == id }?.cwd }.flatMap { $0.isEmpty ? nil : $0 }
            ?? NSHomeDirectory()
        guard FileManager.default.fileExists(atPath: file.resolved(in: folder)) else { return NSSound.beep() }
        Acmd.run(file.editArguments(in: folder)) { [weak self] ok, output in
            if !ok { self?.editorFailed(output) }
        }
    }

    private func editorFailed(_ output: String) {
        let alert = NSAlert()
        alert.messageText = "Couldn't open your editor"
        alert.informativeText = output.isEmpty ? "acmd didn't say why." : output
        let choose = output.contains("no editor chosen") || output.contains("acmd editor set")
        if choose {
            alert.addButton(withTitle: "Choose an Editor…")
            alert.addButton(withTitle: "Cancel")
        }
        if alert.runModal() == .alertFirstButtonReturn, choose {
            openSettings(at: .editor)
        }
    }

    // MARK: The menu bar icon

    /// The app's icon in the menu bar: its places and Settings, from any app, even with
    /// the window closed. Settings → General turns it off.
    private func updateMenuBarItem() {
        if MenuBarPreference.shared.shown {
            guard statusItem == nil else { return }
            let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
            let image = NSImage(systemSymbolName: "terminal", accessibilityDescription: "Agent Command")
            image?.isTemplate = true
            item.button?.image = image
            item.button?.toolTip = "Agent Command"
            let menu = NSMenu()
            menu.delegate = self
            item.menu = menu
            statusItem = item
        } else if let statusItem {
            NSStatusBar.system.removeStatusItem(statusItem)
            self.statusItem = nil
        }
    }

    /// Built as it opens, so the popup's state and what's waiting are current.
    func menuNeedsUpdate(_ menu: NSMenu) {
        guard menu === statusItem?.menu else { return }
        menu.removeAllItems()
        @discardableResult
        func add(_ title: String, _ action: Selector, to target: AnyObject? = nil) -> NSMenuItem {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = target ?? self
            menu.addItem(item)
            return item
        }
        let waiting = store.queue.count
        add("Open Agent Command", #selector(menuBarOpen))
        add(waiting > 0 ? "Inbox (\(waiting) waiting)" : "Inbox", #selector(menuBarInbox))
        add("Scheduled Jobs", #selector(menuBarScheduled))
        add(DefaultHarness.menuTitle(DefaultHarness.current), #selector(menuBarNewSession))
        add("New Session…", #selector(menuBarChooseHarness))
        menu.addItem(.separator())
        add("Reply Popup", #selector(togglePopupEnabled)).state = PopupSettings.shared.enabled ? .on : .off
        menu.addItem(.separator())
        add("Settings…", #selector(showSettings))
        add("Keyboard Shortcuts", #selector(menuBarShortcuts))
        menu.addItem(.separator())
        add("Quit Agent Command", #selector(NSApplication.terminate(_:)), to: NSApp)
    }

    @objc private func menuBarOpen() { showMainWindow() }

    @objc private func menuBarInbox() {
        showMainWindow()
        if inbox == nil { showInbox(.queue) }
    }

    @objc private func menuBarScheduled() {
        showMainWindow()
        if scheduled == nil { showScheduled() }
    }

    @objc private func menuBarNewSession() {
        showMainWindow()
        newDefaultSession()
    }

    @objc private func menuBarChooseHarness() {
        showMainWindow()
        chooseHarness()
    }

    @objc private func menuBarShortcuts() {
        if shortcuts == nil { showShortcuts() } else { showMainWindow() }
    }

    /// Every setting, in a window of its own that can stay open beside the sessions.
    @objc private func showSettings() {
        openSettings(at: nil)
    }

    /// Hosting on or off, from the palette.
    private func toggleHosting() {
        remote.setHosting(!remote.hostingOn)
    }

    /// A pending request's notification: approve it, or open Settings at it.
    private func answerRemoteRequest(_ answer: RemoteRequestResponse) {
        switch answer {
        case .approve(let id): remote.approve(id: id)
        case .show(let id):
            remote.highlighted = id
            openSettings(at: .remote)
        }
    }

    private func openSettings(at section: SettingsSection?) {
        NSApp.activate(ignoringOtherApps: true)
        if let settingsWindow {
            if let section { settingsModel?.section = section }
            settingsWindow.makeKeyAndOrderFront(nil)
            return
        }
        let model = SettingsModel()
        if let section { model.section = section }
        settingsModel = model
        model.ghosttyChanged = { GhosttyRuntime.shared.reloadConfig() }
        model.voiceChanged = { SpeechController.shared.voiceChanged() }
        let keys = ShortcutsModel()
        keys.embedded = true
        keys.start()
        let view = SettingsView(
            model: model, remote: remote, store: store, notifier: notifier, shortcuts: keys,
            chooseTheme: { [weak self] in
                self?.showMainWindow()
                self?.showThemes()
            },
            knownHosts: knownHosts,
            browseHosts: { [weak self] host in self?.showRemoteHosts(host: host) })
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 780, height: 560),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered, defer: false)
        window.title = "Settings"
        window.isReleasedWhenClosed = false
        window.contentViewController = NSHostingController(rootView: view)
        window.setContentSize(NSSize(width: 780, height: 560))
        window.center()
        window.setFrameAutosaveName("Settings")
        settingsClosed = NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification, object: window, queue: .main
        ) { [weak self] _ in
            keys.stop()
            if let observer = self?.settingsClosed { NotificationCenter.default.removeObserver(observer) }
            self?.settingsClosed = nil
            self?.settingsWindow = nil
            self?.settingsModel = nil
        }
        settingsWindow = window
        window.makeKeyAndOrderFront(nil)
    }

    @objc private func toggleShortcuts() {
        if shortcuts == nil { showShortcuts() } else { closePopup() }
    }

    /// Every shortcut, and where to change them. It goes over the whole window, like the Inbox.
    private func showShortcuts() {
        closePopup()
        popupWindow.close()
        showMainWindow()
        let model = ShortcutsModel()
        model.close = { [weak self] in self?.closePopup() }
        showPopup(ShortcutsView(model: model), aboveStatusBar: true)
        window?.makeFirstResponder(nil)
        model.start()
        shortcuts = model
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
        if let scheduled {
            scheduled.stop()
            self.scheduled = nil
        }
        if let shortcuts {
            shortcuts.stop()
            self.shortcuts = nil
        }
        if let browser {
            browser.stop()
            self.browser = nil
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
        let speech = SpeechController.shared
        let popup = PopupSettings.shared
        let state = PaletteState(
            targets: targets, sessions: store.sessions, needsYou: store.nextAttentionID != nil, setMembers: store.setMembers,
            paneCount: pane?.tree.shownLeaves.count ?? 0, showsScreen: pane?.shown != nil,
            speechOn: speech.isEnabled, speaking: speech.isSpeaking, agentRemoteControl: AgentRemoteControlSetting.isOn,
            hostingOn: remote.hostingOn,
            hosts: store.remoteHosts.map(\.name),
            defaultHarness: DefaultHarness.current,
            popupReads: popup.autoPlay, popupOn: popup.enabled, popupStyle: popup.style, popupOpens: popup.opens)
        return Palette.entries(state).map { entry in
            PopupCommand(section: entry.section, title: entry.title, shortcut: entry.shortcut, key: entry.key) {
                [weak self] in self?.perform(entry.action)
            }
        }
    }

    /// Carries out a palette entry.
    private func perform(_ action: PaletteAction) {
        switch action {
        case .rename(let sessions): showRename(sessions)
        case .archive(let sessions): archive(sessions)
        case .moveOut(let id): pane?.moveOut(sessionID: id)
        case .copy(let text): Self.copyToPasteboard(text)
        case .select(let id): store.selectedID = id
        case .nextAttention: showNextAttention()
        case .newSession(let name): if let harness = Harness(rawValue: name) { newSession(harness: harness) }
        case .chooseHarness: chooseHarness()
        case .resume: resumeSession()
        case .split(let direction): splitPane(direction)
        case .closePane: closePane()
        case .focus(let direction): pane?.focus(direction)
        case .equalizePanes: equalizePanes()
        case .screen(let binding): pane?.shown?.perform(binding: binding)
        case .inbox: showInbox(.queue)
        case .scheduled: showScheduled()
        case .inboxFor(let session): showInbox(.session(session.id))
        case .toggleSpeech: SpeechController.shared.setEnabled(!SpeechController.shared.isEnabled)
        case .toggleReading: store.toggleReadingSelected()
        case .toggleAgentRemoteControl: toggleAgentRemoteControl()
        case .toggleHosting: toggleHosting()
        case .remoteSettings: openSettings(at: .remote)
        case .setRemote(let sessions, let on): store.setRemote(sessions, on)
        case .removeAdded(let sessions): store.removeAdded(sessions)
        case .remoteHosts: showRemoteHosts()
        case .newOnHost(let host): askHarness(firstTime: false, host: host)
        case .togglePopupReading: PopupSettings.shared.autoPlay.toggle()
        case .togglePopup: PopupSettings.shared.enabled.toggle()
        case .popupStyle(let style): PopupSettings.shared.style = style
        case .popupOpens(let opens): PopupSettings.shared.opens = opens
        case .themes: showThemes()
        case .focusSidebar: toggleSidebarFocus()
        case .toggleSidebar: split?.toggleSidebar(nil)
        case .report(let kind): showReport(kind)
        case .shortcuts: showShortcuts()
        case .openInEditor(let session): openInEditor(session)
        case .settings: showSettings()
        case .quit: NSApp.terminate(nil)
        }
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
        guard let prompt = Palette.archivePrompt(sessions) else { return }
        let alert = NSAlert()
        alert.messageText = prompt.title
        alert.informativeText = prompt.note
        alert.addButton(withTitle: "Archive")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            store.archive(sessions)
        }
    }

    /// ⇧⌘A and ⌘1 to ⌘9. Its session items are named when the menu opens.
    private func makeGoMenu() -> NSMenuItem {
        let go = NSMenuItem()
        go.submenu = NSMenu(title: "Go")
        go.submenu?.addItem(menuItem("Next Needing Attention", #selector(showNextAttention), .nextAttention))
        go.submenu?.addItem(.separator())
        for (index, action) in KeyAction.sessions.enumerated() {
            let item = menuItem("Session \(index + 1)", #selector(goToNumber(_:)), action)
            item.tag = index + 1
            go.submenu?.addItem(item)
        }
        return go
    }

    /// A menu item whose shortcut comes from the keymap, and follows it when it changes.
    private func menuItem(_ title: String, _ action: Selector, _ key: KeyAction) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
        item.target = self
        menuItems[key] = item
        applyKey(key, to: item)
        return item
    }

    private func applyKey(_ action: KeyAction, to item: NSMenuItem) {
        let key = Keymap.shared.combos(action).first?.menuKey
        item.keyEquivalent = key?.key ?? ""
        item.keyEquivalentModifierMask = key?.mask ?? []
    }

    /// After the keymap changes: the menus and the shortcuts from any app follow it.
    private func keysChanged() {
        for (action, item) in menuItems { applyKey(action, to: item) }
        registerHotKeys()
        Keymap.shared.writeExport()
    }

    /// The shortcuts that work from any app, off while the shortcuts list records a key.
    private func registerHotKeys() {
        let keys = Keymap.shared
        hotKeys = []
        guard !keys.recording else { return }
        let actions: [(UInt32, KeyAction, () -> Void)] = [
            (1, .popupAnywhere, { [weak self] in self?.popupShortcut() }),
            (2, .appAnywhere, { [weak self] in self?.appShortcut() }),
        ]
        for (id, action, run) in actions {
            if let combo = keys.combos(action).first { hotKeys.append(SystemHotKey(id: id, combo, action: run)) }
        }
    }

    private func makeMenu() -> NSMenu {
        let menu = NSMenu()
        let app = NSMenuItem()
        app.submenu = NSMenu()
        app.submenu?.addItem(menuItem("Settings…", #selector(showSettings), .settings))
        app.submenu?.addItem(.separator())
        app.submenu?.addItem(menuItem("Quit Agent Command", #selector(NSApplication.terminate(_:)), .quit))
        app.submenu?.items.last?.target = NSApp
        menu.addItem(app)
        let file = NSMenuItem()
        file.submenu = NSMenu(title: "File")
        for (title, action, key) in [
            (DefaultHarness.menuTitle(DefaultHarness.current), #selector(newDefaultSession), KeyAction.newClaude),
            ("New Terminal", #selector(newTerminal), .newTerminal),
            ("New Session…", #selector(chooseHarness), .newSession),
            ("Resume Session…", #selector(resumeSession), .resume),
            ("Commands…", #selector(toggleCommands), .commands),
            ("Change Theme…", #selector(showThemes), .themes),
            ("Focus Sidebar", #selector(toggleSidebarFocus), .focusSidebar),
            ("Inbox", #selector(toggleInbox), .inbox),
            ("Scheduled Jobs", #selector(toggleScheduled), .scheduled),
            ("Read Aloud", #selector(toggleReading), .readAloud),
            ("Rename…", #selector(renameSelection), .rename),
            ("Open in Editor", #selector(openSelectionInEditor), .openInEditor),
            ("Turn Popup On or Off", #selector(togglePopupEnabled), .togglePopup),
        ] {
            file.submenu?.addItem(menuItem(title, action, key))
        }
        let remoteItem = NSMenuItem(title: "Agent Remote Control for New Sessions", action: #selector(toggleAgentRemoteControl), keyEquivalent: "")
        remoteItem.target = self
        file.submenu?.addItem(remoteItem)
        file.submenu?.addItem(menuItem("Archive…", #selector(archiveSelection), .archive))
        file.submenu?.addItem(menuItem("Remove from This Mac", #selector(removeAddedSelection), .removeAdded))
        menu.addItem(file)
        let view = NSMenuItem()
        view.submenu = NSMenu(title: "Panes")
        let items: [(String, Selector, KeyAction)] = [
            ("Split Right", #selector(splitRight), .splitRight),
            ("Split Down", #selector(splitDown), .splitDown),
            ("Close Pane", #selector(closePane), .closePane),
            ("Focus Left", #selector(focusLeft), .focusLeft),
            ("Focus Down", #selector(focusDown), .focusDown),
            ("Focus Up", #selector(focusUp), .focusUp),
            ("Focus Right", #selector(focusRight), .focusRight),
            ("Equalize", #selector(equalizePanes), .equalize),
        ]
        for (title, action, key) in items {
            view.submenu?.addItem(menuItem(title, action, key))
        }
        menu.addItem(view)
        menu.addItem(makeGoMenu())
        let help = NSMenuItem()
        help.submenu = NSMenu(title: "Help")
        help.submenu?.addItem(menuItem("Keyboard Shortcuts", #selector(toggleShortcuts), .shortcuts))
        help.submenu?.addItem(.separator())
        for (title, action) in [
            ("Report a Bug…", #selector(reportBug)),
            ("Request a Feature…", #selector(requestFeature)),
        ] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = self
            help.submenu?.addItem(item)
        }
        NSApp.helpMenu = help.submenu
        // Text fields get paste and the rest through these, from the responder
        // chain. A terminal surface answers them too.
        let edit = NSMenuItem()
        edit.submenu = NSMenu(title: "Edit")
        edit.submenu?.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.submenu?.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.submenu?.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.submenu?.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        menu.addItem(edit)
        menu.addItem(help)
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
        showMainWindow()
        return true
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        hookHealth.check() // a fix or an install made elsewhere shows on coming back
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
        return false
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

AppLog.start()
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

extension AppDelegate: NSMenuItemValidation {
    /// Each time a menu opens: the Agent Remote Control checkmark, and the Go menu's names.
    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        if item.action == #selector(newDefaultSession) {
            item.title = DefaultHarness.menuTitle(DefaultHarness.current)
        }
        if item.action == #selector(toggleAgentRemoteControl) {
            item.state = AgentRemoteControlSetting.isOn ? .on : .off
        }
        if item.action == #selector(goToNumber(_:)) {
            let session = store.sessionID(number: item.tag).flatMap { id in store.sessions.first { $0.id == id } }
            item.title = session?.displayName ?? "Session \(item.tag)"
            return session != nil
        }
        if item.action == #selector(showNextAttention) {
            return store.nextAttentionID != nil
        }
        return true
    }
}
