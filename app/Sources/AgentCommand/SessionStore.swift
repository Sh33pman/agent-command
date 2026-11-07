import AppKit
import Observation
import UserNotifications

/// The daemon's sessions, kept current for the UI. Everything here is on the
/// main thread; daemon calls run on background queues.
@Observable
final class SessionStore {
    private(set) var sessions: [Session] = []
    private(set) var archived: [Session] = []
    var selectedID: String? {
        didSet {
            if selectedID != oldValue && !reflecting { onSelect?(selected) }
        }
    }
    @ObservationIgnored private var reflecting = false
    /// Sessions showing in panes, the selected one among them.
    private(set) var visibleIDs: Set<String> = []
    /// Each set's sessions, in pane order.
    private(set) var setMembers: [[String]] = []
    /// Sessions in a set whose pane is closed.
    private(set) var closedIDs: Set<String> = []
    private(set) var problem: String?
    /// The Inbox's queue: what's left to deal with, in arrival order.
    private(set) var queue: [InboxItem] = []
    /// Covers the panes, so you're not looking at what they show.
    var inboxOpen = false

    /// Whether the sidebar has the keyboard, and its cursor and marks while it does.
    private(set) var sidebarFocused = false
    private(set) var cursorID: String?
    private(set) var markedIDs: Set<String> = []

    /// Called when the selection changes.
    @ObservationIgnored var onSelect: ((Session?) -> Void)?
    /// Called with the ids of the listed sessions when they change.
    @ObservationIgnored var onListChanged: ((Set<String>) -> Void)?
    /// Called with items new since the app started.
    @ObservationIgnored var onNewItems: (([InboxItem]) -> Void)?
    @ObservationIgnored private var newestItem: Int64?
    /// Called when the queue changes, for an open Inbox.
    @ObservationIgnored var onInboxChanged: (() -> Void)?
    /// Called with the daemon's recent notices, whenever its state is read.
    @ObservationIgnored var onNotices: (([Notice]) -> Void)?
    /// Called once with the saved pane layout, after the first list of sessions.
    @ObservationIgnored var onLayoutLoaded: ((String) -> Void)?

    var selected: Session? { sessions.first { $0.id == selectedID } }

    /// Shows what the panes show, without asking them to change.
    func reflect(selected id: String?, visible: Set<String>, sets: [[String]], closed: Set<String>) {
        reflecting = true
        selectedID = id
        reflecting = false
        if visible != visibleIDs { visibleIDs = visible }
        if sets != setMembers { setMembers = sets }
        markLookedAtSeen()
        if closed != closedIDs { closedIDs = closed }
    }

    /// The sessions in sidebar order, with each set's together.
    var sidebarIDs: [String] {
        PaneSets.rows(order: sessions.map(\.id), sets: setMembers).map(\.id)
    }

    /// The sidebar takes or gives up the keyboard. Marks only last while it has it.
    func setSidebarFocused(_ focused: Bool) {
        guard focused != sidebarFocused else { return }
        sidebarFocused = focused
        markedIDs = []
        if focused { cursorID = selectedID ?? sidebarIDs.first }
    }

    func setCursor(_ id: String) {
        cursorID = id
    }

    func moveCursor(by step: Int) {
        let ids = sidebarIDs
        guard !ids.isEmpty else { return }
        guard let cursorID, let index = ids.firstIndex(of: cursorID) else {
            self.cursorID = ids.first
            return
        }
        self.cursorID = ids[max(0, min(ids.count - 1, index + step))]
    }

    func toggleMarkAtCursor() {
        guard let cursorID else { return }
        if markedIDs.remove(cursorID) == nil { markedIDs.insert(cursorID) }
    }

    /// What an action applies to, in sidebar order: what's marked, else the cursor
    /// while the sidebar has the keyboard, else the selected session.
    var actionTargets: [Session] {
        let ids: Set<String>
        switch (sidebarFocused, cursorID) {
        case (true, _) where !markedIDs.isEmpty: ids = markedIDs
        case (true, let cursor?): ids = [cursor]
        default: ids = selectedID.map { [$0] } ?? []
        }
        return inSidebarOrder(ids)
    }

