import AppKit
import Observation
import SwiftUI

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

    func start() {
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
    /// so nothing reaches the terminal behind it. Moving, marking, opening and leaving
    /// use the sidebar's keys.
    private func handle(_ event: NSEvent) -> Bool {
        let flags = event.modifierFlags.intersection([.command, .control, .option, .shift])
        let key = event.charactersIgnoringModifiers?.lowercased()
        if flags.contains(.command) {
            switch (event.keyCode, flags, key) {
            case (36, _, _):
                if typing, let selected { sendReply(selected) }
            case (_, [.command], "w"):
                close()
            case (_, [.command, .shift], "b"):
                if !showsCursor { setPanel(panel == .sessions ? .messages : .sessions) }
            case (_, [.command, .shift], "s"):
                toggleSpeech()
            case (_, [.command], "r"):
                rename(sessionTargets)
            case (51, [.command, .shift], _):
                archive(sessionTargets)
            default:
                return false
            }
            return true
        }
        if typing {
            if event.keyCode == 53 {
                typing = false
                return true
            }
            return false
        }
        if handleChoice(event, flags: flags) { return true }
        if let move = SidebarKey.from(keyCode: event.keyCode, characters: event.charactersIgnoringModifiers, flags: flags) {
            switch move {
            case .up: moveCursor(-1)
            case .down: moveCursor(1)
            case .toggleMark: toggleMark()
            case .open:
                // Holding return shouldn't run on to whatever comes next.
                if !event.isARepeat {
                    if showsCursor { startAnswering() } else { openCursor() }
                }
            case .leave:
                if panel == .sessions { setPanel(.messages) } else { close() }
            }
            return true
        }
        guard panel == .messages, flags.isEmpty, let key else { return true }
        switch key {
        case "r": replyIfPossible()
        case "d": messageTargets.forEach(dismiss)
        case "u":
            if let cursorItem {
                let read = !cursorItem.read
                messageTargets.forEach { setRead($0, read) }
            }
        case "g": if let cursorItem { go(cursorItem) }
        default:
            // Only the open message, so you've seen what you're answering.
            if let digit = Int(key), digit >= 1, let selected, selected.needsOption, hasSettled {
                answer(selected, option: digit)
            }
        }
        return true
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

    /// While an option is picked out, arrows and vim's keys move between them, return
    /// chooses and escape leaves. Key codes, as in the sidebar. Reports whether it was used.
    private func handleChoice(_ event: NSEvent, flags: NSEvent.ModifierFlags) -> Bool {
        guard let selected, let index = highlightedOption(of: selected), flags.isEmpty || flags == .control else {
            return false
        }
        var by = 0
        switch (event.keyCode, flags) {
        case (126, []), (123, []), (40, _), (4, _): by = -1
        case (125, []), (124, []), (38, _), (37, _): by = 1
        case (36, []), (76, []):
            if !event.isARepeat, hasSettled { answer(selected, option: index + 1) }
            return true
        case (53, []):
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

/// How to show an item's type and what happened to it.
private extension InboxItem {
    var kindIcon: String {
        switch kind {
        case "question": "questionmark.circle"
        case "permission": "hand.raised"
        case "error": "exclamationmark.triangle"
        default: "checkmark.circle"
        }
    }

    var stateIcon: String? {
        switch state {
        case "responded": "arrowshape.turn.up.left"
        case "dismissed": "archivebox"
        case "stale": "clock.arrow.circlepath"
        default: nil
        }
    }
}

/// The full-window Inbox: sessions on the left; on the right the messages, the
/// selected one and a place to reply. The dividers drag, and are remembered.
struct InboxView: View {
    @Bindable var model: InboxModel

    @AppStorage("inbox.sessionsWidth") private var sessionsWidth = 260.0
    @AppStorage("inbox.listWidth") private var listWidth = 360.0
    @AppStorage("inbox.messageShare") private var messageShare = 0.5
    @AppStorage("inbox.replyHeight") private var replyHeight = 140.0
    @AppStorage(InboxLayout.key) private var layout = InboxLayout.columns

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { window in
                HStack(spacing: 0) {
                    sessions.frame(width: sessionsWidth)
                    InboxDivider(vertical: true, value: $sessionsWidth, range: 180...max(180, window.size.width - 500))
                    if layout == .columns {
                        columns(width: window.size.width - sessionsWidth, height: window.size.height)
                    } else {
                        stacked(height: window.size.height)
                    }
                }
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            footer
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
    }

    /// The messages, then the message box beside them.
    private func columns(width: CGFloat, height: CGFloat) -> some View {
        HStack(spacing: 0) {
            messages.frame(width: min(listWidth, max(200, width - 260)))
            InboxDivider(vertical: true, value: $listWidth, range: 200...max(200, width - 260))
            messageBox(height: height)
        }
    }

    /// The messages, then the message box under them.
    private func stacked(height: CGFloat) -> some View {
        let share = Binding(
            get: { messageShare * height },
            set: { messageShare = height > 0 ? $0 / height : 0.5 })
        return VStack(spacing: 0) {
            messages.frame(maxWidth: .infinity, maxHeight: .infinity)
            InboxDivider(vertical: false, value: share, inverted: true, range: 200...max(200, height - 80))
            messageBox(height: min(share.wrappedValue, max(200, height - 80)))
                .frame(height: min(share.wrappedValue, max(200, height - 80)))
        }
    }

    /// The selected message, and a place to reply under it.
    private func messageBox(height: CGFloat) -> some View {
        VStack(spacing: 0) {
            reader.frame(maxWidth: .infinity, maxHeight: .infinity)
            InboxDivider(vertical: false, value: $replyHeight, inverted: true, range: 60...max(60, height - 80))
            reply.frame(height: min(replyHeight, max(60, height - 80)))
        }
    }

    private var footer: some View {
        Text("↑↓ or ⌃J ⌃K move   ↩ open   x mark   ⇧⌘B sessions   1-9 answer   r reply   d dismiss   u unread   g go   ⇧⌘S read aloud   esc back")
            .font(AppFont.ui(11))
            .foregroundStyle(theme.dim)
            .lineLimit(1)
            .padding(.horizontal, 16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: 22)
    }

    // MARK: Sessions

    private var sessions: some View {
        ScrollViewReader { scroll in
            ScrollView {
                LazyVStack(spacing: 0) {
                    filterRow("Queue", count: model.queueCount, filter: .queue)
                    filterRow("All", count: nil, filter: .all)
                    Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1).padding(.vertical, 4)
                    ForEach(model.rows, id: \.session.id) { row in
                        if row.link == .first {
                            let members = model.setMembers(of: row.session.id)
                            SetLabelRow(
                                isSelected: model.filter == .set(members),
                                unread: model.unreadCount(set: members),
                                isCursor: isCursor(.set(members))
                            )
                            .id(InboxFilter.set(members))
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { model.click(filter: .set(members)) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(.set(members)) })
                        }
                        sessionRow(row.session, link: row.link == .first ? .middle : row.link)
                            .id(InboxFilter.session(row.session.id))
                            .contentShape(Rectangle())
                            .simultaneousGesture(TapGesture().onEnded { model.click(filter: .session(row.session.id)) })
                            .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(.session(row.session.id)) })
                            .contextMenu {
                                if model.isMoveOutable(row.session) {
                                    Button("Move Out of Set") { model.moveOut(row.session) }
                                }
                                Button("Rename…") { model.rename(model.targets(forRow: row.session)) }
                                Button("Archive…") { model.archive(model.targets(forRow: row.session)) }
                            }
                    }
                }
            }
            .onChange(of: model.sessionCursor) { _, cursor in scroll.scrollTo(cursor) }
        }
    }

    private func isCursor(_ filter: InboxFilter) -> Bool {
        model.panel == .sessions && !model.typing && model.sessionCursor == filter
    }

    private func filterRow(_ title: String, count: Int?, filter: InboxFilter) -> some View {
        let isSelected = model.filter == filter
        return HStack {
            Text(title).fontWeight(isSelected ? .bold : .regular)
            Spacer()
            if let count, count > 0 { Text("\(count)").fontWeight(.bold) }
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .frame(height: 22)
        .background(isSelected ? theme.yellow : Color.clear)
        .overlay {
            if isCursor(filter) {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
        .id(filter)
        .contentShape(Rectangle())
        .simultaneousGesture(TapGesture().onEnded { model.click(filter: filter) })
        .simultaneousGesture(TapGesture(count: 2).onEnded { model.setFilter(filter) })
    }

    private func sessionRow(_ session: Session, link: SetLink) -> some View {
        let isSelected = model.filter == .session(session.id)
        let unread = model.unreadCount(session.id)
        let open = model.openItem(session.id)
        return VStack(alignment: .leading, spacing: 0) {
            SessionRow(
                session: session, link: link, isSelected: isSelected, isVisible: false, isClosed: false,
                isCursor: isCursor(.session(session.id)), isMarked: model.markedSessions.contains(session.id), unread: 0)
            if unread > 0 || open != nil {
                HStack(spacing: 6) {
                    // Keeps a set's line running past this row's icons.
                    if link == .middle {
                        SetLinkMark(link: .middle).stroke(isSelected ? theme.bg : theme.fg, lineWidth: 1).frame(width: 8)
                    } else if link == .last {
                        Color.clear.frame(width: 8)
                    }
                    HStack(spacing: 10) {
                        if unread > 0 {
                            Label("\(unread)", systemImage: "envelope.badge")
                                .help(unread == 1 ? "1 unread message" : "\(unread) unread messages")
                        }
                        if let open {
                            Image(systemName: open.kindIcon).help("Waiting: \(open.kindLabel)")
                        }
                    }
                    .padding(.leading, 14)
                    .padding(.bottom, 4)
                }
                .font(AppFont.ui(11))
                .foregroundStyle(isSelected ? theme.bg : theme.blue)
                .padding(.horizontal, 12)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(isSelected ? theme.yellow : Color.clear)
            }
        }
    }

    // MARK: Messages

    @ViewBuilder private var messages: some View {
        if model.items.isEmpty {
            Text(model.filter == .queue ? "Nothing to deal with." : "No messages yet.")
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { scroll in
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(model.items) { item in
                            messageRow(item)
                                .id(item.id)
                                .contentShape(Rectangle())
                                .simultaneousGesture(TapGesture().onEnded { model.click(item: item) })
                                .simultaneousGesture(TapGesture(count: 2).onEnded { model.open(item) })
                        }
                    }
                }
                .onChange(of: model.messageCursor) { _, id in
                    if let id { scroll.scrollTo(id) }
                }
            }
        }
    }

    @ViewBuilder private func messageRow(_ item: InboxItem) -> some View {
        let isSelected = item.id == model.selectedID
        let dim = isSelected ? theme.bg : theme.dim
        Group {
            if layout == .columns {
                // A narrow list, so the first line of the message gets its own line.
                HStack(alignment: .top, spacing: 8) {
                    rowMarks(item, isSelected: isSelected)
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 8) {
                            rowName(item)
                            Spacer(minLength: 8)
                            rowEnd(item, dim: dim)
                        }
                        Text(item.preview).font(AppFont.ui(11)).foregroundStyle(dim).lineLimit(1)
                    }
                }
                .padding(.vertical, 6)
            } else {
                HStack(spacing: 8) {
                    rowMarks(item, isSelected: isSelected)
                    rowName(item).frame(width: 130, alignment: .leading)
                    Text(item.preview).foregroundStyle(dim).lineLimit(1)
                    Spacer(minLength: 8)
                    rowEnd(item, dim: dim)
                }
                .frame(height: 24)
            }
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .opacity(item.isOpen || isSelected ? 1 : 0.6)
        .padding(.horizontal, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isSelected ? theme.yellow : Color.clear)
        .overlay {
            if model.panel == .messages && !model.typing && model.messageCursor == item.id {
                Rectangle().strokeBorder(isSelected ? theme.bg : theme.fg, lineWidth: 1)
            }
        }
    }

    private func rowMarks(_ item: InboxItem, isSelected: Bool) -> some View {
        HStack(spacing: 8) {
            if model.markedMessages.contains(item.id) {
                Text("x").fontWeight(.bold).foregroundStyle(isSelected ? theme.bg : theme.red)
            }
            Circle()
                .fill(item.read ? Color.clear : (isSelected ? theme.bg : theme.blue))
                .frame(width: 6, height: 6)
                .help(item.read ? "Read" : "Unread")
            Image(systemName: item.kindIcon)
                .foregroundStyle(isSelected ? theme.bg : kindColor(item))
                .frame(width: 16)
                .help(item.kindLabel)
        }
    }

    private func rowName(_ item: InboxItem) -> some View {
        Text(model.sessionName(item.sessionID))
            .fontWeight(item.read ? .regular : .bold)
            .lineLimit(1)
            .truncationMode(.middle)
    }

    private func rowEnd(_ item: InboxItem, dim: Color) -> some View {
        HStack(spacing: 8) {
            if let icon = item.stateIcon {
                Image(systemName: icon).foregroundStyle(dim).help(item.stateLabel)
            }
            Text(item.age).font(AppFont.ui(11)).foregroundStyle(dim)
        }
    }

    private func kindColor(_ item: InboxItem) -> Color {
        guard item.isOpen else { return theme.dim }
        switch item.kind {
        case "question", "permission": return theme.yellow
        case "error": return theme.red
        default: return theme.green
        }
    }

    // MARK: The selected message

    @ViewBuilder private var reader: some View {
        if let item = model.selected {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 8) {
                    Image(systemName: item.kindIcon).foregroundStyle(kindColor(item)).help(item.kindLabel)
                    Text(model.sessionName(item.sessionID)).fontWeight(.bold).lineLimit(1)
                    if let icon = item.stateIcon {
                        Image(systemName: icon).foregroundStyle(theme.dim).help(item.stateLabel)
                        Text(item.stateLabel).font(AppFont.ui(11)).foregroundStyle(theme.dim).lineLimit(1)
                    }
                    Spacer()
                    SpeakButton(item: item)
                    actionButton("Dismiss", enabled: item.isOpen) { model.dismiss(item) }
                    actionButton(item.read ? "Mark unread" : "Mark read", enabled: true) {
                        model.setRead(item, !item.read)
                    }
                    actionButton("Go to session", enabled: model.session(of: item)?.isArchived == false) {
                        model.go(item)
                    }
                }
                .padding(.horizontal, 16)
                .frame(height: 30)
                Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                InboxNotice(model: model)
                ScrollView {
                    Text(item.rendered)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(16)
                }
            }
        } else {
            Text(model.items.isEmpty ? "" : "Press return or double-click a message to open it.")
                .font(AppFont.ui(11))
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func actionButton(_ title: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(title, action: action)
            .buttonStyle(.plain)
            .font(AppFont.ui(11))
            .foregroundStyle(enabled ? theme.fg : theme.dim)
            .disabled(!enabled)
    }

    // MARK: Reply

    @ViewBuilder private var reply: some View {
        if let item = model.selected {
            InboxAnswerView(model: model, item: item)
        } else {
            Color.clear
        }
    }
}

