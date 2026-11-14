import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// The Settings window's model, with acmd played by the test: nothing reaches the
/// person's real config.
@MainActor
private final class FakeAcmd {
    var calls: [[String]] = []
    var fail: Set<String> = []
    var show = """
        {"path": "/tmp/x/config.toml", "voices": ["af_heart", "am_adam"], "settings": {
          "claude_command": "/bin/claude", "claude_args": ["--model", "opus"], "remote_control": true,
          "notify.enabled": true, "notify.on_waiting": true, "notify.on_error": false, "notify.on_done": true,
          "notify.on_paused": false, "notify.done_min_duration": "1m0s", "notify.cooldown": "20s", "notify.sound": "",
          "speech.voice": "af_heart:0.6,af_bella:0.4", "speech.speed": 1.25, "editor.preset": "zed",
          "editor.command": [], "editor.run_in": "background", "editor.target": "git_root"}}
        """
    var said = "ok"

    func run(_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) {
        calls.append(args)
        if args == ["config", "show", "--json"] {
            done(!fail.contains("show"), fail.contains("show") ? "" : show)
        } else {
            done(!fail.contains(args.first ?? ""), said)
        }
    }

    var changes: [[String]] { calls.filter { $0 != ["config", "show", "--json"] } }
}

@MainActor
private func model(_ acmd: FakeAcmd) -> SettingsModel {
    let m = SettingsModel()
    m.runAcmd = { args, done in acmd.run(args, done) }
    return m
}

@MainActor @Test func settingsReadWhatAcmdSays() {
    let acmd = FakeAcmd()
    let m = model(acmd)
    #expect(!m.loaded)
    m.load()
    let v = m.values
    #expect(m.loaded && m.acmd.path == "/tmp/x/config.toml" && m.acmd.voices == ["af_heart", "am_adam"])
    #expect(v.claudeCommand == "/bin/claude" && v.claudeArgs == ["--model", "opus"] && v.remoteControl)
    #expect(!v.onError && v.doneMin == "1m0s" && v.sound.isEmpty && v.speed == 1.25 && v.editorTarget == "git_root")

    acmd.show = #"{"path": "p", "voices": [], "settings": {"notify.cooldown": "5s"}}"#
    m.load()
    #expect(m.values.cooldown == "5s" && m.values.claudeCommand == "claude", "what's missing is the default")

    acmd.fail = ["show"]
    m.load()
    #expect(m.error == "Couldn't read acmd's settings.")
}

@MainActor @Test func aChangeGoesThroughAcmdConfig() {
    let acmd = FakeAcmd()
    let m = model(acmd)
    m.set("notify.on_error", true)
    m.set("notify.cooldown", "60s")
    m.setArgs(#"--model opus --append-system-prompt "be brief""#)
    m.setArgs("")
    #expect(acmd.changes == [
        ["config", "set", "--", "notify.on_error", "on"],
        ["config", "set", "--", "notify.cooldown", "60s"],
        ["config", "set", "--", "claude_args", #"["--model","opus","--append-system-prompt","be brief"]"#],
        ["config", "set", "--", "claude_args", "[]"],
    ])
    #expect(acmd.calls.count == 8, "each change reads the settings back")
    #expect(m.busy == nil && m.error == nil)

    acmd.fail = ["config"]
    acmd.said = "notify.cooldown is a length of time like 10s or 2m, not \"soon\""
    m.set("notify.cooldown", "soon")
    #expect(m.error == acmd.said)
    acmd.said = ""
    m.set("notify.cooldown", "soon")
    #expect(m.error == "Couldn't change notify.cooldown.")
}

@MainActor @Test func aNewVoiceIsDownloadedOnceSaved() {
    let acmd = FakeAcmd()
    let m = model(acmd)
    var changed = 0
    m.voiceChanged = { changed += 1 }
    m.setVoice("am_adam")
    #expect(changed == 1 && acmd.changes.last == ["config", "set", "--", "speech.voice", "am_adam"])
    acmd.fail = ["config"]
    m.setVoice("bf_emma")
    #expect(changed == 1, "not when it wasn't saved")
    m.playSample()
    #expect(acmd.changes.last == ["speech", "say", SettingsModel.voiceSample])
    acmd.fail = ["speech"]
    acmd.said = ""
    m.playSample()
    #expect(m.error == "Couldn't read the sample.")
}

@MainActor @Test func theEditorWakingAndSetupUseTheirOwnCommands() {
    let acmd = FakeAcmd()
    let m = model(acmd)
    m.setEditor("zed")
    m.setEditor("custom", command: ["nvim", "{dir}"])
    m.setWake(true)
    m.setWake(false)
    m.setWakeAll(false)
    #expect(acmd.changes == [
        ["editor", "set", "zed"], ["editor", "set", "custom", "--", "nvim", "{dir}"],
        ["schedule", "wake", "on"], ["schedule", "wake", "off"], ["schedule", "wake", "on", "--all=false"],
    ])
    acmd.said = "hooks: ok"
    m.checkSetup()
    #expect(m.report == "hooks: ok" && acmd.changes.last == ["doctor"])
    acmd.fail = ["doctor"]
    acmd.said = "hooks: missing"
    m.repairSetup()
    #expect(m.report == "hooks: missing" && m.error == nil, "doctor's report says what's wrong")
    #expect(acmd.changes.last == ["doctor", "--fix"])
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
