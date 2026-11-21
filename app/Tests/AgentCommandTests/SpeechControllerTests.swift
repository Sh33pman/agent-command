import Foundation
import Testing

@testable import AgentCommand

/// The speech controller with the daemon played by the test: nothing plays and
/// nothing downloads.
@MainActor
private func controller(_ fake: FakeCalls, defaults: UserDefaults? = nil) throws -> (SpeechController, UserDefaults) {
    let name = "acmd-test-speech-\(UUID().uuidString)"
    let d = try #require(defaults ?? UserDefaults(suiteName: name))
    let settings = HostSettings()
    settings.client = SettingsClient(calls: fake)
    fake.replies["config.set"] = ["values": [String: Any]()]
    return (SpeechController(calls: fake, settings: settings, defaults: d), d)
}

private func item(_ id: Int64, _ text: String = "hello") throws -> InboxItem {
    try JSONDecoder().decode(InboxItem.self, from: JSONSerialization.data(withJSONObject: [
        "id": id, "session_id": "a", "kind": "done", "text": text, "state": "open",
    ]))
}

@MainActor @Test func speechFollowsTheDaemon() throws {
    let fake = FakeCalls()
    let (s, _) = try controller(fake)
    #expect(!s.isSupported && !s.isEnabled)
    s.apply(SpeechState(supported: true, enabled: true, installed: true))
    #expect(s.isSupported && s.isEnabled && s.isInstalled && !s.isSettingUp && fake.ops.isEmpty, "set up and on: nothing to do")
    s.apply(SpeechState(supported: true, enabled: true, installing: true, progress: 40))
    #expect(s.isSettingUp && s.setupProgress == 0.4)
    s.apply(SpeechState(supported: true, enabled: true, older: true, failure: "no network"))
    #expect(s.isUpgrading && s.setupFailure == "no network" && s.setupProgress == nil)
    s.apply(nil)
    #expect(s.isUpgrading, "no word keeps what was")
    s.apply(SpeechState(supported: false, enabled: true))
    #expect(!s.isEnabled, "where it isn't supported, it's off")
}

/// The app kept speech on or off in its defaults: the first snapshot moves it to
/// the device setting, and sets speech up if it's on.
@MainActor @Test func theOldSettingMovesToTheDaemon() throws {
    let fake = FakeCalls()
    let (s, d) = try controller(fake)
    d.set(true, forKey: SpeechController.oldEnabledKey)
    fake.replies["speech.install"] = ["supported": true, "enabled": true, "installing": true]
    s.apply(SpeechState(supported: true, enabled: false))
    #expect(d.object(forKey: SpeechController.oldEnabledKey) == nil)
    #expect(fake.ops == ["config.set", "speech.install"] && s.isEnabled && s.isSettingUp)
    s.apply(SpeechState(supported: true, enabled: true))
    #expect(fake.ops.count == 2, "only once")

    // On, but not set up: it sets up.
    let fake2 = FakeCalls()
    let (s2, _) = try controller(fake2)
    s2.apply(SpeechState(supported: true, enabled: true))
    #expect(fake2.ops == ["speech.install"])
    // Never on: nothing downloads.
    let fake3 = FakeCalls()
    let (s3, _) = try controller(fake3)
    s3.apply(SpeechState(supported: true))
    #expect(fake3.ops.isEmpty && !s3.isEnabled)
}

@MainActor @Test func turningItOnAndOff() throws {
    let fake = FakeCalls()
    let (s, _) = try controller(fake)
    s.apply(SpeechState(supported: true))
    fake.replies["speech.install"] = ["supported": true, "enabled": true, "installing": true, "progress": 10]
    s.setEnabled(true)
    #expect(fake.ops == ["config.set", "speech.install"] && fake.asked[0].input["values"] as? [String: Bool] == ["speech.enabled": true])
    #expect(s.setupProgress == 0.1)
    s.setEnabled(false)
    #expect(fake.ops.last == "config.set" && !s.isEnabled)
    fake.failures["speech.install"] = "no helper"
    s.apply(SpeechState(supported: true, enabled: true))
    s.retrySetup()
    #expect(s.setupFailure == "no helper" && !s.isSettingUp)
    fake.failures["config.set"] = "config.toml has a mistake"
    s.setEnabled(false)
    #expect(s.failure == "config.toml has a mistake")
    s.apply(SpeechState(supported: true, enabled: true, installed: true))
    fake.failures["speech.install"] = nil
    s.voiceChanged()
    #expect(fake.ops.last == "speech.install", "another voice downloads")
}

@MainActor @Test func readingGoesThroughTheDaemon() throws {
    let fake = FakeCalls()
    let (s, _) = try controller(fake)
    s.speak(try item(1))
    #expect(s.failure?.hasPrefix("Speech is off") == true && fake.ops.isEmpty)
    s.apply(SpeechState(supported: true, enabled: true, installing: true))
    s.speak(try item(1))
    #expect(s.failure?.contains("still being set up") == true)
    s.apply(SpeechState(supported: true, enabled: true, installed: true))

    fake.holding = true
    s.toggle(try item(1, "read me"))
    #expect(s.speakingItem == 1 && fake.last["text"] as? String == "read me")
    s.toggle(try item(2))
    #expect(s.speakingItem == 2 && fake.ops.suffix(2) == ["speech.stop", "speech.say"], "one at a time")
    s.toggle(try item(2))
    #expect(!s.isSpeaking && fake.ops.last == "speech.stop", "the same item stops it")
    fake.release()
    #expect(!s.isSpeaking, "an old answer doesn't bring it back")

    fake.holding = false
    fake.failures["speech.say"] = "speech engine: boom"
    s.speak(try item(3))
    #expect(s.failure == "speech engine: boom" && !s.isSpeaking)
    fake.failures["speech.say"] = nil
    s.speak(try item(4))
    #expect(!s.isSpeaking && s.failure == nil, "a finished read clears")
    s.stop()
    s.fail("why")
    #expect(s.failure == "why")
}

@Test func speechStateDecodes() throws {
    let st = try JSONDecoder().decode(SpeechState.self, from: Data(#"{"supported": true, "installing": true, "progress": 5, "voice": "af_bella"}"#.utf8))
    #expect(st.supported && st.installing && st.progress == 5 && st.voice == "af_bella" && !st.enabled)
}