/// The popup: the queue on the left, and the message on the right with its session,
/// next and previous, and a place to reply.
struct PopupInboxView: View {
    @Bindable var model: InboxModel

    @AppStorage("popup.listWidth") private var listWidth = 260.0
    @AppStorage("popup.replyHeight") private var replyHeight = 130.0

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { window in
                HStack(spacing: 0) {
                    queue.frame(width: min(listWidth, max(160, window.size.width - 320)))
                    InboxDivider(vertical: true, value: $listWidth, range: 160...max(160, window.size.width - 320))
                    VStack(spacing: 0) {
                        header
                        Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                        InboxNotice(model: model)
                        message.frame(maxWidth: .infinity, maxHeight: .infinity)
                        InboxDivider(
                            vertical: false, value: $replyHeight, inverted: true,
                            range: 60...max(60, window.size.height - 120))
                        answer.frame(height: min(replyHeight, max(60, window.size.height - 120)))
                    }
                }
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            Text("↑↓ or ⌃J ⌃K next and previous   ↩ reply or choose   1-9 answer   d dismiss   g go to session   ⇧⌘S read aloud   esc close")
                .font(AppFont.ui(11))
                .foregroundStyle(theme.dim)
                .lineLimit(1)
                .padding(.horizontal, 16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .frame(height: 22)
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
        .overlay { Rectangle().strokeBorder(Color(nsColor: theme.line), lineWidth: 1) }
    }

    @ViewBuilder private var queue: some View {
        if model.items.isEmpty {
            Text("Nothing to deal with.")
                .foregroundStyle(theme.dim)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { scroll in
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(model.items) { item in
                            row(item)
                                .id(item.id)
                                .contentShape(Rectangle())
                                .onTapGesture { model.click(item: item) }
                        }
                    }
                }
                .onChange(of: model.selectedID) { _, id in
                    if let id { scroll.scrollTo(id) }
                }
            }
        }
    }

    private func row(_ item: InboxItem) -> some View {
        let isSelected = item.id == model.selectedID
        return HStack(spacing: 8) {
            Circle()
                .fill(item.read ? Color.clear : (isSelected ? theme.bg : theme.blue))
                .frame(width: 6, height: 6)
                .help(item.read ? "Read" : "Unread")
            Image(systemName: item.kindIcon)
                .foregroundStyle(isSelected ? theme.bg : kindColor(item))
                .frame(width: 16)
                .help(item.kindLabel)
            Text(model.sessionName(item.sessionID))
                .fontWeight(item.read ? .regular : .bold)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 8)
            Text(item.age).font(AppFont.ui(11)).foregroundStyle(isSelected ? theme.bg : theme.dim)
        }
        .foregroundStyle(isSelected ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .frame(height: 26)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(isSelected ? theme.yellow : Color.clear)
    }

    private var header: some View {
        HStack(spacing: 10) {
            if let item = model.selected {
                Image(systemName: item.kindIcon).foregroundStyle(kindColor(item)).help(item.kindLabel)
                Text(model.sessionName(item.sessionID)).fontWeight(.bold).lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            if let position = model.position {
                iconButton("chevron.up", help: "Previous (↑)", enabled: position.index > 1) { model.step(-1) }
                Text("\(position.index) of \(position.count)").font(AppFont.ui(11)).foregroundStyle(theme.dim)
                iconButton("chevron.down", help: "Next (↓)", enabled: position.index < position.count) { model.step(1) }
            }
            if let item = model.selected {
                SpeakButton(item: item)
                textButton("Dismiss") { model.dismiss(item) }
                textButton("Go to session") { model.go(item) }
            }
            iconButton("xmark", help: "Close (esc)", enabled: true) { model.close() }
        }
        .padding(.horizontal, 16)
        .frame(height: 34)
    }

    private func textButton(_ title: String, action: @escaping () -> Void) -> some View {
        Button(title, action: action).buttonStyle(.plain).font(AppFont.ui(11))
    }

    private func iconButton(_ icon: String, help: String, enabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon).foregroundStyle(enabled ? theme.fg : theme.dim)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .help(help)
    }

    @ViewBuilder private var message: some View {
        if let item = model.selected {
            ScrollView {
                Text(item.rendered)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(16)
            }
        } else {
            Color.clear
        }
    }

    @ViewBuilder private var answer: some View {
        if let item = model.selected {
            InboxAnswerView(model: model, item: item)
        } else {
            Color.clear
        }
    }

    private func kindColor(_ item: InboxItem) -> Color {
        switch item.kind {
        case "question", "permission": return theme.yellow
        case "error": return theme.red
        default: return theme.green
        }
    }
}

