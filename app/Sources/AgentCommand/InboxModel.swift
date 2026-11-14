import AppKit
import Observation

/// Where the Inbox puts the message box, relative to the list of messages.
enum InboxLayout: String {
    case columns, stacked

    static let key = "inbox.layout"
}

/// What the Inbox lists.
enum InboxFilter: Hashable {
    /// What's left to deal with, in arrival order.
    case queue
    /// Everything, newest first.
    case all
    /// One session's items, newest first.
    case session(String)
    /// The items of a set's sessions, newest first.
    case set([String])
}

/// Which part of the Inbox has the keyboard, unless the reply box does.
enum InboxPanel {
    case sessions, messages
}

/// The Inbox's state: what's listed, what's open and what you've typed. Its panels
/// work like the sidebar: a click or the arrows move a cursor, and return or a double
/// click opens what's under it.
@Observable
final class InboxModel {
    private let store: SessionStore
    /// Called to leave the Inbox.
    @ObservationIgnored var close: () -> Void = {}
    /// Called to show a session.
    @ObservationIgnored var goToSession: (String) -> Void = { _ in }
    /// The sidebar's actions on sessions.
    @ObservationIgnored var rename: ([Session]) -> Void = { _ in }
    @ObservationIgnored var archive: ([Session]) -> Void = { _ in }
    @ObservationIgnored var moveOut: (Session) -> Void = { _ in }
    /// Called when the last item leaves the queue.
    @ObservationIgnored var onQueueEmptied: () -> Void = {}

    private(set) var filter: InboxFilter
    /// The message open below or beside the list.
    private(set) var selectedID: Int64? {
        didSet { if selectedID != oldValue { messageChanged() } }
    }
    private(set) var history: [InboxItem] = []
    /// Every session's items, for the counts beside the sessions.
    private(set) var allItems: [InboxItem] = []
    private(set) var error: String?
    private(set) var sending = false
    /// Drafts by session: a newer item from it keeps what you were writing.
    var drafts: [String: String] = [:]
    /// Whether the reply box should have the keyboard.
    var typing = false
    /// The option picked out with the keyboard, and the message it belongs to.
    private var choice: (item: Int64, index: Int)?
    private(set) var panel = InboxPanel.messages
    /// Where each panel's cursor is, and what's marked with x.
    private(set) var sessionCursor: InboxFilter
    private(set) var messageCursor: Int64? {
        didSet {
            if let session = items.first(where: { $0.id == messageCursor })?.sessionID { cursorSession = session }
        }
    }
    /// Says that a new message has come in, for a few seconds.
    private(set) var notice: String?
    private(set) var markedSessions: Set<String> = []
    private(set) var markedMessages: Set<Int64> = []

    @ObservationIgnored private var lastIndex = 0
    @ObservationIgnored private var hadQueue = false
    /// Whether the next list to load shows its first message.
    @ObservationIgnored private var showsFirst = false
    /// The session of the message under the cursor, known even once that message has gone.
    @ObservationIgnored private var cursorSession: String?
    /// A session whose message left while on show: its next one takes over, and is read
    /// if the one before it was.
    @ObservationIgnored private var following: (session: String, speak: Bool)?
    /// Messages you answered or dismissed, as against ones that went by themselves.
    @ObservationIgnored private var leftByYou: Set<Int64> = []
    /// What the queue held last time, to tell what's new.
    @ObservationIgnored private var knownItems: Set<Int64>?
    /// Stops the popup reading a message it moved to by itself.
    @ObservationIgnored private var movingQuietly = false
    @ObservationIgnored private var noticeTimer: Timer?
    /// The popup shows the message under the cursor, without opening it first.
    private let showsCursor: Bool
    /// Whether a message on show counts as read: only once you're using the popup.
    @ObservationIgnored var canMarkRead: () -> Bool = { true }
    /// Whether a key press is for this Inbox: the popup can sit beside the app's window.
    @ObservationIgnored var handlesKey: (NSEvent) -> Bool = { _ in true }
    @ObservationIgnored private var shownAt = Date.distantPast
    @ObservationIgnored private var monitor: Any?

