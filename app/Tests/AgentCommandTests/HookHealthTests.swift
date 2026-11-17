import Foundation
import Testing
@testable import AgentCommand

struct HookHealthTests {
    let report = """
    acmd: a warning on stderr
    {
      "agents": [
        {"agent": "claude", "name": "Claude Code", "installed": true},
        {"agent": "antigravity", "name": "Antigravity", "installed": true, "problems": [
          {"severity": "warn", "text": "no acmd hooks in ~/.gemini/config/hooks.json", "fix": "acmd doctor --fix"}
        ]},
        {"agent": "other", "name": "Other", "installed": false, "problems": [
          {"severity": "fail", "text": "never shown"}
        ]}
      ]
    }
    """

    @Test func readsTheReportPastStderr() throws {
        let agents = try #require(HookHealth.decode(report))
        #expect(agents.map(\.agent) == ["claude", "antigravity", "other"])
        #expect(agents[0].problems.isEmpty)
        #expect(agents[1].problems.first?.fix == "acmd doctor --fix")
        #expect(HookHealth.decode("unknown flag: --json") == nil)
    }

    @Test func warnsOnlyForInstalledAgentsWithProblems() throws {
        let agents = try #require(HookHealth.decode(report))
        var setUp = 0
        let warnings = HookHealth.warnings(for: agents) { setUp += 1 }
        #expect(warnings.map(\.id) == ["hooks-antigravity"])
        let w = try #require(warnings.first)
        #expect(w.title == "Antigravity's hooks need a look")
        #expect(w.detail.hasSuffix("No acmd hooks in ~/.gemini/config/hooks.json."))
        #expect(w.actionTitle == "Set Up")
        w.action()
        #expect(setUp == 1)
    }

    @Test func aFailureSaysTheHooksArentSetUp() {
        let agents = [AgentHookState(agent: "claude", name: "Claude Code", installed: true,
                                     problems: [.init(severity: "fail", text: "no acmd hooks", fix: nil)])]
        #expect(HookHealth.warnings(for: agents) {}.first?.title == "Claude Code's hooks aren't set up")
    }

    @MainActor @Test func checksAndKeepsTheLastAnswer() {
        let health = HookHealth()
        var calls: [[String]] = []
        var answer = report
        health.run = { args, done in
            calls.append(args)
            done(true, answer)
        }
        health.check()
        #expect(calls == [["doctor", "--json"]])
        #expect(health.agents.count == 3)
        #expect(health.warnings {}.count == 1)

        // A check straight after is skipped, unless forced.
        health.check()
        #expect(calls.count == 1)
        answer = #"{"agents": [{"agent": "antigravity", "name": "Antigravity", "installed": true}]}"#
        health.check(force: true)
        #expect(calls.count == 2)
        #expect(health.warnings {}.isEmpty)

        // An acmd that doesn't know --json keeps the last answer.
        answer = "Error: unknown flag: --json"
        health.check(force: true)
        #expect(health.agents.count == 1)
    }
}
