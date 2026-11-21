import Foundation
import Testing

@testable import AgentCommand

@Test func settingValuesReadAndWriteAsJSON() throws {
    let json = #"{"a": true, "b": 7433, "c": "10s", "d": ["--x", "y"], "e": 1.25}"#
    let values = try JSONDecoder().decode([String: SettingValue].self, from: Data(json.utf8))
    #expect(values == ["a": .bool(true), "b": .number(7433), "c": .string("10s"), "d": .list(["--x", "y"]), "e": .number(1.25)])
    #expect(values["a"]?.bool == true && values["c"]?.bool == nil && values["c"]?.string == "10s" && values["a"]?.string == nil)
    let again = try JSONDecoder().decode([String: SettingValue].self, from: JSONEncoder().encode(values))
    #expect(again == values)
    #expect(throws: (any Error).self) { try JSONDecoder().decode(SettingValue.self, from: Data(#"{"x": 1}"#.utf8)) }
}

@Test func configStateReadsWhatEachSettingIs() throws {
    let json = #"""
        {"path": "/c/config.toml", "values": {"remote.host.port": 7500, "remote.host.enabled": true},
         "settings": [{"key": "claude_command", "type": "string", "description": "d", "where": "host", "runs_code": true},
                      {"key": "editor.resolved", "type": "string", "where": "device", "read_only": true}],
         "problem": {"message": "bad"}}
        """#
    let st = try JSONDecoder().decode(ConfigState.self, from: Data(json.utf8))
    #expect(st.settings[0].runsCode == true && st.settings[0].place == "host" && st.settings[1].place == "device" && st.settings[1].readOnly == true)
    #expect(st.problem?.text == "config.toml: bad")
    let typed = AcmdSettings(state: st)
    #expect(typed.settings.hostPort == 7500 && typed.settings.hostEnabled && typed.path == "/c/config.toml" && typed.voices.isEmpty)
}

@Test func theSettingsOpsSendWhatTheDaemonReads() {
    let fake = FakeCalls()
    let c = SettingsClient(calls: fake)
    fake.replies = ["config.get": ["path": "p"], "remote.config.get": ["path": "h"], "config.set": ["path": "p"], "remote.config.set": ["path": "h"]]
    var paths: [String] = []
    let note: (Result<ConfigState, Error>) -> Void = { paths.append((try? $0.get().path) ?? "fail") }
    c.get(done: note)
    c.get(host: "studio", done: note)
    #expect(fake.last["host"] as? String == "studio")
    c.set(["notify.sound": .string("")], done: note)
    #expect(fake.last["host"] == nil && fake.last["values"] as? [String: String] == ["notify.sound": ""])
    c.set(["notify.sound": .string("")], host: "studio", done: note)
    #expect(fake.last["host"] as? String == "studio")
    #expect(fake.ops == ["config.get", "remote.config.get", "config.set", "remote.config.set"] && paths == ["p", "h", "p", "h"])
    fake.replies["config.get"] = ["values": "not a map"]
    c.get(done: note)
    #expect(paths.last == "fail")
}

@MainActor @Test func thisMacsSettingsAreReadFromTheSnapshot() {
    let settings = HostSettings()
    let fake = FakeCalls()
    settings.client = SettingsClient(calls: fake)
    settings.values = ["agent_remote_control": .bool(true), "default_harness": .string("antigravity")]
    #expect(settings.bool("agent_remote_control") && !settings.bool("missing") && settings.string("default_harness") == "antigravity")
    #expect(settings.string("agent_remote_control").isEmpty)

    fake.replies["config.set"] = ["values": ["agent_remote_control": false]]
    settings.set(["agent_remote_control": .bool(false)])
    #expect(!settings.bool("agent_remote_control") && settings.values.count == 1, "the reply is the settings in effect")
    var why = ""
    fake.failures["config.set"] = "config.toml has a mistake: fix it first"
    settings.set(["agent_remote_control": .bool(true)]) { why = $0 }
    #expect(why == "config.toml has a mistake: fix it first" && !settings.bool("agent_remote_control"))
}

@Test func aSnapshotCarriesTheSettingsAndHealth() throws {
    let json = #"""
        {"sessions": [], "settings": {"default_harness": "claude"},
         "health": [{"id": "config.invalid", "severity": "warning", "title": "config.toml has a mistake",
                     "detail": "line 3", "fix": {"step": "Fix line 3"}}, {"id": "x", "title": "t"}]}
        """#
    let s = try JSONDecoder().decode(Snapshot.self, from: Data(json.utf8))
    #expect(s.settings?["default_harness"] == .string("claude"))
    #expect(s.health == [
        HealthItem(id: "config.invalid", title: "config.toml has a mistake", detail: "line 3", fix: .init(step: "Fix line 3")),
        HealthItem(id: "x", title: "t"),
    ])
}

@Test func hostsAndClientsInConfigSaySo() throws {
    let host = try JSONDecoder().decode(RemoteHost.self, from: Data(#"{"name": "studio", "in_config": true}"#.utf8))
    let client = try JSONDecoder().decode(RemoteClient.self, from: Data(#"{"name": "laptop", "fingerprint": "f", "in_config": true}"#.utf8))
    #expect(host.inConfig && client.inConfig)
    #expect(try !JSONDecoder().decode(RemoteHost.self, from: Data(#"{"name": "studio"}"#.utf8)).inConfig)
}
