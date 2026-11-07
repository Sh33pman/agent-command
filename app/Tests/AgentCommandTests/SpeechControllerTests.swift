import Foundation
import Testing

@testable import AgentCommand

/// The speech controller with a fake acmd, so nothing plays and nothing downloads.
extension UsesSharedSpeech {
@Suite @MainActor
struct SpeechControllerTests {
    private let speech = SpeechController.shared

    /// A stand-in for `acmd speech ...`, which does what the script says for each.
    private func fakeAcmd(status: String = "Speech: set up", say: String = "sleep 30", install: String = "exit 0") throws {
        let dir = NSTemporaryDirectory() + "acmd-speech-tests-\(getpid())"
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let path = dir + "/fake-acmd"
        let script = """
            #!/bin/sh
            case "$2" in
            status) echo "\(status)";;
            say) cat > /dev/null; \(say);;
            install) \(install);;
            esac
            """
        try script.write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: path)
        setenv("ACMD_BIN", path, 1)
        try #require(DaemonPaths.acmd == path)
        UserDefaults.standard.removeObject(forKey: "speech.enabled")
        speech.setEnabled(false)
        UserDefaults.standard.removeObject(forKey: "speech.enabled")
    }

    private func item(_ id: Int64) throws -> InboxItem { try makeItem(id, session: "a") }

    private func wait(_ what: String, until done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async throws {
        for _ in 0..<300 {
            if done() { return }
            try await Task.sleep(for: .milliseconds(20))
        }
        Issue.record("timed out waiting for \(what)", sourceLocation: sourceLocation)
    }

    private func turnOn() async throws {
        speech.checkSetup()
        try await wait("speech on") { speech.isEnabled && speech.isInstalled }
    }

    private func readStatusNotInstalled() async throws {
        speech.checkSetup()
        try await wait("the status") { !speech.isInstalled }
    }

    // MARK: Whether it's on

    @Test func someoneWhoNeverSetItUpHasItOffWithNoDownload() async throws {
        try fakeAcmd(status: "Speech: not installed")
        speech.checkSetup()
        try await Task.sleep(for: .milliseconds(300))
        #expect(!speech.isEnabled && !speech.isInstalled && !speech.isSettingUp)
    }

    @Test func someoneWhoHasItKeepsIt() async throws {
        try fakeAcmd()
        try await turnOn()
        #expect(!speech.isSettingUp)
    }

    @Test func anOlderEngineUpgradesByItself() async throws {
        try fakeAcmd(status: "Speech: set up for an older engine, which acmd no longer uses", install: "sleep 0.3; exit 0")
        speech.checkSetup()
        try await wait("the upgrade to start") { speech.isSettingUp }
        #expect(speech.isEnabled && speech.isUpgrading)
        try await wait("the upgrade to finish") { !speech.isSettingUp }
        #expect(speech.isInstalled && speech.setupFailure == nil)
    }

    @Test func turningItOnDownloadsAndShowsProgress() async throws {
        try fakeAcmd(status: "Speech: not installed", install: "echo 'progress 42'; sleep 0.5; exit 0")
        try await readStatusNotInstalled()
        speech.setEnabled(true)
        #expect(speech.isSettingUp && !speech.isUpgrading)
        try await wait("progress") { speech.setupProgress == 0.42 }
        try await wait("the install to finish") { !speech.isSettingUp }
        #expect(speech.isInstalled && speech.setupProgress == nil)
    }

    @Test func aFailedInstallSaysWhyAndCanBeRetried() async throws {
        try fakeAcmd(status: "Speech: not installed", install: "echo 'error: no network' >&2; exit 1")
        try await readStatusNotInstalled()
        speech.setEnabled(true)
        try await wait("the failure") { speech.setupFailure != nil }
        #expect(speech.setupFailure == "no network")
        #expect(!speech.isInstalled)
        try fakeAcmd(status: "Speech: not installed", install: "exit 0")
        speech.setEnabled(true)
        try await wait("the retry") { speech.isInstalled }
        #expect(speech.setupFailure == nil)
    }

    @Test func turningItOffStopsADownload() async throws {
        try fakeAcmd(status: "Speech: not installed", install: "sleep 30")
        try await readStatusNotInstalled()
        speech.setEnabled(true)
        #expect(speech.isSettingUp)
        speech.setEnabled(false)
        #expect(!speech.isSettingUp && !speech.isEnabled)
        try await Task.sleep(for: .milliseconds(200))
        #expect(speech.setupFailure == nil, "stopping it isn't a failure")
    }

    // MARK: Reading

    @Test func offItSaysHowToTurnItOn() throws {
        try fakeAcmd()
        speech.speak(try item(1))
        #expect(speech.speakingItem == nil)
        #expect(speech.failure?.contains("Speech is off") == true)
    }

    @Test func oneReadAtATimeAndTheSameItemStopsIt() async throws {
        try fakeAcmd()
        try await turnOn()
        speech.speak(try item(1))
        #expect(speech.speakingItem == 1 && speech.isSpeaking)
        speech.speak(try item(2))
        #expect(speech.speakingItem == 2)
        speech.toggle(try item(1))
        #expect(speech.speakingItem == 1, "another item's button starts that one")
        speech.toggle(try item(1))
        #expect(speech.speakingItem == nil && !speech.isSpeaking)
        try await Task.sleep(for: .milliseconds(200))
        #expect(speech.failure == nil, "stopping isn't a failure")
    }

    @Test func aFinishedReadClears() async throws {
        try fakeAcmd(say: "exit 0")
        try await turnOn()
        speech.speak(try item(1))
        try await wait("the read to end") { !speech.isSpeaking }
        #expect(speech.failure == nil)
    }

    @Test func aFailedReadSaysWhy() async throws {
        try fakeAcmd(say: "echo 'error: speech engine: boom' >&2; exit 1")
        try await turnOn()
        speech.speak(try item(1))
        try await wait("the failure") { speech.failure != nil }
        #expect(speech.failure == "speech engine: boom")
        #expect(!speech.isSpeaking)
    }

    @Test func aWholeLongMessageDoesntHangIt() async throws {
        try fakeAcmd(say: "exit 0")
        try await turnOn()
        let long = String(repeating: "A long message. ", count: 40_000)
        let data = try JSONSerialization.data(withJSONObject: ["id": 9, "session_id": "a", "kind": "done", "text": long, "state": "open"])
        speech.speak(try JSONDecoder().decode(InboxItem.self, from: data))
        try await wait("the read to end") { !speech.isSpeaking }
    }
}
}
