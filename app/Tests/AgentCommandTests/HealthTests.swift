import Foundation
import Testing

@testable import AgentCommand

@Test func theSnapshotSaysWhatTheHostCanDoAndWhatsNew() throws {
    let json = #"""
        {"dismissed": ["wake"], "features": {"host": {"os": "darwin", "harness_kinds": ["claude"], "wake": true, "hosting": true},
         "device": {"speech": true, "editor": false}},
         "update": {"current": "0.64.0", "latest": "0.65.0", "url": "https://example.com/r"},
         "health": [{"id": "remote.hosting_off", "title": "Hosting is off", "detail": "d",
                     "fix": {"op": "remote.hosting", "data": {"enabled": true}, "title": "Turn On Hosting", "step": "s"}}]}
        """#
    let s = try JSONDecoder().decode(Snapshot.self, from: Data(json.utf8))
    #expect(s.dismissed == ["wake"])
    #expect(s.features == HostFeatures(host: .init(os: "darwin", harnessKinds: ["claude"], wake: true, hosting: true), device: .init(speech: true)))
    #expect(s.update?.latest == "0.65.0" && s.update?.releaseURL?.absoluteString == "https://example.com/r")
    #expect(s.health?.first?.fix?.data == ["enabled": .bool(true)] && s.health?.first?.fix?.title == "Turn On Hosting")
    #expect(UpdateState(current: "1").releaseURL == nil)
}

@Test func healthEntriesBecomeWarnings() {
    var ran: [HealthItem.Fix] = []
    let health = [
        HealthItem(id: "setup.offer", title: "Set up?"),
        HealthItem(id: "setup.refresh", title: "Again"),
        HealthItem(id: "hooks.claude", severity: "error", title: "Claude Code's hooks aren't set up", detail: "d",
                   fix: .init(op: "init", step: "Run acmd init.", title: "Set Up")),
        HealthItem(id: "wake", title: "Jobs can't wake the Mac", fix: .init(command: ["schedule", "wake", "on"], step: "s")),
        HealthItem(id: "config.invalid", title: "config.toml has a mistake", fix: .init(step: "Fix line 3.")),
    ]
    let warnings = HealthWarnings.warnings(health) { ran.append($0) }
    #expect(warnings.map(\.id) == ["hooks.claude", "wake", "config.invalid"], "setup is offered at launch, not warned about")
    #expect(warnings[0].actionTitle == "Set Up" && warnings[0].fix == "Run acmd init." && warnings[0].detail == "d")
    #expect(warnings[1].actionTitle == "Fix", "a command with no title")
    #expect(warnings[2].actionTitle.isEmpty, "only a step: nothing to press")
    warnings.forEach { $0.action() }
    #expect(ran.map(\.op) == ["init", nil] && ran[1].command == ["schedule", "wake", "on"])
}

@Test func theHealthOpsSendWhatTheDaemonReads() {
    let fake = FakeCalls()
    let c = HealthClient(calls: fake)
    fake.replies = ["health.dismiss": ["wake"], "health.restore": [String](), "init": ["output": "Done.", "done": true],
                    "report.details": ["details": "acmd 0.64.0"], "report.page": ["page_url": "https://github.com/x", "body": "b"]]
    var out: [String] = []
    c.dismiss("wake", client: "mac") { out.append("\((try? $0.get()) ?? [])") }
    #expect(fake.last["client_id"] as? String == "mac" && fake.last["id"] as? String == "wake")
    c.dismiss(nil, client: "mac") { out.append("\((try? $0.get()) ?? ["fail"])") }
    c.setUp { out.append((try? $0.get().output) ?? "fail") }
    c.fix("remote.hosting", data: ["enabled": .bool(true)]) { out.append($0 == nil ? "fixed" : "fail") }
    #expect(fake.last["enabled"] as? Bool == true)
    c.reportDetails(.feature) { out.append((try? $0.get()) ?? "fail") }
    #expect(fake.last["kind"] as? String == "feature")
    var draft = ReportDraft(kind: .bug)
    draft.title = "Crash"
    draft.description = "It quit."
    draft.details = "acmd"
    c.reportPage(draft) { out.append((try? $0.get().page_url) ?? "fail") }
    #expect(fake.last["title"] as? String == "Crash" && (fake.last["body"] as? String)?.hasPrefix("It quit.") == true)
    #expect(fake.ops == ["health.dismiss", "health.restore", "init", "remote.hosting", "report.details", "report.page"])
    #expect(out == [#"["wake"]"#, "[]", "Done.", "fixed", "acmd 0.64.0", "https://github.com/x"])
    fake.failures["remote.hosting"] = "no"
    c.fix("remote.hosting", data: [:]) { out.append($0 == nil ? "fixed" : "failed") }
    #expect(out.last == "failed")
}

@MainActor @Test func theStoreKeepsDismissalsWithTheDaemon() {
    let fake = FakeCalls()
    let store = SessionStore()
    store.healthClient = HealthClient(calls: fake)
    store.setDismissed("wake", true)
    store.setDismissed("hooks.claude", true)
    #expect(store.dismissed == ["wake", "hooks.claude"] && fake.ops == ["health.dismiss", "health.dismiss"])
    store.setDismissed("wake", false)
    #expect(store.dismissed == ["hooks.claude"] && fake.ops.suffix(2) == ["health.restore", "health.dismiss"])
    #expect(fake.last["id"] as? String == "hooks.claude", "the rest are dismissed again")
    store.restoreDismissed()
    #expect(store.dismissed.isEmpty && fake.ops.last == "health.restore")
}
