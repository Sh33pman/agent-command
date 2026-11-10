import AppKit
import Testing

@testable import AgentCommand

@Test func sidebarNumberKeysAndTab() {
    let digits = ["1", "2", "3", "4", "5", "6", "7", "8", "9"]
    for (n, code) in SidebarKey.numberKeyCodes.enumerated() {
        #expect(SidebarKey.from(keyCode: code, characters: digits[n], flags: []) == .number(n + 1))
    }
    #expect(SidebarKey.from(keyCode: 18, characters: "&", flags: []) == .number(1), "the key, not the layout's character")
    #expect(SidebarKey.from(keyCode: 18, characters: "1", flags: .command) == nil, "⌘1 is the menu's")
    #expect(SidebarKey.from(keyCode: 29, characters: "0", flags: []) == nil, "no session 0")
    #expect(SidebarKey.from(keyCode: 48, characters: "\t", flags: []) == .nextAttention)
    #expect(SidebarKey.from(keyCode: 48, characters: "\t", flags: .shift) == nil)
    #expect(SidebarKey.number(3).isSidebarOnly && SidebarKey.nextAttention.isSidebarOnly)
    #expect(!SidebarKey.up.isSidebarOnly && !SidebarKey.open.isSidebarOnly)
}

@Test func theAppShortcutsDontClash() {
    #expect(SystemHotKey.Combo.popup != SystemHotKey.Combo.app)
    #expect(SystemHotKey.Combo.popup.id != SystemHotKey.Combo.app.id)
}
