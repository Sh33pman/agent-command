import Foundation
import Observation

/// What a session runs, as the daemon lists it (the harnesses op): its name to the
/// daemon, what to call it, and its icon. The app draws the icons it knows; any
/// other is drawn generically, so a new agent needs no new app.
struct Harness: Hashable, Identifiable {
    let rawValue: String
    var title: String
    /// The icon key: "claude", "antigravity", "terminal", or one the app doesn't know.
    var icon: String
    /// Keeps a conversation, runs scheduled jobs and can be resumed.
    var isAgent: Bool

    init(name: String, title: String, icon: String, agent: Bool) {
        (rawValue, self.title, self.icon, isAgent) = (name, title, icon, agent)
    }

    static let claude = Harness(name: "claude", title: "Claude", icon: "claude", agent: true)
    static let antigravity = Harness(name: "antigravity", title: "Antigravity", icon: "antigravity", agent: true)
    static let terminal = Harness(name: "terminal", title: "Terminal", icon: "terminal", agent: false)

    /// The ones the app knows, until the daemon says what's installed.
    static let known: [Harness] = [.claude, .antigravity, .terminal]

    /// Everything new sessions can run here, from the daemon.
    static var allCases: [Harness] { HarnessList.shared.harnesses }

    /// The agents: what keeps a conversation, runs scheduled jobs and can be resumed.
    static var agents: [Harness] { allCases.filter(\.isAgent) }

    var id: String { rawValue }

    /// The agent's own product name, as in "Claude Code settings".
    var productName: String { self == .claude ? "Claude Code" : title }

    /// What its conversation is called in an id's label: "Claude session", "Antigravity conversation".
    var conversationTitle: String { self == .claude ? "Claude session" : "\(title) conversation" }

    /// The folder panel's button.
    var panelPrompt: String { isAgent ? "Start Session" : "Open Terminal" }

    /// The harness named so: one the daemon listed, one the app knows, or nil.
    init?(rawValue: String) {
        guard let h = HarnessList.shared.harnesses.first(where: { $0.rawValue == rawValue })
            ?? Harness.known.first(where: { $0.rawValue == rawValue })
        else { return nil }
        self = h
    }

    /// A session's harness. Older sessions have none, and are Claude's.
    init?(session name: String) {
        self.init(rawValue: name.isEmpty ? Harness.claude.rawValue : name)
    }

    /// The agent a session's `agent` names: one the app knows, or a generic one.
    static func agent(_ name: String) -> Harness {
        Harness(rawValue: name) ?? Harness(name: name, title: name.isEmpty ? "Agent" : name.capitalized, icon: name, agent: true)
    }
}

extension Harness: Decodable {
    enum CodingKeys: String, CodingKey { case name, title, icon, agent }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let name = try c.decode(String.self, forKey: .name)
        self.init(name: name, title: try c.decodeIfPresent(String.self, forKey: .title) ?? name.capitalized,
                  icon: try c.decodeIfPresent(String.self, forKey: .icon) ?? name,
                  agent: try c.decodeIfPresent(Bool.self, forKey: .agent) ?? false)
    }
}

/// What new sessions can run here, from the daemon's harnesses op.
@Observable final class HarnessList {
    static let shared = HarnessList()

    private(set) var harnesses: [Harness] = Harness.known

    @ObservationIgnored var calls: DaemonCalls = DaemonSocketCalls()

    struct Answer: Decodable {
        var harnesses: [Harness]
        var `default`: String?
    }

    /// Asks the daemon. An answer with nothing in it keeps what was there.
    func load(done: @escaping () -> Void = {}) {
        calls.call("harnesses", Data("{}".utf8)) { [weak self] result in
            if case .success(let data) = result, let answer = try? JSONDecoder().decode(Answer.self, from: data), !answer.harnesses.isEmpty {
                self?.harnesses = answer.harnesses
            }
            done()
        }
    }

    func set(_ list: [Harness]) { harnesses = list.isEmpty ? Harness.known : list }
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
struct ResumeTarget: Equatable {
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
            case .conversation(let c): "\(c.harnessKind.rawValue):\(c.id)"
            case .typed(let id, let harness): "typed:\(harness.rawValue):\(id)"
            }
        }

        var target: ResumeTarget {
            switch self {
            case .conversation(let c): ResumeTarget(id: c.id, harness: c.harnessKind, isRunning: c.isRunning)
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
