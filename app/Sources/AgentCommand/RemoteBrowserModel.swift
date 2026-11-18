import AppKit
import Observation

/// The Remote Hosts browser: your known hosts and their state on the left, and
/// the chosen host's Remote sessions on the right, to open, add, or start one
/// on. Everything comes through the daemon (RemoteHostsClient).
@Observable final class RemoteBrowserModel {
    enum Panel { case hosts, sessions }

    @ObservationIgnored let client: RemoteHostsClient
    /// The known hosts, as the latest snapshot has them.
    @ObservationIgnored private let hostsSource: () -> [RemoteHost]
    private(set) var panel = Panel.hosts
    private(set) var hostName: String?
    /// The chosen host's sessions, as it last listed them.
    private(set) var sessions: [RemoteSession] = []
    private(set) var listed: String?
    private(set) var sessionCursor = 0
    private(set) var loading = false
    /// Why the host's sessions couldn't be listed, or an Open or Add failed.
    private(set) var error: String?
    /// The sessions being added, by their id on the host.
    private(set) var adding: Set<String> = []
    private(set) var retrying: Set<String> = []
    @ObservationIgnored private var monitor: Any?
    /// Whether a key is the browser's: not one typed in another window.
    @ObservationIgnored var handlesKey: (NSEvent) -> Bool = { _ in true }
    @ObservationIgnored private var generation = 0

    /// Closes the browser.
    @ObservationIgnored var close: () -> Void = {}
    /// Puts a session just added in the sidebar.
    @ObservationIgnored var adopt: (Session) -> Void = { _ in }
    /// Shows a session here, by its id here, and closes the browser.
    @ObservationIgnored var focus: (String) -> Void = { _ in }
    /// Opens ⌘⇧N with this host chosen.
    @ObservationIgnored var newSession: (String) -> Void = { _ in }
    @ObservationIgnored var copy: (String) -> Void = { text in
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    init(client: RemoteHostsClient = RemoteHostsClient(), hosts: @escaping () -> [RemoteHost], host: String? = nil) {
        self.client = client
        hostsSource = hosts
        hostName = host
    }

    var hosts: [RemoteHost] { hostsSource() }

    /// The chosen host: the first while none is.
    var host: RemoteHost? {
        let list = hosts
        return list.first { $0.name == hostName } ?? list.first
    }

    /// The session under the cursor, while the sessions have the keyboard.
    var cursorSession: RemoteSession? {
        panel == .sessions && sessions.indices.contains(sessionCursor) ? sessions[sessionCursor] : nil
    }

    func start() {
        load()
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self, self.handlesKey(event) else { return event }
            return self.handle(KeyCombo(event: event)) ? nil : event
        }
    }

    func stop() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    // MARK: Hosts

    func choose(host name: String) {
        guard name != host?.name || listed != name else { return }
        hostName = name
        sessions = []
        sessionCursor = 0
        error = nil
        load()
    }

    /// Lists the chosen host's sessions. quiet keeps what's shown while it does,
    /// for a refresh.
    func load(quiet: Bool = false) {
        guard let name = host?.name else { return }
        generation += 1
        let mine = generation
        if !quiet { loading = true }
        client.browse(name) { [weak self] result in
            guard let self, mine == self.generation else { return }
            self.loading = false
            self.listed = name
            switch result {
            case .success(let browse):
                self.sessions = browse.sessions
                self.error = nil
                self.sessionCursor = min(self.sessionCursor, max(0, browse.sessions.count - 1))
            case .failure(let failure):
                if !quiet { self.sessions = [] }
                self.error = errorText(failure)
            }
        }
    }

    func retry(_ host: RemoteHost) {
        retrying.insert(host.name)
        client.retry(host.name) { [weak self] result in
            guard let self else { return }
            self.retrying.remove(host.name)
            if case .failure(let failure) = result { self.error = errorText(failure) }
            if host.name == self.host?.name { self.load(quiet: true) }
        }
    }

    // MARK: Sessions

    /// Adds it, if it isn't yet, then shows it.
    func open(_ session: RemoteSession) {
        if session.added, !session.localID.isEmpty { return focus(session.localID) }
        add(session) { [weak self] local in self?.focus(local.id) }
    }

    func add(_ session: RemoteSession, then: ((Session) -> Void)? = nil) {
        guard let name = host?.name, !adding.contains(session.id) else { return }
        guard !session.added else { return }
        adding.insert(session.id)
        client.addSession(host: name, id: session.id) { [weak self] result in
            guard let self else { return }
            self.adding.remove(session.id)
            switch result {
            case .success(let local):
                if let i = self.sessions.firstIndex(where: { $0.id == session.id }) {
                    self.sessions[i].added = true
                    self.sessions[i].localID = local.id
                }
                self.adopt(local)
                then?(local)
            case .failure(let failure):
                self.error = errorText(failure)
            }
        }
    }

    // MARK: Keys

    /// The browser's keys, like the Inbox's: the arrows (or ⌃J ⌃K) move, left
    /// and right go between hosts and sessions, return opens, escape goes back.
    /// a adds, n starts a session on the host, r retries and c copies its fingerprint.
    func handle(_ combo: KeyCombo) -> Bool {
        let keys = Keymap.shared
        switch keys.action(for: combo, among: [.up, .down, .left, .right, .open, .back, .closePane]) {
        case .up: move(-1)
        case .down: move(1)
        case .left: panel = .hosts
        case .right: enterSessions()
        case .open:
            if panel == .hosts {
                enterSessions()
            } else if let session = cursorSession {
                open(session)
            }
        case .back:
            if panel == .sessions { panel = .hosts } else { close() }
        case .closePane: close()
        default:
            guard combo.mods.isEmpty else { return !combo.mods.contains(.command) }
            if combo == KeyCombo("a"), let session = cursorSession {
                add(session)
            } else if combo == KeyCombo("n"), let host {
                newSession(host.name)
            } else if combo == KeyCombo("r"), let host, host.canRetry {
                retry(host)
            } else if combo == KeyCombo("c"), let text = host?.message().copy, !text.isEmpty {
                copy(text)
            }
        }
        return true
    }

    private func enterSessions() {
        if !sessions.isEmpty { panel = .sessions }
    }

    private func move(_ step: Int) {
        switch panel {
        case .hosts:
            let list = hosts
            guard !list.isEmpty else { return }
            let index = list.firstIndex { $0.name == host?.name } ?? 0
            choose(host: list[max(0, min(list.count - 1, index + step))].name)
        case .sessions:
            guard !sessions.isEmpty else { return }
            sessionCursor = max(0, min(sessions.count - 1, sessionCursor + step))
        }
    }

    func setCursor(_ session: RemoteSession) {
        if let i = sessions.firstIndex(of: session) {
            sessionCursor = i
            panel = .sessions
        }
    }

    /// The keys, for the browser's top line.
    static let hint = "↑↓ move  ←→ hosts and sessions  return open  a add  n new session  r retry  c copy  esc close"
}
