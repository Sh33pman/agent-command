import AppKit
import Observation
import UserNotifications

/// The daemon's sessions, kept current for the UI. Everything here is on the
/// main thread; daemon calls run on background queues.
@Observable
final class SessionStore {
    var sessions: [Session] = []
    private(set) var archived: [Session] = []
    var selectedID: String? {
        didSet {
            if selectedID != oldValue && !reflecting { onSelect?(selected) }
        }
    }
    @ObservationIgnored private var reflecting = false
    /// Sessions showing in panes, the selected one among them.
    private(set) var visibleIDs: Set<String> = []
    /// The daemon's sets: each set's id and its listed sessions, in order.
    var sets: [String: [String]] = [:]
    /// Each set's sessions, in order.
    var setMembers: [[String]] { sets.keys.sorted().compactMap { sets[$0] } }
    /// Called with the daemon's sets when they change, so the panes follow.
    @ObservationIgnored var onSetsChanged: (([String: [String]]) -> Void)?
    /// Sessions in a set whose pane is closed.
    private(set) var closedIDs: Set<String> = []
    private(set) var problem: String?
    /// The Inbox's queue: what's left to deal with, in arrival order.
    var queue: [InboxItem] = []
    /// Covers the panes, so you're not looking at what they show.
    var inboxOpen = false
    /// Scheduled jobs, by name.
    var jobs: [Job] = []
    /// Waking the Mac for jobs; nil when the daemon can't.
    var wake: WakeState?
    /// The groups sessions can go in, Default last.
    var groups: [SessionGroup] = []
    /// Hosting, for Remote sessions; nil when the daemon has none.
    private(set) var remote: RemoteStatus?
    /// Called with Hosting's status, from each snapshot.
    @ObservationIgnored var onRemote: ((RemoteStatus?) -> Void)?
    /// The Remote hosts this Mac uses sessions on, and their connections.
    private(set) var remoteHosts: [RemoteHost] = []
    /// What's wrong with the host, from the daemon: config.toml with a mistake, for one.
    private(set) var health: [HealthItem] = []
    /// The health ids this app dismissed: the daemon keeps them, by client id.
    private(set) var dismissed: Set<String> = []
    /// What the host and this Mac can do.
    private(set) var features: HostFeatures?
    /// acmd's version, and a newer release if there is one.
    private(set) var update: UpdateState?
    /// Told once, with the first snapshot's health: launch's setup offer.
    @ObservationIgnored var onFirstHealth: (([HealthItem]) -> Void)?
    @ObservationIgnored private var healthArrived = false
    /// The daemon's health, setup and report ops.
    @ObservationIgnored var healthClient = HealthClient()
    /// Where changes to many sessions go (mutate with ids); tests swap it.
    @ObservationIgnored var mutations: DaemonCalls = DaemonSocketCalls()
    /// Where job saves go: the daemon, or a test's fake.
    @ObservationIgnored var jobCalls: DaemonCalls = DaemonSocketCalls()

    /// Stops showing a warning in this app, or shows it again.
    func setDismissed(_ id: String, _ on: Bool) {
        if on { dismissed.insert(id) } else { dismissed.remove(id) }
        let keep = dismissed
        if on {
            healthClient.dismiss(id, client: clientID)
        } else {
            // Restoring one: restore all, then dismiss the rest again.
            healthClient.dismiss(nil, client: clientID) { [weak self] _ in
                guard let self else { return }
                for other in keep { self.healthClient.dismiss(other, client: self.clientID) }
            }
        }
    }