/// A divider you can drag to resize what's beside it.
private struct InboxDivider: View {
    let vertical: Bool
    @Binding var value: Double
    /// Dragging toward the start makes it bigger: what it resizes comes after it.
    var inverted = false
    let range: ClosedRange<Double>

    @State private var start: Double?

    private var theme: AppTheme { .shared }

    var body: some View {
        Rectangle()
            .fill(Color(nsColor: theme.line))
            .frame(width: vertical ? 1 : nil, height: vertical ? nil : 1)
            .overlay {
                // A wider area to grab than the line you see.
                Color.clear
                    .frame(width: vertical ? 9 : nil, height: vertical ? nil : 9)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside {
                            (vertical ? NSCursor.resizeLeftRight : NSCursor.resizeUpDown).push()
                        } else {
                            NSCursor.pop()
                        }
                    }
                    .gesture(
                        DragGesture(coordinateSpace: .global)
                            .onChanged { drag in
                                let from = start ?? value
                                start = from
                                let moved = Double(vertical ? drag.translation.width : drag.translation.height)
                                value = min(range.upperBound, max(range.lowerBound, from + (inverted ? -moved : moved)))
                            }
                            .onEnded { _ in start = nil })
            }
            .zIndex(1)
    }
}

/// Says that a new message has come in.
private struct InboxNotice: View {
    let model: InboxModel

