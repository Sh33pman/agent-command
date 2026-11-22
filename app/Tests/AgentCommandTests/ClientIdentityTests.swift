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