    /// Shows every warning again.
    func restoreDismissed() {
        dismissed = []
        healthClient.dismiss(nil, client: clientID)
    }
    /// Which sessions the sidebar lists; kept for next time.
    var filter = SidebarFilter.load() {
        didSet { if filter != oldValue { filter.save(defaults) } }
    }
    /// How the sidebar lists them: groups or a list, the sort, and what's
    /// collapsed. This app's own view, kept for next time.
    var options = SidebarOptions.load() {
        didSet { if options != oldValue { options.save(defaults) } }
    }
    @ObservationIgnored var defaults = UserDefaults.standard
    /// The daemon's Remote host ops; tests swap its calls.
    @ObservationIgnored var remoteClient = RemoteHostsClient()

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
    /// Whether the main window is open: closing it leaves the app running in the background.
    @ObservationIgnored var isWindowShown = true
    /// Whether you're in the main window, as against another app or the app in the background.
    /// (No app at all, as in tests run in any order, is not in front.)
    var isInMainWindow: Bool { NSApp?.isActive == true && isWindowShown }
    @ObservationIgnored private var news: InboxNews?
    /// Called when the queue changes, for an open Inbox.
    @ObservationIgnored var onInboxChanged: (() -> Void)?
    /// Called with the daemon's recent notices, whenever its state is read.
    @ObservationIgnored var onNotices: (([Notice]) -> Void)?
    /// Called once with the saved pane layout, after the first list of sessions.
    @ObservationIgnored var onLayoutLoaded: ((String) -> Void)?

    var selected: Session? { sessions.first { $0.id == selectedID } }

    /// Shows what the panes show, without asking them to change.
    func reflect(selected id: String?, visible: Set<String>, closed: Set<String>) {
        reflecting = true
        selectedID = id
        reflecting = false
        if visible != visibleIDs { visibleIDs = visible }
        markLookedAtSeen()
        if closed != closedIDs { closedIDs = closed }
    }

    /// The sidebar's rows: group headers and the sessions the filter lists.
    var sidebarRows: [SidebarRow] {
        let filter = shownFilter
        return SidebarLayout.rows(sessions: sessions.filter(filter.includes), groups: groups, sets: setMembers,
                                  options: options, filtering: filter != .all)
    }

    /// The sessions you can see in the sidebar, top to bottom: what the numbers and
    /// the cursor go by. Those in a collapsed group aren't among them.
    var sidebarIDs: [String] { SidebarLayout.sessionIDs(sidebarRows) }

    /// Collapses a group to its header, or opens it again.
    func toggleCollapsed(_ groupID: String) {
        if options.collapsed.remove(groupID) == nil { options.collapsed.insert(groupID) }
    }

    /// The hosts the sidebar's filter offers.
    var filterHosts: [String] { SidebarFilter.hostNames(known: remoteHosts, sessions: sessions) }

    /// The filter as it applies now: a host that's gone lists everything.
    var shownFilter: SidebarFilter { filter.effective(hosts: filterHosts) }

