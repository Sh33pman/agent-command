import Foundation
import Observation

/// What a session runs in: one of the daemon's harnesses (a kind, claude or
/// antigravity, with its own name and settings: there can be several of a
/// kind), or a terminal, which isn't a harness. Equal when they're the same
/// harness. The app draws the icons it knows; any other is drawn generically.
nonisolated struct Harness: Hashable, Identifiable {
    /// Its id; "" for a terminal, and for one known only by its kind.
    let harnessID: String
    /// claude, antigravity or terminal (or one the app doesn't know).
    let kind: String
    /// Its own name: "Claude", "Work". A terminal's is "Terminal".
    var name: String
    /// What its kind is called: "Claude", "Antigravity".
    var kindTitle: String
    var icon: String
    /// Its program is found, so it can start sessions.
    var installed = true
    var order = 0
    /// Its program ("" is its kind's own), extra arguments, and, for Claude,
    /// its folder as set ("" is Claude's standard folder) and in full.
    var command = ""
    var args: [String] = []
    var configDir = ""
    var folder = ""

    init(id: String = "", kind: String, name: String? = nil, kindTitle: String? = nil, icon: String? = nil) {
        let title = kindTitle ?? Harness.title(of: kind)
        (harnessID, self.kind, self.name, self.kindTitle, self.icon) = (id, kind, name ?? title, title, icon ?? kind)
    }

    var id: String { harnessID.isEmpty ? "kind:\(kind)" : harnessID }
    static func == (a: Harness, b: Harness) -> Bool { a.id == b.id }
    func hash(into h: inout Hasher) { h.combine(id) }

    /// What to call it: its name.
    var title: String { name }
    /// Keeps a conversation, runs scheduled jobs and can be resumed.
    var isAgent: Bool { kind != "terminal" }
    var isClaude: Bool { kind == "claude" }
    var isAntigravity: Bool { kind == "antigravity" }

    static func title(of kind: String) -> String {
        switch kind {
        case "claude": "Claude"
        case "antigravity": "Antigravity"
        case "terminal": "Terminal"
        case "": "Agent"
        default: kind.capitalized
        }
    }

    static let claude = Harness(kind: "claude")
    static let antigravity = Harness(kind: "antigravity")
    static let terminal = Harness(kind: "terminal")

    /// The ones the app knows by kind, until the daemon lists its harnesses.
    static let known: [Harness] = [.claude, .antigravity]

    /// What a new session can run in here: the harnesses, then a terminal.
    @MainActor static var allCases: [Harness] { HarnessList.shared.harnesses + [.terminal] }

    /// The harnesses: what keeps a conversation, runs scheduled jobs and can be resumed.
    @MainActor static var agents: [Harness] { HarnessList.shared.harnesses }

    /// The kind's own product name, as in "Claude Code settings".
    var productName: String { isClaude ? "Claude Code" : kindTitle }

    /// What its conversation is called in an id's label: "Claude session", "Antigravity conversation".
    var conversationTitle: String { isClaude ? "Claude session" : "\(kindTitle) conversation" }

    /// The folder panel's button.
    var panelPrompt: String { isAgent ? "Start Session" : "Open Terminal" }

    /// What a new-session request says it runs in (internal/ipc.NewRequest).
    var request: (id: String, kind: String) { (harnessID, kind) }

    /// The harness with this id, or nil.
    @MainActor init?(id: String) {
        guard !id.isEmpty, let h = HarnessList.shared.harnesses.first(where: { $0.harnessID == id }) else { return nil }
        self = h
    }

    /// What a session (or a job, or a conversation) runs in: its harness, by
    /// id; one that was removed, or none, its kind (shown so).
    @MainActor static func resolve(id: String, kind: String) -> Harness {
        if let h = Harness(id: id) { return h }
        if kind == "terminal" { return .terminal }
        var h = Harness(kind: kind.isEmpty ? "claude" : kind)
        if !id.isEmpty { h.name = "\(h.kindTitle) (removed harness)" }
        return h
    }

    /// The agent a session's `agent` names: one the app knows, or a generic one.
    static func agent(_ name: String) -> Harness {
        Harness(kind: name, name: Harness.title(of: name))
    }
}