    private var theme: AppTheme { .shared }

    var body: some View {
        if let notice = model.notice {
            Label(notice, systemImage: "bell.badge")
                .font(AppFont.ui(11))
                .foregroundStyle(theme.yellow)
                .lineLimit(1)
                .padding(.horizontal, 16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .frame(height: 22)
        }
    }
}

/// Reads a message aloud, or stops.
struct SpeakButton: View {
    let item: InboxItem

    private var theme: AppTheme { .shared }

    var body: some View {
        let speech = SpeechController.shared
        let speaking = speech.speakingItem == item.id
        if speech.isEnabled {
            Button {
                speech.toggle(item)
            } label: {
                Image(systemName: speaking ? "stop.fill" : "speaker.wave.2").foregroundStyle(theme.fg)
            }
            .buttonStyle(.plain)
            .help(speaking ? "Stop reading (⇧⌘S)" : "Read aloud (⇧⌘S)")
        }
    }
}

/// How to answer an item: its options, or a reply box.
private struct InboxAnswerView: View {
    @Bindable var model: InboxModel
    let item: InboxItem

    @FocusState private var boxFocused: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        let blocked = model.answerable(item)
        VStack(alignment: .leading, spacing: 8) {
            if let error = model.error ?? SpeechController.shared.failure {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(theme.red)
            }
            if item.isOpen && !item.multipart {
                if item.needsOption {
                    options(enabled: blocked == nil)
                    Spacer(minLength: 0)
                } else {
                    replyBox(enabled: blocked == nil)
                }
            } else {
                Spacer(minLength: 0)
            }
            if let blocked {
                Text(blocked).font(AppFont.ui(11)).foregroundStyle(theme.dim).lineLimit(1)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private func options(enabled: Bool) -> some View {
        let highlighted = model.highlightedOption(of: item)
        return VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 12) {
                ForEach(Array(item.options.enumerated()), id: \.offset) { index, option in
                    Button("\(index + 1). \(option)") { model.answer(item, option: index + 1) }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 10)
                        .frame(height: 24)
                        .overlay(
                            Rectangle().strokeBorder(
                                index == highlighted ? theme.yellow : Color(nsColor: theme.line), lineWidth: 1))
                        .foregroundStyle(enabled ? theme.fg : theme.dim)
                        .disabled(!enabled || model.sending)
                }
            }
            if highlighted != nil {
                Text("←→ or h l move   ↩ choose   esc leave").font(AppFont.ui(11)).foregroundStyle(theme.dim)
            }
        }
    }

    private func replyBox(enabled: Bool) -> some View {
        let draft = Binding(
            get: { model.drafts[item.sessionID] ?? "" },
            set: { model.drafts[item.sessionID] = $0 })
        return VStack(alignment: .trailing, spacing: 4) {
            TextEditor(text: draft)
                .font(AppFont.ui(13))
                .scrollContentBackground(.hidden)
                .disabled(!enabled)
                .focused($boxFocused)
                .overlay(
                    Rectangle().strokeBorder(
                        boxFocused ? theme.yellow : Color(nsColor: theme.line), lineWidth: 1))
                .onChange(of: model.typing) { _, typing in boxFocused = typing }
                .onChange(of: boxFocused) { _, focused in model.typing = focused }
                .onAppear { boxFocused = model.typing }
            Text("⌘↩ send   esc leave").font(AppFont.ui(11)).foregroundStyle(theme.dim)
        }
    }
}