    /// What a session's row says about where it runs. clients counts the
    /// connections attached to it, for one shared from this Mac.
    func place(of session: Session, clients: [String: Int] = [:]) -> SessionPlace {
        SessionPlace(session, hosts: remoteHosts, clients: clients[session.id] ?? 0)
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

    /// The session numbered n, from 1, counting down the sidebar.
    func sessionID(number n: Int) -> String? {
        let ids = sidebarIDs
        return n >= 1 && n <= ids.count ? ids[n - 1] : nil
    }

    /// Sessions that need you, as the daemon ranks them: the most urgent first,
    /// and within a rank the one waiting longest. Whatever the filter shows, so
    /// nothing that needs you is missed.
    var attentionIDs: [String] {
        sessions.filter { !$0.isArchived && $0.attention != nil }.sorted { a, b in
            let (x, y) = (a.attention!, b.attention!)
            if x.rank != y.rank { return x.rank < y.rank }
            if x.sinceDate != y.sinceDate { return x.sinceDate < y.sinceDate }
            return a.id < b.id
        }.map(\.id)
    }

    /// The next session that needs you after the selected one, going round; nil when
    /// none does.
    var nextAttentionID: String? {
        let ids = attentionIDs
        guard let selectedID, let index = ids.firstIndex(of: selectedID) else { return ids.first }
        return ids[(index + 1) % ids.count]
    }

    /// Shows a session, and moves the sidebar's cursor to it while it has the keyboard.
    /// One the filter hides brings everything back into the list.
    func show(_ id: String) {
        if let session = sessions.first(where: { $0.id == id }), !shownFilter.includes(session) { filter = .all }
        if sidebarFocused { cursorID = id }
        selectedID = id
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

    /// Renames sessions: to the name for one; the daemon numbers several in order.
    func rename(_ targets: [Session], to base: String) {
        mutateMany("session.rename", targets.map(\.id), text: base, failure: "Couldn't rename")
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
        mutateMany("session.archive", targets.map(\.id), failure: "Couldn't archive")
    }

    /// Clears the unseen mark on sessions you're looking at: on screen, with the app
    /// in front. Call it when any of that changes.
    func markLookedAtSeen() {
        guard isInMainWindow, !inboxOpen else { return }
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
        var limit: Int?
        var cursor: String?
    }

    /// The most a whole-list load takes, page by page: what the daemon kept
    /// in one read before paging.
    static let inboxLoadMax = 500

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

    /// The Inbox, newest first, up to inboxLoadMax items: one session's, or
    /// everyone's.
    func loadInbox(session: String?, done: @escaping ([InboxItem]) -> Void) {
        run(failure: "Couldn't read the inbox") { conn -> [InboxItem] in
            var items: [InboxItem] = []
            var cursor: String?
            repeat {
                let page: Page<InboxItem> = try conn.call(
                    "inbox", InboxRequest(session_id: session, limit: Page<InboxItem>.max, cursor: cursor))
                items += page.items
                // An empty page ends it too, whatever its cursor says.
                cursor = page.nextCursor.isEmpty || page.items.isEmpty ? nil : page.nextCursor
            } while cursor != nil && items.count < Self.inboxLoadMax
            return Array(items.prefix(Self.inboxLoadMax))
        } done: { done($0) }
    }

    /// One page of the Inbox, from cursor ("" for the newest).
    func loadInboxPage(session: String?, cursor: String = "", done: @escaping (Page<InboxItem>) -> Void) {
        run(failure: "Couldn't read the inbox") { conn -> Page<InboxItem> in
            try conn.call("inbox", InboxRequest(session_id: session, cursor: cursor.isEmpty ? nil : cursor))
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
        var startedDaemon = false
        var loadedLayout = false
        var state = SnapshotAssembler()
        while true {
            do {
                let conn = try DaemonConnection(socket: DaemonPaths.socket)
                if !loadedLayout {
                    struct Layout: Codable {
                        var client_id: String?
                        var json: String = ""
                    }
                    var layout: Layout = try conn.call("layout", Layout(client_id: clientID))
                    if layout.json.isEmpty { // first start with an id: the one layout from before
                        layout = try conn.call("layout", Layout())
                    }
                    loadedLayout = true
                    DispatchQueue.main.async { self.onLayoutLoaded?(layout.json) }
                }
                // The daemon's stream: a snapshot, or after a dropped connection
                // only what was missed, then each change as it happens.
                try conn.subscribe(since: state.hasSnapshot ? state.rev : 0)
                while true {
                    var changed = state.apply(try conn.nextEvent())
                    while conn.hasLineWaiting { // changes that came together, shown together
                        changed = state.apply(try conn.nextEvent()) || changed
                    }
                    if changed, state.hasSnapshot { publish(try state.snapshot()) }
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

    func publish(_ snapshot: Snapshot) {
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
            let sets = Self.sets(of: visible, known: snapshot.sets ?? [])
            if sets != self.sets {
                self.sets = sets
                self.onSetsChanged?(sets)
            }
            if archived != self.archived { self.archived = archived }
            let jobs = snapshot.jobs ?? []
            if jobs != self.jobs { self.jobs = jobs }
            if snapshot.wake != self.wake { self.wake = snapshot.wake }
            let groups = (snapshot.groups ?? []).filter { !$0.isArchived }
            if groups != self.groups { self.groups = groups }
            if snapshot.remote != self.remote { self.remote = snapshot.remote }
            self.onRemote?(snapshot.remote)
            let hosts = snapshot.remoteHosts ?? []
            if hosts != self.remoteHosts { self.remoteHosts = hosts }
            if let settings = snapshot.settings, settings != HostSettings.shared.values { HostSettings.shared.values = settings }
            let health = snapshot.health ?? []
            if health != self.health { self.health = health }
            let dismissed = Set(snapshot.dismissed ?? [])
            if dismissed != self.dismissed { self.dismissed = dismissed }
            if snapshot.features != self.features { self.features = snapshot.features }
            if snapshot.update != self.update { self.update = snapshot.update }
            if snapshot.speech != SpeechController.shared.state { SpeechController.shared.apply(snapshot.speech) }
            if !self.healthArrived {
                self.healthArrived = true
                self.onFirstHealth?(health)
            }
            let queue = snapshot.inbox ?? []
            if queue != self.queue {
                self.queue = queue
                self.onInboxChanged?()
            }
            // Items from before this launch aren't news.
            if let news = self.news {
                let (fresh, next) = news.fresh(queue)
                if !fresh.isEmpty { self.onNewItems?(fresh) }
                self.news = next
            } else {
                self.news = InboxNews(queue)
            }
            self.markLookedAtSeen()
            self.onNotices?(snapshot.notices ?? [])
            self.showUnreadOnDock()
        }
    }

    /// Each set's listed sessions, in order, from the sessions that name it as
    /// their parent. A set with fewer than two listed is left out.
    static func sets(of sessions: [Session], known: [SessionSetEntity]) -> [String: [String]] {
        let ids = Set(known.map(\.id))
        var members: [String: [(order: Int, id: String)]] = [:]
        for session in sessions {
            guard let parent = session.parent, ids.contains(parent) else { continue }
            members[parent, default: []].append((session.order ?? 0, session.id))
        }
        return members.compactMapValues { list in
            list.count < 2 ? nil : list.sorted { ($0.order, $0.id) < ($1.order, $1.id) }.map(\.id)
        }
    }

    /// This app's id with the daemon, so it keeps a layout of its own. Made once.
    var clientID: String { ClientIdentity.id(defaults) }

    /// Makes sets in the daemon: the one-off move of sets from panes saved
    /// before it kept them. One that can't be made is left out.
    func createSets(_ sets: [(id: String, members: [String])]) {
        struct Create: Encodable {
            let kind = "set.create"
            let set_id: String
            let ids: [String]
        }
        guard !sets.isEmpty else { return }
        calls.async {
            guard let conn = try? DaemonConnection(socket: DaemonPaths.socket) else { return }
            for set in sets {
                do {
                    try conn.send("mutate", Create(set_id: set.id, ids: set.members))
                } catch {
                    NSLog("couldn't move set %@ to the daemon: %@", set.id, error.localizedDescription)
                }
            }
        }
    }

    // MARK: Groups

    private struct GroupMutation: Encodable {
        let kind: String
        var group_id: String?
        var text: String?
        var color: String?
        var ids: [String]?
        var after: String?
        var before: String?
    }

    private func mutateGroup(_ m: GroupMutation, failure: String) {
        run(failure: failure) { conn in
            try conn.send("mutate", m)
        } done: { _ in }
    }

    /// Makes a group, and moves sessions into it (with their sets).
    func createGroup(_ name: String, moving ids: [String] = []) {
        run(failure: "Couldn't make the group") { conn in
            let group: SessionGroup = try conn.call("mutate", GroupMutation(kind: "group.create", text: name))
            if !ids.isEmpty { try conn.send("mutate", GroupMutation(kind: "session.move", group_id: group.id, ids: ids)) }
            return group
        } done: { _ in }
    }

    func renameGroup(_ id: String, to name: String) {
        mutateGroup(GroupMutation(kind: "group.rename", group_id: id, text: name), failure: "Couldn't rename the group")
    }

    func setGroupColor(_ id: String, _ color: String) {
        mutateGroup(GroupMutation(kind: "group.color", group_id: id, color: color), failure: "Couldn't change the group's colour")
    }

    /// Deletes a group; its sessions move to Default.
    func deleteGroup(_ id: String) {
        mutateGroup(GroupMutation(kind: "group.delete", group_id: id), failure: "Couldn't delete the group")
    }

    /// Moves sessions to a group. A session in a set takes its set along.
    func moveSessions(_ ids: [String], to group: String) {
        mutateGroup(GroupMutation(kind: "session.move", group_id: group, ids: ids), failure: "Couldn't move the sessions")
    }

    /// Puts a group just before or just after another.
    func moveGroup(_ id: String, before: String? = nil, after: String? = nil) {
        mutateGroup(GroupMutation(kind: "group.move", group_id: id, after: after, before: before), failure: "Couldn't move the group")
    }

    /// Moves a group one place up or down among the groups you arranged.
    func moveGroup(_ id: String, by step: Int) {
        let ordered = SidebarLayout.orderedGroups(groups).filter { !$0.isDefault }
        guard let at = ordered.firstIndex(where: { $0.id == id }) else { return }
        let to = at + step
        guard ordered.indices.contains(to) else { return }
        if step < 0 { moveGroup(id, before: ordered[to].id) } else { moveGroup(id, after: ordered[to].id) }
    }

    /// Carries out a drop in the sidebar.
    func perform(_ drop: SidebarDrop) {
        switch drop {
        case .moveSessions(let ids, let group):
            moveSessions(ids, to: group)
            clearMarks()
        case .moveGroup(let id, let before, let after):
            moveGroup(id, before: before, after: after)
        }
    }

    /// Takes a session out of its set. It keeps running, on its own.
    func removeFromSet(_ id: String) { mutate("set.remove", id, failure: "Couldn't move the session out of its set") }

    /// The daemon takes a single-instance lock, so a spare start is harmless.
    /// It runs through a shell so it outlives the app.
    private static func startDaemon() {
        let process = Process()
        // The only thing passed in is our own binary, as a quoted $0.
        process.executableURL = URL(fileURLWithPath: "/bin/sh")
        process.arguments = ["-c", "nohup \"$0\" daemon >/dev/null 2>&1 &", DaemonPaths.acmd]
        try? process.run()
    }

    /// Starts a new session in a folder. harness is "claude", "antigravity" or "terminal". It's
    /// selected, unless placed puts it somewhere itself.
    /// remote makes it a Remote session or not; nil, as from ⌘N, is the config's
    /// `new_sessions_remote`.
    /// setWith puts it in a set with that session, for a split.
    func newSession(harness: String, in folder: String, remote: Bool? = nil, setWith: String? = nil,
                    placed: ((Session) -> Void)? = nil) {
        struct New: Encodable {
            let cwd: String
            let harness: String
            let remote: Bool?
            let set_with: String?
        }
        run(failure: "Couldn't start the session") { conn in
            let session: Session = try conn.call("new", New(cwd: folder, harness: harness, remote: remote, set_with: setWith))
            return session
        } done: { session in
            self.adopt(session)
            if let placed {
                placed(session)
            } else {
                self.selectedID = session.id
            }
        }
    }

    /// Lists a session the daemon just made, before its next snapshot does.
    func adopt(_ session: Session) {
        if !sessions.contains(where: { $0.id == session.id }) { sessions.append(session) }
    }

    /// Shows a session, listing it first if it's new here.
    func focus(_ session: Session) {
        adopt(session)
        show(session.id)
    }

    // MARK: Remote

    /// Turns Remote on or off for this Mac's sessions: clients of its Hosting
    /// may see and use a Remote session. Sessions added from a host are skipped.
    func setRemote(_ targets: [Session], _ on: Bool) {
        let ids = targets.filter { $0.can(.setRemote) }.map(\.id)
        mutateMany("session.remote", ids, flag: on, failure: on ? "Couldn't make Remote" : "Couldn't turn Remote off for")
    }

    /// Starts a session on a host, then shows it here. done gets why it couldn't, if it couldn't.
    /// setWith puts it in a set with that session from the same host, and placed
    /// then puts it in the panes instead of selecting it: a split.
    func newRemoteSession(host: String, dir: String, harness: Harness?, setWith: String? = nil,
                          placed: ((Session) -> Void)? = nil, done: @escaping (String?) -> Void = { _ in }) {
        remoteClient.newSession(host: host, dir: dir, harness: harness?.rawValue, setWith: setWith) { [weak self] result in
            switch result {
            case .success(let session):
                if let placed {
                    self?.adopt(session)
                    placed(session)
                } else {
                    self?.focus(session)
                }
                done(nil)
            case .failure(let failure):
                done(errorText(failure))
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
            let client_id: String
            let json: String
        }
        let id = clientID
        run(failure: "Couldn't save the layout") { conn in
            try conn.send("layout.set", Layout(client_id: id, json: json))
        } done: { _ in }
    }

    /// Lists earlier agent conversations (Claude's and Antigravity's) that can be resumed. done gets an error
    /// message when that fails.
    func loadConversations(done: @escaping ([Conversation], String?) -> Void) {
        calls.async {
            do {
                let conn = try DaemonConnection(socket: DaemonPaths.socket)
                var list: [Conversation] = []
                var cursor = ""
                repeat { // all of them, for the picker's search
                    let page: Page<Conversation> = try conn.call("conversations", PageRequest(limit: Page<Conversation>.max, cursor: cursor))
                    list += page.items
                    cursor = page.items.isEmpty ? "" : page.nextCursor
                } while !cursor.isEmpty
                DispatchQueue.main.async { done(list, nil) }
            } catch {
                DispatchQueue.main.async { done([], error.localizedDescription) }
            }
        }
    }

    /// Continues an earlier conversation of an agent's, by its id or an id prefix, and
    /// selects it.
    func resumeConversation(_ id: String, harness: Harness = .claude) {
        struct New: Encodable {
            let cwd: String
            let harness: String
            let resume: String
        }
        run(failure: "Couldn't resume the session") { conn in
            let session: Session = try conn.call("new", New(cwd: "", harness: harness.rawValue, resume: id))
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

    /// One change to many sessions: the daemon answers for each, and what didn't
    /// change is said, with why.
    private func mutateMany(_ kind: String, _ ids: [String], text: String? = nil, flag: Bool? = nil, failure: String) {
        guard !ids.isEmpty else { return }
        struct Many: Encodable {
            let kind: String
            let ids: [String]
            var text: String?
            var flag: Bool?
        }
        let data = (try? JSONEncoder().encode(Many(kind: kind, ids: ids, text: text, flag: flag))) ?? Data()
        mutations.call("mutate", data) { [weak self] result in
            switch result {
            case .success(let reply):
                let results = (try? JSONDecoder().decode(BatchResults.self, from: reply)) ?? BatchResults()
                if let why = results.problem(failure) { self?.problem = why }
            case .failure(let error):
                self?.problem = "\(failure) the sessions: \(errorText(error))"
            }
        }
    }

    private func mutate(_ kind: String, _ id: String, failure: String) {
        run(failure: failure) { conn in
            try conn.send("mutate", Mutation(kind: kind, id: id))
        } done: { _ in }
    }

    // MARK: Scheduled jobs

    private struct JobRef: Encodable {
        var id = ""
        var paused = false
        var harness = ""
    }

    /// Checks a job without saving it: its schedule read back, its next runs and
    /// what's wrong. done gets nil when the daemon can't be reached.
    func checkJob(_ job: Job, done: @escaping (JobCheck?) -> Void) {
        calls.async {
            let check: JobCheck? = try? DaemonConnection(socket: DaemonPaths.socket).call("job.check", job)
            DispatchQueue.main.async { done(check) }
        }
    }

    /// Saves a job, new or edited. done gets the saved job, or why it wasn't saved.
    func saveJob(_ job: Job, done: @escaping (Result<Job, Error>) -> Void) {
        let input: Data
        do {
            input = try JSONEncoder().encode(job)
        } catch {
            return done(.failure(error))
        }
        jobCalls.call("job.save", input) { reply in
            let result = reply.flatMap { data in Result { try JSONDecoder().decode(Job.self, from: data) } }
            if case .success(let saved) = result {
                if let i = self.jobs.firstIndex(where: { $0.id == saved.id }) {
                    self.jobs[i] = saved
                } else {
                    self.jobs.append(saved)
                }
            }
            done(result)
        }
    }

    func deleteJob(_ id: String) {
        jobs.removeAll { $0.id == id }
        run(failure: "Couldn't delete the job") { conn in
            try conn.send("job.delete", JobRef(id: id))
        } done: { _ in }
    }

    func setJobPaused(_ id: String, _ paused: Bool) {
        if let i = jobs.firstIndex(where: { $0.id == id }) { jobs[i].paused = paused }
        run(failure: paused ? "Couldn't pause the job" : "Couldn't resume the job") { conn in
            try conn.send("job.pause", JobRef(id: id, paused: paused))
        } done: { _ in }
    }

    /// Runs a job now. done gets its run, or why it couldn't start.
    func runJob(_ id: String, done: @escaping (Result<JobRun, Error>) -> Void) {
        calls.async {
            let result = Result { () throws -> JobRun in
                try DaemonConnection(socket: DaemonPaths.socket).call("job.run", JobRef(id: id))
            }
            DispatchQueue.main.async { done(result) }
        }
    }

    /// A job's runs, newest first.
    func loadJobRuns(_ id: String, done: @escaping ([JobRun]) -> Void) {
        struct Runs: Encodable {
            let id: String
            let limit: Int
        }
        // A job keeps 100 runs at most: one page has them all.
        run(failure: "Couldn't read the job's runs") { conn -> [JobRun] in
            let page: Page<JobRun> = try conn.call("job.runs", Runs(id: id, limit: Page<JobRun>.max))
            return page.items
        } done: { done($0) }
    }

    /// The settings an agent takes for scheduled runs.
    func loadAgentSettings(harness: String = "", done: @escaping ([AgentSetting]) -> Void) {
        run(failure: "Couldn't read the agent's settings") { conn -> [AgentSetting] in
            try conn.call("agent.settings", JobRef(harness: harness))
        } done: { done($0) }
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

/// What a change to many sessions did, one result each (internal/ipc.Results).
struct BatchResults: Decodable, Equatable {
    struct Result: Decodable, Equatable {
        let id: String
        let ok: Bool
        var code: String?
        var error: String?
    }

    var results: [Result] = []

    enum CodingKeys: String, CodingKey { case results }

    init(results: [Result] = []) { self.results = results }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        results = try c.decodeIfPresent([Result].self, forKey: .results) ?? []
    }

    /// "Couldn't archive 1 of 5 sessions: studio can't be reached", or nil when all did.
    func problem(_ failure: String) -> String? {
        let failed = results.filter { !$0.ok }
        guard let first = failed.first else { return nil }
        let what = results.count == 1 ? "the session" : "\(failed.count) of \(results.count) sessions"
        return "\(failure) \(what): \(first.error ?? first.code ?? "it failed")"
    }
}