extension Harness: Decodable {
    enum CodingKeys: String, CodingKey {
        case id, kind, name, title, icon, installed, order, command, args, folder
        case configDir = "config_dir"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "claude"
        self.init(id: try c.decodeIfPresent(String.self, forKey: .id) ?? "", kind: kind,
                  name: try c.decodeIfPresent(String.self, forKey: .name),
                  kindTitle: try c.decodeIfPresent(String.self, forKey: .title),
                  icon: try c.decodeIfPresent(String.self, forKey: .icon))
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? true
        order = try c.decodeIfPresent(Int.self, forKey: .order) ?? 0
        command = try c.decodeIfPresent(String.self, forKey: .command) ?? ""
        args = try c.decodeIfPresent([String].self, forKey: .args) ?? []
        configDir = try c.decodeIfPresent(String.self, forKey: .configDir) ?? ""
        folder = try c.decodeIfPresent(String.self, forKey: .folder) ?? ""
    }
}

/// The harnesses, from the daemon's harnesses op and the snapshot, and the
/// default one. Adds, renames, changes and removes them through its ops.
@Observable final class HarnessList {
    static let shared = HarnessList()

    private(set) var harnesses: [Harness] = Harness.known
    /// The harness a plain new session runs in; nil when none is installed.
    private(set) var defaultID: String?

    @ObservationIgnored var calls: DaemonCalls = DaemonSocketCalls()

    nonisolated struct Answer: Decodable {
        var harnesses: [Harness]
        var `default`: String?
    }

    /// The default harness, if there is one.
    var defaultHarness: Harness? { defaultID.flatMap { id in harnesses.first { $0.harnessID == id } } }

    /// Asks the daemon.
    func load(done: @escaping () -> Void = {}) {
        calls.call("harnesses", Data("{}".utf8)) { [weak self] result in
            if case .success(let data) = result, let answer = try? JSONDecoder().decode(Answer.self, from: data) {
                self?.set(answer.harnesses, default: answer.default)
            }
            done()
        }
    }

    /// The harnesses as the snapshot lists them. The default is the setting's,
    /// when it names an installed one, else the first installed.
    func set(_ list: [Harness], default chosen: String? = nil) {
        if list != harnesses || zip(list, harnesses).contains(where: { $0.name != $1.name || $0.installed != $1.installed
            || $0.command != $1.command || $0.args != $1.args || $0.configDir != $1.configDir || $0.folder != $1.folder }) {
            harnesses = list
        }
        let setting = chosen ?? HostSettings.shared.string("default_harness")
        let def = list.first { $0.harnessID == setting && $0.installed } ?? list.first { $0.installed }
        if def?.harnessID != defaultID { defaultID = def?.harnessID }
    }

    /// A harness to save (internal/ipc.HarnessSave): a field left nil stays as it is.
    nonisolated struct Save: Encodable, Equatable {
        var id: String? = nil
        var kind: String? = nil
        var name: String? = nil
        var command: String? = nil
        var args: [String]? = nil
        var config_dir: String? = nil
    }

    /// Adds a harness, then lists them again; done gets the new one, or why not.
    func add(_ save: Save, done: @escaping (Result<Harness, Error>) -> Void) { change("harness.add", save, done) }

    /// Changes a harness (harness.update).
    func update(_ save: Save, done: @escaping (Result<Harness, Error>) -> Void) { change("harness.update", save, done) }

    /// Removes a harness: refused while a scheduled job runs in it.
    func remove(_ harness: Harness, done: @escaping (String?) -> Void) {
        let body = (try? JSONEncoder().encode(["id": harness.harnessID])) ?? Data()
        calls.call("harness.remove", body) { [weak self] result in
            switch result {
            case .success: self?.load { done(nil) }
            case .failure(let error): done(error.localizedDescription)
            }
        }
    }