    init(store: SessionStore, filter: InboxFilter, showsCursor: Bool = false) {
        self.store = store
        self.filter = filter
        self.showsCursor = showsCursor
        sessionCursor = filter
    }

    var items: [InboxItem] {
        filter == .queue ? store.queue : history
    }

    var selected: InboxItem? {
        items.first { $0.id == selectedID }
    }

    private var cursorItem: InboxItem? {
        items.first { $0.id == messageCursor }
    }

    /// What an action on messages applies to: what's marked, else the cursor.
    private var messageTargets: [InboxItem] {
        markedMessages.isEmpty ? (cursorItem.map { [$0] } ?? []) : items.filter { markedMessages.contains($0.id) }
    }

    /// The sessions listed on the left, as the sidebar lists them: sets together. Only
    /// agents: a plain terminal, or one whose agent left no messages, has nothing here.
    var rows: [(session: Session, link: SetLink)] {
        let senders = Set(allItems.map(\.sessionID))
        let agents = store.sessions.filter { !$0.isTerminal || !$0.claudeSessionID.isEmpty || senders.contains($0.id) }
        let byID = Dictionary(uniqueKeysWithValues: agents.map { ($0.id, $0) })
        return PaneSets.rows(order: agents.map(\.id), sets: store.setMembers).compactMap { row in
            byID[row.id].map { ($0, row.link) }
        }
    }

    var queueCount: Int { store.queue.count }

    func unreadCount(_ sessionID: String) -> Int {
        allItems.filter { !$0.read && $0.sessionID == sessionID }.count
    }

    /// The set a session is in, as the Inbox lists it.
    func setMembers(of sessionID: String) -> [String] {
        store.setMembers.first { $0.contains(sessionID) } ?? []
    }

    func unreadCount(set members: [String]) -> Int {
        allItems.filter { !$0.read && members.contains($0.sessionID) }.count
    }

    /// What this session is waiting on, if anything.
    func openItem(_ sessionID: String) -> InboxItem? {
        store.queue.first { $0.sessionID == sessionID }
    }

    func session(of item: InboxItem) -> Session? {
        store.session(id: item.sessionID)
    }

    func sessionName(_ id: String) -> String {
        store.session(id: id)?.displayName ?? "Deleted session"
    }

    // MARK: Showing

