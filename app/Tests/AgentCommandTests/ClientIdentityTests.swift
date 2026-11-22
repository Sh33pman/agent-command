import Foundation
import Testing

@testable import AgentCommand

@Test func theAppKeepsItsIdAndSaysWhatItIs() throws {
    let d = try #require(UserDefaults(suiteName: "acmd-test-client-\(UUID().uuidString)"))
    let id = ClientIdentity.id(d)
    #expect(!id.isEmpty && ClientIdentity.id(d) == id, "made once")
    let hello = ClientIdentity.hello(d)
    #expect(hello.id == id && hello.kind == "mac" && hello.on_host && hello.caps.contains("notify"))
    let encoded = try JSONSerialization.jsonObject(with: JSONEncoder().encode(hello)) as? [String: Any]
    #expect(encoded?["on_host"] as? Bool == true && encoded?["caps"] as? [String] == ["notify", "sound", "attach", "speech"])
}

/// The app says how it's used only when that changes: in front, idle after a
/// few minutes without input, or behind another app; and the session it shows.
@MainActor @Test func presenceGoesToTheDaemonWhenItChanges() throws {
    let fake = FakeCalls()
    let p = PresenceReporter(calls: fake)
    var idle: TimeInterval = 0
    var shown = "s1"
    p.secondsIdle = { idle }
    p.focus = { shown }
    p.setActive(true)
    #expect(fake.ops == ["client.presence"] && fake.last["presence"] as? String == "active" && fake.last["focus"] as? String == "s1")
    p.update()
    #expect(fake.ops.count == 1, "nothing changed")
    idle = PresenceReporter.idleAfter + 1
    p.update()
    #expect(fake.last["presence"] as? String == "idle")
    idle = 0
    shown = "s2"
    p.update()
    #expect(fake.last["presence"] as? String == "active" && fake.last["focus"] as? String == "s2")
    p.setActive(false)
    #expect(fake.last["presence"] as? String == "background" && p.presence == "background")
    fake.failures["client.presence"] = "daemon not reachable"
    p.setActive(true)
    #expect(p.sent == nil, "a failed report is tried again")
    fake.failures["client.presence"] = nil
    p.update()
    #expect(p.sent?.presence == "active")
}

/// What opens the popup is kept by the daemon, in this app's profile; what the
/// app kept itself moves there once.
@MainActor @Test func thePopupsTriggerLivesInTheDaemon() throws {
    let d = try #require(UserDefaults(suiteName: "acmd-test-popup-\(UUID().uuidString)"))
    d.set("needsYou", forKey: "popup.opens")
    let fake = FakeCalls()
    fake.replies["client.profile"] = [String: Any]()
    let settings = PopupSettings(calls: fake, defaults: d)
    settings.loadProfile()
    #expect(settings.opens == .needsYou && d.string(forKey: "popup.opens") == nil)
    #expect(fake.ops == ["client.profile", "client.profile.set"] && fake.last["popup_opens"] as? String == "needs_you")

    // The daemon's wins, and isn't sent back.
    let fake2 = FakeCalls()
    fake2.replies["client.profile"] = ["popup_opens": "never"]
    let s2 = PopupSettings(calls: fake2, defaults: d)
    s2.loadProfile()
    #expect(s2.opens == .never && fake2.ops == ["client.profile"])
    s2.opens = .everything
    #expect(fake2.ops.last == "client.profile.set" && fake2.last["popup_opens"] as? String == "everything")

    // Nothing kept anywhere: it stays as it is.
    let fake3 = FakeCalls()
    fake3.replies["client.profile"] = [String: Any]()
    let s3 = PopupSettings(calls: fake3, defaults: d)
    s3.loadProfile()
    #expect(s3.opens == .everything && fake3.ops == ["client.profile"])
    d.set("everything", forKey: "popup.opens")
    s3.loadProfile()
    #expect(fake3.ops.last == "client.profile.set", "the same as now still moves")
    #expect(PopupOpens.allCases.allSatisfy { PopupOpens(wire: $0.wire) == $0 } && PopupOpens(wire: "x") == nil)
}
