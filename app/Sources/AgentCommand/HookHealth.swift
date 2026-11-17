import Foundation
import Observation

/// One agent's hook setup, as `acmd doctor --json` reports it.
struct AgentHookState: Decodable, Equatable {
    struct Problem: Decodable, Equatable {
        let severity: String
        let text: String
        let fix: String?
    }

    let agent: String
    let name: String
    let installed: Bool
    let problems: [Problem]

    enum CodingKeys: String, CodingKey { case agent, name, installed, problems }

    init(agent: String, name: String, installed: Bool, problems: [Problem]) {
        self.agent = agent
        self.name = name
        self.installed = installed
        self.problems = problems
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agent = try c.decode(String.self, forKey: .agent)
        name = try c.decode(String.self, forKey: .name)
        installed = try c.decode(Bool.self, forKey: .installed)
        problems = try c.decodeIfPresent([Problem].self, forKey: .problems) ?? []
    }
}

/// Whether each installed agent's hooks are set up, so the app can warn when
/// they aren't. An agent that isn't installed is never checked, so it never
/// warns. The check is `acmd doctor --json`, the same checks `acmd doctor`
/// makes, run when the app starts, every few minutes, and when it comes to
/// the front.
@Observable
final class HookHealth {
    private(set) var agents: [AgentHookState] = []
    @ObservationIgnored private var running = false
    @ObservationIgnored private var lastRun: Date?
    @ObservationIgnored private var timer: Timer?
    /// Runs acmd with arguments, then done(succeeded, output) (tests replace it).
    @ObservationIgnored var run: (_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) -> Void = { args, done in
        Acmd.run(args, done)
    }

    /// How often it checks again on its own.
    static let every: TimeInterval = 5 * 60

    func start() {
        check()
        timer = Timer.scheduledTimer(withTimeInterval: Self.every, repeats: true) { [weak self] _ in
            self?.check()
        }
    }

    /// Checks again, unless a check ran in the last few seconds or is running.
    func check(force: Bool = false) {
        if running { return }
        if !force, let lastRun, Date().timeIntervalSince(lastRun) < 30 { return }
        running = true
        run(["doctor", "--json"]) { [weak self] _, output in
            guard let self else { return }
            running = false
            lastRun = Date()
            // An acmd too old to know --json says nothing: keep what we had.
            if let states = Self.decode(output) { agents = states }
        }
    }

    /// Reads the report out of acmd's output, which may carry a line of
    /// stderr before it.
    static func decode(_ output: String) -> [AgentHookState]? {
        guard let start = output.firstIndex(of: "{"),
              let data = String(output[start...]).data(using: .utf8)
        else { return nil }
        struct Report: Decodable { let agents: [AgentHookState] }
        return (try? JSONDecoder().decode(Report.self, from: data))?.agents
    }

    /// The warnings for installed agents whose hooks need attention.
    func warnings(setUp: @escaping () -> Void) -> [AppWarning] {
        Self.warnings(for: agents, setUp: setUp)
    }

    static func warnings(for agents: [AgentHookState], setUp: @escaping () -> Void) -> [AppWarning] {
        agents.compactMap { a in
            guard a.installed, !a.problems.isEmpty else { return nil }
            let failing = a.problems.contains { $0.severity == "fail" }
            let lines = a.problems.map { problem in
                let text = problem.text.prefix(1).uppercased() + problem.text.dropFirst()
                return text.hasSuffix(".") ? text : text + "."
            }
            return AppWarning(
                id: "hooks-\(a.agent)",
                title: failing ? "\(a.name)'s hooks aren't set up" : "\(a.name)'s hooks need a look",
                detail: "Its sessions can't show live status, and the Inbox misses them. " + lines.joined(separator: " "),
                fix: "Set them up again. This is what `acmd init` does.",
                actionTitle: "Set Up", action: setUp)
        }
    }
}