    /// Starts showing. With a cursor, it opens on that message, so nothing else gets
    /// shown or read first.
    func start(cursor: InboxItem? = nil) {
        if let cursor { messageCursor = cursor.id }
        // Opened for a session or set: as if you'd chosen it on the left.
        showsFirst = filter != .queue
        store.onInboxChanged = { [weak self] in self?.reload() }
        reload()
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, self.handlesKey(event) else { return event }
            return self.handle(event) ? nil : event
        }
    }

    func stop() {
        noticeTimer?.invalidate()
        SpeechController.shared.stop()
        store.onInboxChanged = nil
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    /// Opens something from the left panel: its messages, ready to move through.
    func setFilter(_ next: InboxFilter) {
        filter = next
        sessionCursor = next
        showsFirst = true
        following = nil
        knownItems = nil
        selectedID = nil
        messageCursor = nil
        lastIndex = 0
        typing = false
        error = nil
        markedMessages = []
        setPanel(.messages)
        reload()
    }

    /// Moves the keyboard to a panel. Marks don't carry over, as in the sidebar.
    func setPanel(_ next: InboxPanel) {
        typing = false
        choice = nil
        guard next != panel else { return }
        panel = next
        markedSessions = []
        markedMessages = []
        if next == .sessions { sessionCursor = filter }
    }

    /// A click on the left panel: the cursor moves, and nothing opens.
    func click(filter: InboxFilter) {
        setPanel(.sessions)
        sessionCursor = filter
    }

    /// A click on a message: the cursor moves, and nothing opens.
    func click(item: InboxItem) {
        following = nil
        setPanel(.messages)
        messageCursor = item.id
        lastIndex = items.firstIndex { $0.id == item.id } ?? 0
        showCursor()
    }

    /// In the popup, shows the message under the cursor.
    private func showCursor() {
        guard showsCursor else { return }
        if selectedID != messageCursor {
            selectedID = messageCursor
            shownAt = Date()
            typing = false
            error = nil
        }
        markShownRead()
    }

    /// Reading stops when you leave a message. In the popup, the next can start by
    /// itself, but only the one on show: it doesn't run through the queue.
    private func messageChanged() {
        let speech = SpeechController.shared
        speech.stop()
        if showsCursor, speech.isEnabled, PopupSettings.shared.autoPlay, !movingQuietly, let selected {
            speech.speak(selected)
        }
    }

    /// The shortcut: stops whatever is being read, or reads the message on show.
    private func toggleSpeech() {
        let speech = SpeechController.shared
        if speech.isSpeaking {
            speech.stop()
        } else if let item = selected ?? cursorItem {
            speech.speak(item)
        }
    }

    /// Marks the message on show as read, if you're using the popup.
    func markShownRead() {
        guard showsCursor, canMarkRead(), let selected, !selected.read else { return }
        setRead(selected, true)
    }

    /// Where this message is in the list, and how many there are.
    var position: (index: Int, count: Int)? {
        items.firstIndex { $0.id == selectedID }.map { ($0 + 1, items.count) }
    }

    /// Goes to the next or previous message.
    func step(_ by: Int) {
        moveCursor(by)
    }

    /// What the left panel lists, in order.
    private var panelFilters: [InboxFilter] {
        var filters = [InboxFilter.queue, .all]
        for row in rows {
            if row.link == .first { filters.append(.set(setMembers(of: row.session.id))) }
            filters.append(.session(row.session.id))
        }
        return filters
    }

    private func moveCursor(_ by: Int) {
        switch panel {
        case .sessions:
            let filters = panelFilters
            let index = filters.firstIndex(of: sessionCursor) ?? 0
            sessionCursor = filters[max(0, min(filters.count - 1, index + by))]
        case .messages:
            following = nil
            let list = items
            guard !list.isEmpty else { return }
            let index = list.firstIndex { $0.id == messageCursor } ?? 0
            let next = max(0, min(list.count - 1, index + by))
            messageCursor = list[next].id
            lastIndex = next
            showCursor()
        }
    }

    private func toggleMark() {
        switch panel {
        case .sessions:
            guard case .session(let id) = sessionCursor else { return }
            if markedSessions.remove(id) == nil { markedSessions.insert(id) }
        case .messages:
            guard let id = messageCursor else { return }
            if markedMessages.remove(id) == nil { markedMessages.insert(id) }
        }
    }

    private func openCursor() {
        switch panel {
        case .sessions: setFilter(sessionCursor)
        case .messages: if let cursorItem { open(cursorItem) }
        }
    }

    /// The sessions an action on sessions applies to, from whichever panel has the keyboard.
    private var sessionTargets: [Session] {
        switch panel {
        case .sessions:
            if !markedSessions.isEmpty {
                return rows.map(\.session).filter { markedSessions.contains($0.id) }
            }
            guard case .session(let id) = sessionCursor, let session = store.session(id: id) else { return [] }
            return [session]
        case .messages:
            return cursorItem.flatMap { store.session(id: $0.sessionID) }.map { [$0] } ?? []
        }
    }

    /// What an action from a row's menu applies to, as in the sidebar.
    func targets(forRow session: Session) -> [Session] {
        markedSessions.contains(session.id) ? rows.map(\.session).filter { markedSessions.contains($0.id) } : [session]
    }

    func isMoveOutable(_ session: Session) -> Bool {
        !setMembers(of: session.id).isEmpty
    }

    private func reload() {
        store.loadInbox(session: nil) { [weak self] items in self?.allItems = items }
        switch filter {
        case .queue:
            keepSelection()
        case .all:
            load(session: nil)
        case .session(let id):
            load(session: id)
        case .set(let members):
            store.loadInbox(session: nil) { [weak self] items in
                self?.history = items.filter { members.contains($0.sessionID) }
                self?.keepSelection()
            }
        }
    }

    private func load(session: String?) {
        store.loadInbox(session: session) { [weak self] items in
            self?.history = items
            self?.keepSelection()
        }
    }

    /// When the item under the cursor leaves the list, the cursor stays in place, on
    /// the one that took its place. A message that's gone is no longer open.
    private func keepSelection() {
        let list = items
        let speech = SpeechController.shared
        let cursorLeft = messageCursor != nil && !list.contains { $0.id == messageCursor }
        let leftByItself = cursorLeft && !leftByYou.contains(messageCursor ?? -1)
        // A message that goes by itself shouldn't start the next one reading.
        if filter == .queue, leftByItself, selectedID == messageCursor, let cursorSession {
            let wasReading = speech.speakingItem == messageCursor
            following = (cursorSession, wasReading || (showsCursor && speech.isEnabled && PopupSettings.shared.autoPlay))
        } else if cursorLeft {
            following = nil
        }
        let newcomers = list.filter { knownItems?.contains($0.id) == false }
        if filter == .queue { knownItems = Set(list.map(\.id)) }

        let replacement = following.flatMap { follow in list.first { $0.sessionID == follow.session } }
        if let replacement, let follow = following {
            following = nil
            if !typing && choice == nil || cursorLeft {
                moveToReplacement(replacement, speak: follow.speak)
                keepRest(list)
                return
            }
        }

        movingQuietly = leftByItself
        defer { movingQuietly = false }
        if let index = list.firstIndex(where: { $0.id == messageCursor }) {
            lastIndex = index
        } else {
            messageCursor = list.isEmpty ? nil : list[min(lastIndex, list.count - 1)].id
        }
        if selected == nil {
            selectedID = nil
            typing = false
        }
        showCursor()
        if let newest = newcomers.last { say("New message from \(sessionName(newest.sessionID)).") }
        keepRest(list)
    }

    /// A newer message from the session you were on takes over from the one that left.
    private func moveToReplacement(_ item: InboxItem, speak: Bool) {
        movingQuietly = true
        defer { movingQuietly = false }
        typing = false
        messageCursor = item.id
        lastIndex = items.firstIndex { $0.id == item.id } ?? 0
        selectedID = item.id
        shownAt = Date()
        if speak { SpeechController.shared.speak(item) }
        if showsCursor { markShownRead() } else if !item.read { setRead(item, true) }
        say("New message from \(sessionName(item.sessionID)). It replaces the one you were on.")
    }

    private func say(_ text: String) {
        notice = text
        noticeTimer?.invalidate()
        noticeTimer = Timer.scheduledTimer(withTimeInterval: 8, repeats: false) { [weak self] _ in self?.notice = nil }
    }

    /// What follows wherever the cursor ended up.
    private func keepRest(_ list: [InboxItem]) {
        showFirstIfAsked()
        markedMessages.formIntersection(list.map(\.id))
        if filter == .queue {
            if list.isEmpty && hadQueue { onQueueEmptied() }
            hadQueue = !list.isEmpty
        }
    }

    /// After opening something on the left, shows its first message, with the keyboard
    /// still on the list.
    private func showFirstIfAsked() {
        guard showsFirst else { return }
        showsFirst = false
        guard let first = items.first else { return }
        messageCursor = first.id
        lastIndex = 0
        selectedID = first.id
        shownAt = Date()
        if !first.read { setRead(first, true) }
    }

    /// Opens a message. As return does on a session, a second open goes to where you
    /// type.
    func open(_ item: InboxItem) {
        setPanel(.messages)
        messageCursor = item.id
        lastIndex = items.firstIndex { $0.id == item.id } ?? 0
        if item.id != selectedID { error = nil }
        selectedID = item.id
        if !item.read { setRead(item, true) }
        startAnswering()
    }

    // MARK: Actions

    func setRead(_ item: InboxItem, _ read: Bool) {
        store.setInboxRead(item.id, read) { [weak self] in self?.reload() }
    }

    func dismiss(_ item: InboxItem) {
        guard item.isOpen else { return }
        leftByYou.insert(item.id)
        store.dismissInbox(item.id) { [weak self] in self?.reload() }
    }

    func go(_ item: InboxItem) {
        guard store.session(id: item.sessionID)?.isArchived == false else { return }
        close()
        goToSession(item.sessionID)
    }

    func answer(_ item: InboxItem, option: Int) {
        guard item.id == selectedID, answerable(item) == nil, option <= item.options.count else { return }
        send(item, option: option, text: nil)
    }

    func sendReply(_ item: InboxItem) {
        let text = drafts[item.sessionID] ?? ""
        guard answerable(item) == nil, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        send(item, option: nil, text: text)
    }

    private func send(_ item: InboxItem, option: Int?, text: String?) {
        guard !sending else { return }
        leftByYou.insert(item.id)
        sending = true
        error = nil
        store.answerInbox(item.id, option: option, text: text) { [weak self] failure in
            guard let self else { return }
            self.sending = false
            self.error = failure
            if failure == nil {
                self.drafts[item.sessionID] = nil
                self.typing = false
            }
            self.reload()
        }
    }

    /// Why this item can't be answered from here, if it can't. The daemon checks too.
    func answerable(_ item: InboxItem) -> String? {
        guard item.isOpen else { return "This has been dealt with." }
        if item.multipart { return "This question has several parts. Answer it in the session." }
        guard let session = store.session(id: item.sessionID), !session.claudeSessionID.isEmpty, !session.isStopped,
            !session.isArchived
        else { return "Claude isn't running in this session." }
        if item.needsOption {
            return session.status == "waiting" ? nil : "This session isn't waiting for an answer."
        }
        return session.status == "done" || session.status == "error" ? nil : "Claude is busy. Your draft is kept."
    }

    // MARK: Keys

    /// Handles a key press, and reports whether it was used. The Inbox has the keyboard,
    /// so nothing reaches the terminal behind it. Every key comes from the keymap, and
    /// the app's and the lists' keys mean here what they mean elsewhere.
    func handle(_ event: NSEvent) -> Bool {
        let keys = Keymap.shared
        let combo = KeyCombo(event: event)
        if typing, let action = keys.action(for: combo, among: [.save, .leave]) {
            if action == .save {
                if let selected { sendReply(selected) }
            } else {
                typing = false
            }
            return true
        }
        switch keys.action(for: combo, among: [.closePane, .focusSidebar, .readAloud, .rename, .archive]) {
        case .closePane: close()
        case .focusSidebar: if !showsCursor { setPanel(panel == .sessions ? .messages : .sessions) }
        case .readAloud: toggleSpeech()
        case .rename: rename(sessionTargets)
        case .archive: archive(sessionTargets)
        default:
            // There's no pane to split or move to behind the Inbox.
            if keys.action(for: combo, among: KeyAction.paneActions) != nil { return true }
            if typing { return false }
            return handleList(combo, event)
        }
        return true
    }

    /// Keys for the lists and the message on show, while not writing a reply.
    private func handleList(_ combo: KeyCombo, _ event: NSEvent) -> Bool {
        let keys = Keymap.shared
        if handleChoice(combo, event) { return true }
        switch keys.action(for: combo, among: [.up, .down, .open, .back, .mark, .nextNeedingYou]) {
        case .up: moveCursor(-1)
        case .down: moveCursor(1)
        case .mark: toggleMark()
        case .open:
            // Holding return shouldn't run on to whatever comes next.
            if !event.isARepeat {
                if showsCursor { startAnswering() } else { openCursor() }
            }
        case .back:
            if panel == .sessions { setPanel(.messages) } else { close() }
        case .nextNeedingYou: nextUnread()
        default:
            guard panel == .messages else { return !combo.mods.contains(.command) }
            switch keys.action(for: combo, among: [.reply, .dismiss, .remove, .unread, .go]) {
            case .reply: replyIfPossible()
            case .dismiss, .remove: messageTargets.forEach(dismiss)
            case .unread:
                if let cursorItem {
                    let read = !cursorItem.read
                    messageTargets.forEach { setRead($0, read) }
                }
            case .go: if let cursorItem { go(cursorItem) }
            default:
                // Only the open message, so you've seen what you're answering.
                if combo.mods.isEmpty, let index = KeyName.digitCodes.firstIndex(of: combo.key),
                   let selected, selected.needsOption, hasSettled {
                    answer(selected, option: index + 1)
                }
                // The menus get the rest of ⌘, and nothing else goes behind the Inbox.
                return !combo.mods.contains(.command)
            }
        }
        return true
    }

    /// Goes to the next unread message after the cursor, round to the start, as tab
    /// goes to the next session that needs you in the sidebar. The Inbox opens it.
    private func nextUnread() {
        guard panel == .messages else { return }
        let list = items
        let start = list.firstIndex { $0.id == messageCursor } ?? -1
        for step in 1...max(1, list.count) where !list.isEmpty {
            let index = (start + step + list.count) % list.count
            let item = list[index]
            guard !item.read, item.id != messageCursor else { continue }
            following = nil
            messageCursor = item.id
            lastIndex = index
            if showsCursor { showCursor() } else { open(item) }
            return
        }
        NSSound.beep()
    }


    /// Not straight after a message appears: a second press meant for the one before
    /// would answer this one.
    private var hasSettled: Bool {
        Date().timeIntervalSince(shownAt) > 0.5
    }

    /// Goes to where you answer the message on show: its options, or the reply box.
    private func startAnswering() {
        guard let selected else { return }
        if selected.needsOption {
            guard !selected.options.isEmpty, !selected.multipart, answerable(selected) == nil,
                choice?.item != selected.id, !showsCursor || hasSettled
            else { return }
            choice = (selected.id, 0)
            shownAt = Date()
        } else {
            replyIfPossible()
        }
    }

    /// The option picked out with the keyboard, while this message is being answered.
    func highlightedOption(of item: InboxItem) -> Int? {
        guard let choice, choice.item == item.id, answerable(item) == nil else { return nil }
        return choice.index
    }

    /// While an option is picked out, the list keys move between the options, open
    /// chooses and back leaves. Reports whether it was used.
    private func handleChoice(_ combo: KeyCombo, _ event: NSEvent) -> Bool {
        guard let selected, let index = highlightedOption(of: selected) else { return false }
        var by = 0
        switch Keymap.shared.action(for: combo, among: [.up, .left, .down, .right, .open, .back]) {
        case .up, .left: by = -1
        case .down, .right: by = 1
        case .open:
            if !event.isARepeat, hasSettled { answer(selected, option: index + 1) }
            return true
        case .back:
            choice = nil
            return true
        default:
            return false
        }
        choice = (selected.id, max(0, min(selected.options.count - 1, index + by)))
        // You've looked at what's on offer, so return can choose straight away.
        shownAt = .distantPast
        return true
    }

    private func replyIfPossible() {
        guard let selected, !selected.needsOption, !selected.multipart, answerable(selected) == nil else { return }
        typing = true
    }
}
