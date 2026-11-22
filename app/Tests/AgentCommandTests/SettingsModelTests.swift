import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// The Settings window's model, with the daemon and acmd played by the test: nothing
/// reaches the person's real config.
@MainActor
private final class FakeAcmd {
    var calls: [[String]] = []
    var fail: Set<String> = []
    var said = "ok"

    func run(_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) {
        calls.append(args)
        done(!fail.contains(args.first ?? ""), said)
    }
}

/// config.get's answer, as the daemon gives it.
private func configState(_ values: [String: Any], path: String = "/tmp/x/config.toml", problem: [String: Any]? = nil) -> [String: Any] {
    var out: [String: Any] = ["path": path, "values": values, "settings": [
        ["key": "speech.voice", "type": "string", "where": "device", "suggestions": ["af_heart", "am_adam"]],
        ["key": "claude_command", "type": "string", "where": "host", "runs_code": true],
    ]]
    if let problem { out["problem"] = problem }
    return out
}

private let fullValues: [String: Any] = [
    "claude_command": "/bin/claude", "claude_args": ["--model", "opus"], "agent_remote_control": true,
    "agy_command": "/bin/agy", "agy_args": ["--x"],
    "notify.enabled": true, "notify.on_waiting": true, "notify.on_error": false, "notify.on_done": true,
    "notify.on_paused": false, "notify.done_min_duration": "1m0s", "notify.cooldown": "20s", "notify.sound": "",
    "speech.voice": "af_heart:0.6,af_bella:0.4", "speech.speed": 1.25, "editor.preset": "zed",
    "editor.command": [String](), "editor.run_in": "background", "editor.target": "git_root",
]

@MainActor
private func model(_ fake: FakeCalls, _ given: FakeAcmd? = nil) -> SettingsModel {
    let acmd = given ?? FakeAcmd()
    let m = SettingsModel()
    m.client = SettingsClient(calls: fake)
    m.runAcmd = { args, done in acmd.run(args, done) }
    fake.replies["config.get"] = configState(fullValues)
    fake.replies["config.set"] = configState(fullValues)
    return m
}

@MainActor @Test func settingsReadWhatTheDaemonSays() {
    let fake = FakeCalls()
    let m = model(fake)
    #expect(!m.loaded)
    m.load()
    let v = m.values
    #expect(fake.ops == ["config.get"])
    #expect(m.loaded && m.acmd.path == "/tmp/x/config.toml" && m.acmd.voices == ["af_heart", "am_adam"] && m.acmd.problem == nil)
    #expect(v.claudeCommand == "/bin/claude" && v.claudeArgs == ["--model", "opus"] && v.agentRemoteControl)
    #expect(v.agyCommand == "/bin/agy" && v.agyArgs == ["--x"])
    #expect(!v.onError && v.doneMin == "1m0s" && v.sound.isEmpty && v.speed == 1.25 && v.editorTarget == "git_root")

    fake.replies["config.get"] = configState(["notify.cooldown": "5s"], path: "p", problem: ["line": 3, "message": "expected a value"])
    m.load()
    #expect(m.values.cooldown == "5s" && m.values.claudeCommand == "claude" && m.values.agyCommand == "agy" && m.values.agyArgs.isEmpty,
            "what's missing is the default")
    #expect(m.acmd.problem?.text == "config.toml, line 3: expected a value")

    fake.failures["config.get"] = "daemon not reachable"
    m.load()
    #expect(m.error == "Couldn't read acmd's settings: daemon not reachable")
}

