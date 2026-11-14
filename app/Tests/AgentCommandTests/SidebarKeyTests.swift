import AppKit
import Testing

@testable import AgentCommand

@Test func sidebarNumberKeysAndTab() {
    let keys = Keymap()
    for (n, code) in SidebarKey.numberKeyCodes.enumerated() {
        #expect(SidebarKey.from(keyCode: code, flags: [], keys: keys) == .number(n + 1))
    }
    #expect(SidebarKey.from(keyCode: 18, flags: .command, keys: keys) == nil, "⌘1 is the menu's")
    #expect(SidebarKey.from(keyCode: 29, flags: [], keys: keys) == nil, "no session 0")
    #expect(SidebarKey.from(keyCode: 48, flags: [], keys: keys) == .nextAttention)
    #expect(SidebarKey.from(keyCode: 48, flags: .shift, keys: keys) == nil)
}

@Test func sidebarMovesWithTheListKeys() {
    let keys = Keymap()
    #expect(SidebarKey.from(keyCode: Key.up, flags: [], keys: keys) == .up)
    #expect(SidebarKey.from(keyCode: Key.k, flags: .control, keys: keys) == .up)
    #expect(SidebarKey.from(keyCode: Key.k, flags: [], keys: keys) == .up, "plain k, as in Scheduled")
    #expect(SidebarKey.from(keyCode: Key.j, flags: [], keys: keys) == .down)
    #expect(SidebarKey.from(keyCode: 76, flags: [], keys: keys) == .open, "keypad enter is return")
    #expect(SidebarKey.from(keyCode: Key.escape, flags: [], keys: keys) == .leave)
    #expect(SidebarKey.from(keyCode: 7, flags: [], keys: keys) == .toggleMark)

    keys.set(.mark, [KeyCombo("m")!])
    #expect(SidebarKey.from(keyCode: 7, flags: [], keys: keys) == nil)
    #expect(SidebarKey.from(keyCode: 46, flags: [], keys: keys) == .toggleMark, "rebound to m")
}