    /// What a right click on a row applies to: all that's marked if it's marked.
    func actionTargets(forRow session: Session) -> [Session] {
        markedIDs.contains(session.id) ? inSidebarOrder(markedIDs) : [session]
    }

    private func inSidebarOrder(_ ids: Set<String>) -> [Session] {
        let byID = Dictionary(uniqueKeysWithValues: sessions.map { ($0.id, $0) })
        return sidebarIDs.filter(ids.contains).compactMap { byID[$0] }
    }

    /// The sidebar's cursor and marks while it has the keyboard, to come back to.
    var sidebarState: (cursor: String?, marks: Set<String>)? {
        sidebarFocused ? (cursorID, markedIDs) : nil
    }

    func restoreSidebar(_ state: (cursor: String?, marks: Set<String>)) {
        cursorID = state.cursor
        markedIDs = state.marks
    }

    func clearMarks() {
        markedIDs = []
    }

    /// Renames sessions: to the name for one, numbered in order for several.
    func rename(_ targets: [Session], to base: String) {
        struct Mutation: Encodable {
            let kind: String
            let id: String
            let text: String
        }
        for (session, name) in zip(targets, RenameNames.names(base: base, count: targets.count)) {
            run(failure: "Couldn't rename the session") { conn in
                try conn.send("mutate", Mutation(kind: "session.rename", id: session.id, text: name))
            } done: { _ in }
        }
    }

    /// Archives sessions, leaving the cursor on the next row.
    func archive(_ targets: [Session]) {
        let gone = Set(targets.map(\.id))
        if let cursorID, gone.contains(cursorID) {
            let ids = sidebarIDs
            let index = ids.firstIndex(of: cursorID) ?? 0
            self.cursorID = ids[index...].first { !gone.contains($0) } ?? ids[..<index].last { !gone.contains($0) }
        }
        markedIDs = []
        targets.forEach { archive($0.id) }
    }

    /// Clears the unseen mark on sessions you're looking at: on screen, with the app
    /// in front. Call it when any of that changes.
    func markLookedAtSeen() {
        guard NSApp.isActive, !inboxOpen else { return }
        for item in queue where !item.read && visibleIDs.contains(item.sessionID) {
            if readInFlight.insert(item.id).inserted {
                setInboxRead(item.id, true) { self.readInFlight.remove(item.id) }
            }
        }
        let looked = sessions.filter { $0.unseen && visibleIDs.contains($0.id) }.map(\.id)
        for id in looked where seenInFlight.insert(id).inserted {
            run(failure: "Couldn't update the session") { conn in
                try conn.send("mutate", Mutation(kind: "session.seen", id: id))
            } done: { _ in
                self.seenInFlight.remove(id)
            }
        }
    }

    @ObservationIgnored private var seenInFlight: Set<String> = []
    @ObservationIgnored private var readInFlight: Set<Int64> = []

    // MARK: Inbox

    func session(id: String) -> Session? {
        sessions.first { $0.id == id } ?? archived.first { $0.id == id }
    }

    /// Unread items in the queue, for one session or for all.
    func unreadCount(session id: String? = nil) -> Int {
        queue.filter { !$0.read && (id == nil || $0.sessionID == id) }.count
    }

    private struct InboxMutation: Encodable {
        let kind: String
        let item_ids: [Int64]
        var flag = false
    }

    private struct InboxRequest: Encodable {
        let session_id: String?
    }

    private struct InboxAnswerRequest: Encodable {
        let id: Int64
        let option: Int?
        let text: String?
    }

    /// Puts the number of unread messages on the app's Dock icon.
    private func showUnreadOnDock() {
        let unread = unreadCount()
        let label = unread > 0 ? String(unread) : nil
        guard NSApp.dockTile.badgeLabel != label else { return }
        NSApp.dockTile.badgeLabel = label
        // Only an app bundle has a notification center.
        guard Bundle.main.bundleIdentifier != nil else { return }
        UNUserNotificationCenter.current().setBadgeCount(unread) { error in
            if let error { NSLog("dock badge: %@", error.localizedDescription) }
        }
    }

