import Foundation

/// What a session runs. The raw value is its name to the daemon.
enum Harness: String, CaseIterable, Identifiable {
    case claude
    case antigravity
    case terminal

    /// The agents: what keeps a conversation, runs scheduled jobs and can be resumed.
    static let agents: [Harness] = [.claude, .antigravity]

    var id: String { rawValue }

    var title: String {
        switch self {
        case .claude: "Claude"
        case .antigravity: "Antigravity"
        case .terminal: "Terminal"
        }
    }

    /// The agent's own product name, as in "Claude Code settings".
    var productName: String { self == .claude ? "Claude Code" : title }

    /// What its conversation is called in an id's label: "Claude session", "Antigravity conversation".
    var conversationTitle: String { self == .claude ? "Claude session" : "\(title) conversation" }

    /// The folder panel's button.
    var panelPrompt: String { self == .terminal ? "Open Terminal" : "Start Session" }

    /// A session's harness. Older sessions have none, and are Claude's.
    init?(session name: String) {
        self.init(rawValue: name.isEmpty ? Harness.claude.rawValue : name)
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