@MainActor @Test func aChangeGoesThroughTheDaemon() {
    let fake = FakeCalls()
    let m = model(fake)
    m.set("notify.on_error", true)
    #expect(fake.last["values"] as? [String: Bool] == ["notify.on_error": true])
    m.set("notify.cooldown", "60s")
    #expect(fake.last["values"] as? [String: String] == ["notify.cooldown": "60s"], "as typed: the daemon reads it")
    m.setArgs(#"--model opus --append-system-prompt "be brief""#)
    #expect(fake.last["values"] as? [String: [String]] == ["claude_args": ["--model", "opus", "--append-system-prompt", "be brief"]])
    m.setArgs("", key: "agy_args")
    #expect(fake.last["values"] as? [String: [String]] == ["agy_args": []])
    #expect(fake.ops == ["config.set", "config.set", "config.set", "config.set"], "one call each, and the reply is the settings")
    #expect(m.busy == nil && m.error == nil && m.values.claudeCommand == "/bin/claude")
    #expect(HostSettings.shared.values["claude_command"] == .string("/bin/claude"), "this Mac's settings follow")

    // Several at once: one call, all or none.
    m.setAll([("remote.host.port", "7500"), ("remote.host.port_fallback", "off")])
    #expect(fake.ops.last == "config.set" && fake.last["values"] as? [String: String] == ["remote.host.port": "7500", "remote.host.port_fallback": "off"])
    m.setAll([])
    #expect(fake.ops.count == 5, "nothing to change")

    fake.failures["config.set"] = "notify.cooldown is a length of time like 10s or 2m, not \"soon\""
    m.set("notify.cooldown", "soon")
    #expect(m.error == fake.failures["config.set"] && fake.ops.last == "config.get", "it reads them back")
    fake.failures["config.set"] = ""
    m.set("notify.cooldown", "soon")
    #expect(m.error == "Couldn't change notify.cooldown.")
}

@MainActor @Test func aHostsSettingsAreItsOwn() {
    let fake = FakeCalls()
    let m = model(fake)
    fake.replies["remote.config.get"] = configState(["claude_command": "/opt/claude"], path: "/home/me/config.toml")
    fake.replies["remote.config.set"] = configState(["claude_command": "/opt/claude2"], path: "/home/me/config.toml")
    m.section = .claude
    m.host = "studio"
    #expect(fake.ops.last == "remote.config.get" && fake.last["host"] as? String == "studio")
    #expect(m.target == "studio" && m.values.claudeCommand == "/opt/claude")
    m.set("claude_command", "/opt/claude2")
    #expect(fake.ops.last == "remote.config.set" && fake.last["host"] as? String == "studio")
    #expect(m.values.claudeCommand == "/opt/claude2")
    let before = HostSettings.shared.values["claude_command"]
    #expect(HostSettings.shared.values["claude_command"] == before, "a host's change isn't this Mac's")

    // The editor is this Mac's, whichever host is chosen.
    m.section = .editor
    #expect(m.target == nil && fake.ops.last == "config.get")
    m.section = .speech
    #expect(fake.ops.filter { $0 == "config.get" }.count == 1, "no reload between two of this Mac's pages")
    m.section = .notifications
    #expect(fake.ops.last == "remote.config.get")

    fake.failures["remote.config.get"] = "studio isn't connected"
    m.load()
    #expect(m.error == "Couldn't read studio's settings: studio isn't connected")
    // An answer for a host no longer chosen is dropped.
    fake.holding = true
    m.load()
    m.host = nil
    fake.release()
    #expect(m.target == nil && m.values.claudeCommand == "/bin/claude")
}

@MainActor @Test func aNewVoiceIsDownloadedOnceSaved() {
    let fake = FakeCalls()
    let acmd = FakeAcmd()
    let m = model(fake, acmd)
    var changed = 0
    m.voiceChanged = { changed += 1 }
    m.setVoice("am_adam")
    #expect(changed == 1 && fake.last["values"] as? [String: String] == ["speech.voice": "am_adam"])
    fake.failures["config.set"] = "can't use voice bf_emma"
    m.setVoice("bf_emma")
    #expect(changed == 1, "not when it wasn't saved")
    m.playSample()
    #expect(fake.ops.last == "speech.say" && fake.last["text"] as? String == SettingsModel.voiceSample)
    fake.failures["speech.say"] = ""
    m.playSample()
    #expect(m.error == "Couldn't read the sample.")
    #expect(acmd.calls.isEmpty, "no process")
}

@MainActor @Test func theEditorWakingAndSetup() {
    let fake = FakeCalls()
    let acmd = FakeAcmd()
    let m = model(fake, acmd)
    m.setEditor("zed")
    #expect(fake.last["values"] as? [String: AnyHashable] == ["editor.preset": "zed", "editor.command": [String](), "editor.run_in": "background"])
    m.setEditor("custom", command: ["nvim", "{dir}"])
    #expect((fake.last["values"] as? [String: Any])?["editor.command"] as? [String] == ["nvim", "{dir}"])
    m.setWake(true)
    m.setWake(false)
    m.setWakeAll(false)
    #expect(acmd.calls == [["schedule", "wake", "on"], ["schedule", "wake", "off"], ["schedule", "wake", "on", "--all=false"]])
    acmd.fail = ["schedule"]
    acmd.said = ""
    m.setWake(true)
    #expect(m.error == "Couldn't change wake.")
    acmd.said = "hooks: ok"
    acmd.fail = []
    m.checkSetup()
    #expect(m.report == "hooks: ok" && acmd.calls.last == ["doctor"])
    acmd.fail = ["doctor"]
    acmd.said = "hooks: missing"
    m.repairSetup()
    #expect(m.report == "hooks: missing" && m.error == nil, "doctor's report says what's wrong")
    #expect(acmd.calls.last == ["doctor", "--fix"])
}

@MainActor @Test func theFontIsSetInTheAppsGhosttyConfig() {
    let m = SettingsModel()
    var text = "theme = Nord\nfont-family = Menlo\nfont-family = Symbols Nerd Font\n"
    var applied = 0
    m.readGhostty = { text }
    m.writeGhostty = { text = $0 }
    m.ghosttyChanged = { applied += 1 }
    #expect(m.fontFamily == "Menlo" && m.fontSize == 13)
    m.setFontFamily("Fira Code")
    m.setFontSize(15)
    #expect(text == "theme = Nord\nfont-family = \"Fira Code\"\nfont-family = Symbols Nerd Font\nfont-size = 15\n")
    #expect(m.fontFamily == "Fira Code" && m.fontSize == 15 && applied == 2)
    m.setFontSize(13.5)
    #expect(m.fontSize == 13.5)
    m.setFontFamily(nil)
    m.setFontSize(nil)
    #expect(text == "theme = Nord\nfont-family = Symbols Nerd Font\n", "the defaults again; a fallback stays")

    struct Refused: Error {}
    m.writeGhostty = { _ in throw Refused() }
    m.setFontSize(20)
    #expect(m.error?.hasPrefix("Couldn't save the font") == true && applied == 5, "not applied")
    #expect(SettingsModel.monospacedFamilies.contains(AppFont.defaultFamily))
}

@Test func goDurationsReadAndSay() {
    #expect(GoDuration.seconds("10s") == 10 && GoDuration.seconds("1m30s") == 90 && GoDuration.seconds("2h0m0s") == 7200)
    #expect(GoDuration.seconds("1.5s") == 1.5 && GoDuration.seconds("500ms") == 0.5 && GoDuration.seconds("0s") == 0)
    #expect(GoDuration.seconds("") == nil && GoDuration.seconds("soon") == nil && GoDuration.seconds("10") == nil)
    #expect(GoDuration.label(0) == "No wait" && GoDuration.label(10) == "10 seconds" && GoDuration.label(60) == "1 minute")
    #expect(GoDuration.label(300) == "5 minutes" && GoDuration.label(90) == "1 min 30 s")
    let m = SettingsModel()
    #expect(m.waitChoices(current: "20s") == [0, 5, 10, 20, 30, 60, 120, 300])
    #expect(m.waitChoices(current: "1m0s") == SettingsModel.waits)
}

@Test func argumentsAreWordsWithQuotes() {
    #expect(ShellWords.split(#"--model opus  --x "two words" 'it''s' """#) == ["--model", "opus", "--x", "two words", "its", ""])
    #expect(ShellWords.split("   ") == [])
    #expect(ShellWords.join(["--x", "two words", ""]) == #"--x "two words" """#)
    #expect(ShellWords.split(ShellWords.join(["a b", "c"])) == ["a b", "c"])
}

@Test func theMenuBarIconIsOnUntilTurnedOff() throws {
    let defaults = try #require(UserDefaults(suiteName: "acmd-test-menubar-\(getpid())"))
    defer { defaults.removePersistentDomain(forName: "acmd-test-menubar-\(getpid())") }
    let pref = MenuBarPreference(defaults: defaults)
    var told = 0
    pref.onChange = { told += 1 }
    #expect(pref.shown, "on by default")
    pref.shown = false
    pref.shown = false
    #expect(told == 1, "only a change is told")
    #expect(!MenuBarPreference(defaults: defaults).shown, "kept")
    pref.shown = true
    #expect(told == 2 && MenuBarPreference(defaults: defaults).shown)
}

/// A change sends the version of each setting it makes; one refused because
/// the setting changed since shows the settings as they are now, and Use
/// Mine makes it over them.
@MainActor @Test func aRefusedSettingCanStillBeMade() {
    let fake = FakeCalls()
    let m = model(fake)
    var state = configState(fullValues)
    state["versions"] = ["claude_command": "v1", "notify.on_done": "n1"]
    fake.replies["config.get"] = state
    m.load()
    fake.failures["config.set"] = "claude_command changed since you loaded it"
    fake.codes["config.set"] = "conflict"
    m.set("claude_command", "my-claude")
    let sent = fake.asked.last { $0.op == "config.set" }?.input
    #expect(sent?["versions"] as? [String: String] == ["claude_command": "v1"], "only the changed key's")
    #expect(m.refused != nil && m.error?.contains("Use Mine") == true && fake.ops.last == "config.get", "it shows them as they are")

    state["versions"] = ["claude_command": "v2", "notify.on_done": "n1"]
    fake.replies["config.get"] = state
    m.load()
    fake.failures = [:]
    m.useMine()
    let again = fake.asked.last { $0.op == "config.set" }?.input
    #expect(again?["versions"] as? [String: String] == ["claude_command": "v2"] && m.refused == nil)
    #expect((again?["values"] as? [String: Any])?["claude_command"] as? String == "my-claude")
}