    /// The shortcut in the agent view: stops whatever is being read, or reads the
    /// latest message from the session on show.
    func toggleReadingSelected() {
        let speech = SpeechController.shared
        if speech.isSpeaking {
            speech.stop()
            return
        }
        guard let session = selected else { return }
        loadInbox(session: session.id) { items in
            if let latest = items.first {
                speech.speak(latest)
            } else {
                speech.fail("\(session.displayName) hasn't sent a message yet.")
            }
        }
    }

    func loadInbox(session: String?, done: @escaping ([InboxItem]) -> Void) {
        run(failure: "Couldn't read the inbox") { conn -> [InboxItem] in
            try conn.call("inbox", InboxRequest(session_id: session))
        } done: { done($0) }
    }

    func setInboxRead(_ id: Int64, _ read: Bool, done: @escaping () -> Void = {}) {
        run(failure: "Couldn't update the item") { conn in
            try conn.send("mutate", InboxMutation(kind: "inbox.read", item_ids: [id], flag: read))
        } done: { _ in done() }
    }

    func dismissInbox(_ id: Int64, done: @escaping () -> Void = {}) {
        run(failure: "Couldn't dismiss the item") { conn in
            try conn.send("mutate", InboxMutation(kind: "inbox.dismiss", item_ids: [id]))
        } done: { _ in done() }
    }

    /// Types an answer into the item's session, then reports why it failed, if it did.
    func answerInbox(_ id: Int64, option: Int?, text: String?, done: @escaping (String?) -> Void) {
        calls.async {
            var failure: String?
            do {
                let conn = try DaemonConnection(socket: DaemonPaths.socket)
                try conn.send("inbox.answer", InboxAnswerRequest(id: id, option: option, text: text))
            } catch {
                failure = error.localizedDescription
            }
            DispatchQueue.main.async { done(failure) }
        }
    }

    private struct Mutation: Encodable {
        let kind: String
        let id: String
    }

    private let calls = DispatchQueue(label: "acmd.daemon.calls")

    /// Follows the daemon's state until the app quits.
    func start() {
        Thread.detachNewThread { [weak self] in self?.follow() }
    }

    private func follow() {
        struct Watch: Encodable {
            let rev: UInt64
            let wait_ms: Int
            let app: Bool
        }
        struct Watched: Decodable {
            let rev: UInt64
        }
        var startedDaemon = false
        var loadedLayout = false
        while true {
            do {
                let conn = try DaemonConnection(socket: DaemonPaths.socket)
                // The state first: a daemon with no changes yet holds a watch
                // from revision 0 until it times out.
                let first: Snapshot = try conn.call("snapshot")
                publish(first)
                if !loadedLayout {
                    struct Layout: Decodable {
                        let json: String
                    }
                    let layout: Layout = try conn.call("layout")
                    loadedLayout = true
                    DispatchQueue.main.async { self.onLayoutLoaded?(layout.json) }
                }
                var rev: UInt64 = 0
                while true {
                    let watched: Watched = try conn.call(
                        "watch", Watch(rev: rev, wait_ms: 25_000, app: true))
                    rev = watched.rev
                    let snapshot: Snapshot = try conn.call("snapshot")
                    publish(snapshot)
                }
            } catch {
                if !startedDaemon {
                    startedDaemon = true
                    Self.startDaemon()
                }
                DispatchQueue.main.async { self.problem = error.localizedDescription }
                Thread.sleep(forTimeInterval: 1)
            }
        }
    }

    private func publish(_ snapshot: Snapshot) {
        // Only sessions acmd runs, oldest first: ids are time-ordered ULIDs.
        let hosted = (snapshot.sessions ?? []).filter { $0.kind == "hosted" }.sorted { $0.id < $1.id }
        let visible = hosted.filter { !$0.isArchived }
        let archived = hosted.filter(\.isArchived)
        DispatchQueue.main.async {
            self.problem = nil
            if visible != self.sessions {
                self.sessions = visible
                self.onListChanged?(Set(visible.map(\.id)))
            }
            if archived != self.archived { self.archived = archived }
            let queue = snapshot.inbox ?? []
            if queue != self.queue {
                self.queue = queue
                self.onInboxChanged?()
            }
            // Items from before this launch aren't news.
            let newest = max(self.newestItem ?? 0, queue.map(\.id).max() ?? 0)
            if let seen = self.newestItem {
                let fresh = queue.filter { $0.id > seen }
                if !fresh.isEmpty { self.onNewItems?(fresh) }
            }
            self.newestItem = newest
            self.markLookedAtSeen()
            self.onNotices?(snapshot.notices ?? [])
            self.showUnreadOnDock()
        }
    }