    private func change(_ op: String, _ save: Save, _ done: @escaping (Result<Harness, Error>) -> Void) {
        let body = (try? JSONEncoder().encode(save)) ?? Data()
        calls.call(op, body) { [weak self] result in
            switch result {
            case .success(let data):
                guard let h = try? JSONDecoder().decode(Harness.self, from: data) else {
                    done(.failure(DaemonError(message: "the daemon's answer didn't make sense")))
                    return
                }
                self?.load { done(.success(h)) }
            case .failure(let error): done(.failure(error))
            }
        }
    }
}

/// The new-session picker's list: what's typed narrows it, the arrows move along it,
/// and a number picks that row straight away.
struct HarnessChoice {
    let harnesses: [Harness]
    private(set) var search = ""
    private(set) var selected = 0

    init(_ harnesses: [Harness] = Harness.allCases) {
        self.harnesses = harnesses
    }

    var matches: [Harness] {
        let query = search.trimmingCharacters(in: .whitespaces)
        return query.isEmpty ? harnesses : harnesses.filter { $0.title.localizedCaseInsensitiveContains(query) }
    }

    /// The row's number, as shown and typed.
    func number(of harness: Harness) -> Int { (harnesses.firstIndex(of: harness) ?? 0) + 1 }

    /// What return picks.
    var chosen: Harness? {
        let list = matches
        return list.indices.contains(selected) ? list[selected] : nil
    }

    mutating func move(_ delta: Int) {
        let count = matches.count
        if count > 0 { selected = (selected + delta + count) % count }
    }

    mutating func select(_ harness: Harness) {
        if let index = matches.firstIndex(of: harness) { selected = index }
    }

    /// Takes what's in the search line. A row's number on its own picks that row.
    mutating func setSearch(_ text: String) -> Harness? {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        if let n = Int(trimmed), harnesses.indices.contains(n - 1) { return harnesses[n - 1] }
        search = text
        selected = 0
        return nil
    }
}

/// A conversation to resume: its id, the agent it's from, and whether that agent
/// says it's still live.
nonisolated struct ResumeTarget: Equatable {
    let id: String
    let harness: Harness
    var isRunning = false
}

/// The resume picker's list: earlier conversations, narrowed by what's typed. An id
/// typed with nothing listed for it gets a row for each agent, as an id alone
/// doesn't say whose it is.
struct ResumeChoice {
    /// One row of the list.
    enum Row: Identifiable, Equatable {
        case conversation(Conversation)
        /// Look the typed id up as this agent's.
        case typed(String, Harness)

        var id: String {
            switch self {
            case .conversation(let c): "\(c.harness.id):\(c.id)"
            case .typed(let id, let harness): "typed:\(harness.id):\(id)"
            }
        }

        var target: ResumeTarget {
            switch self {
            case .conversation(let c): ResumeTarget(id: c.id, harness: c.harness, isRunning: c.isRunning)
            case .typed(let id, let harness): ResumeTarget(id: id, harness: harness)
            }
        }
    }

    var conversations: [Conversation] = []
    private(set) var search = ""
    private(set) var selected = 0

    init(_ conversations: [Conversation] = []) {
        self.conversations = conversations
    }

    var matches: [Conversation] { conversations.filter { $0.matches(search) } }

    /// What search holds, if it could be an id with nothing listed for it.
    var typedID: String? {
        let text = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let isHex = !text.isEmpty && text.allSatisfy { $0.isHexDigit || $0 == "-" }
        return matches.isEmpty && text.count >= 8 && isHex ? text : nil
    }

    var rows: [Row] {
        if let id = typedID { return Harness.agents.map { .typed(id, $0) } }
        return matches.map { .conversation($0) }
    }

    /// What return picks.
    var chosen: Row? {
        let list = rows
        return list.indices.contains(selected) ? list[selected] : nil
    }

    mutating func setSearch(_ text: String) {
        search = text
        selected = 0
    }

    mutating func move(_ delta: Int) {
        let count = rows.count
        if count > 0 { selected = (selected + delta + count) % count }
    }

    mutating func select(_ row: Row) {
        if let index = rows.firstIndex(of: row) { selected = index }
    }
}