    /// The daemon takes a single-instance lock, so a spare start is harmless.
    /// It runs through a shell so it outlives the app.
    private static func startDaemon() {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", "nohup \"$0\" daemon >/dev/null 2>&1 &", DaemonPaths.acmd]
        try? process.run()
    }

    /// Starts a new session in a folder. harness is "claude" or "terminal". It's
    /// selected, unless placed puts it somewhere itself.
    func newSession(harness: String, in folder: String, placed: ((Session) -> Void)? = nil) {
        struct New: Encodable {
            let cwd: String
            let harness: String
        }
        run(failure: "Couldn't start the session") { conn in
            let session: Session = try conn.call("new", New(cwd: folder, harness: harness))
            return session
        } done: { session in
            if !self.sessions.contains(session) { self.sessions.append(session) }
            if let placed {
                placed(session)
            } else {
                self.selectedID = session.id
            }
        }
    }

    /// Resumes a stopped session.
    func resume(_ id: String, done: @escaping () -> Void) {
        struct Mutation: Encodable {
            let kind: String
            let id: String
        }
        run(failure: "Couldn't resume the session") { conn in
            let session: Session = try conn.call("resume", Mutation(kind: "", id: id))
            return session
        } done: { _ in
            done()
        }
    }

    /// Keeps the pane layout in the daemon, so it comes back after a restart.
    func saveLayout(_ json: String) {
        struct Layout: Encodable {
            let json: String
        }
        run(failure: "Couldn't save the layout") { conn in
            try conn.send("layout.set", Layout(json: json))
        } done: { _ in }
    }

    /// Lists earlier Claude sessions that can be resumed. done gets an error
    /// message when that fails.
    func loadConversations(done: @escaping ([Conversation], String?) -> Void) {
        calls.async {
            do {
                let conn = try DaemonConnection(socket: DaemonPaths.socket)
                let list: [Conversation] = try conn.call("conversations")
                DispatchQueue.main.async { done(list, nil) }
            } catch {
                DispatchQueue.main.async { done([], error.localizedDescription) }
            }
        }
    }

    /// Continues an earlier conversation, by its id or an id prefix, and selects it.
    func resumeConversation(_ id: String) {
        struct New: Encodable {
            let cwd: String
            let resume: String
        }
        run(failure: "Couldn't resume the session") { conn in
            let session: Session = try conn.call("new", New(cwd: "", resume: id))
            return session
        } done: { session in
            if !self.sessions.contains(session) { self.sessions.append(session) }
            if self.selectedID == session.id {
                self.onSelect?(session)
            } else {
                self.selectedID = session.id
            }
        }
    }

    /// Ends a session. An agent can be unarchived later; a terminal can't.
    func archive(_ id: String) { mutate("session.archive", id, failure: "Couldn't archive the session") }

    /// Lists an archived agent again. It starts when it's activated.
    func unarchive(_ id: String) { mutate("session.unarchive", id, failure: "Couldn't unarchive the session") }

    private func mutate(_ kind: String, _ id: String, failure: String) {
        run(failure: failure) { conn in
            try conn.send("mutate", Mutation(kind: kind, id: id))
        } done: { _ in }
    }

    private func run<T>(
        failure: String,
        _ work: @escaping (DaemonConnection) throws -> T,
        done: @escaping (T) -> Void
    ) {
        calls.async {
            do {
                let result = try work(try DaemonConnection(socket: DaemonPaths.socket))
                DispatchQueue.main.async { done(result) }
            } catch {
                DispatchQueue.main.async { self.problem = "\(failure): \(error.localizedDescription)" }
            }
        }
    }
}
