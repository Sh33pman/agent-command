import AppKit
import Carbon.HIToolbox
import Testing

@testable import AgentCommand

@Test func everyDefaultIsAShortcutTheActionCanUse() {
    for action in KeyAction.allCases {
        let combos = action.defaults.compactMap(KeyCombo.init)
        #expect(combos.count == action.defaults.count, "\(action) has a default that doesn't read")
        for combo in combos {
            #expect(action.problem(with: combo) == nil, "\(action): \(combo.display)")
        }
        if !action.takesSeveral { #expect(combos.count == 1, "\(action) is a menu's, with one key") }
    }
}

@Test func theDefaultsDontClash() {
    #expect(Keymap().clashing.isEmpty, "\(Keymap().clashing)")
}

@Test func shortcutsShowAsTheMenusDo() {
    let keys = Keymap()
    #expect(keys.label(.archive) == "⇧⌘⌫")
    #expect(keys.label(.focusLeft) == "⌃⌘H")
    #expect(keys.label(.splitRight) == "⌘↩")
    #expect(keys.label(.togglePopup) == "⌥⌘I")
    #expect(keys.label(.equalize) == "⌃⌘=")
    #expect(keys.label(.shortcuts) == "⌘/")
    #expect(keys.label(.session3) == "⌘3")
    #expect(keys.label(.mark) == "x", "a plain letter stays small")
    #expect(keys.label(.back) == "esc")
    #expect(keys.pair(.up, .down) == "↑↓ or ⌃K ⌃J or k j")
    #expect(keys.tip("Inbox", .inbox) == "Inbox (⇧⌘I)")
}

@Test func everyKeyReadsBackAsWritten() {
    for name in KeyName.all {
        for mods: KeyCombo.Mods in [[], [.command], [.control, .option, .shift, .command]] {
            let combo = KeyCombo(key: name.code, mods: mods)
            #expect(KeyCombo(combo.text) == combo, "\(combo.text)")
        }
    }
    #expect(KeyCombo("Ctrl+Cmd+J") == KeyCombo("ctrl+cmd+j"))
    #expect(KeyCombo("hyper+j") == nil && KeyCombo("cmd+nope") == nil)
    #expect(KeyCombo(key: 76) == KeyCombo("return"), "keypad enter is return")
}

@Test func menusGetTheirKeyEquivalent() throws {
    let archive = try #require(KeyCombo("shift+cmd+delete")?.menuKey)
    #expect(archive.key == "\u{8}" && archive.mask == [.shift, .command])
    let split = try #require(KeyCombo("cmd+return")?.menuKey)
    #expect(split.key == "\r" && split.mask == .command)
    #expect(KeyCombo("shift+cmd+i")?.menuKey?.key == "i")
}

@Test func changesAreSavedAndCanBePutBack() {
    var saved: [String: [String]] = [:]
    let keys = Keymap(save: { saved = $0 })
    keys.add(KeyCombo("y")!, to: .reply)
    #expect(keys.labels(.reply) == "r y" && saved == ["inbox.reply": ["r", "y"]], "the Inbox's keys take several")
    keys.add(KeyCombo("shift+cmd+k")!, to: .inbox)
    #expect(keys.labels(.inbox) == "⇧⌘K", "a menu's key is replaced")
    keys.remove(KeyCombo("r")!, from: .reply)
    #expect(keys.labels(.reply) == "y")
    keys.set(.reply, [KeyCombo("r")!])
    #expect(!keys.isChanged(.reply) && saved["inbox.reply"] == nil, "back to the default is no change")
    keys.remove(KeyCombo("r")!, from: .reply)
    #expect(keys.combos(.reply).isEmpty && saved["inbox.reply"] == [], "none at all is kept")

    let again = Keymap(saved: saved)
    #expect(again.label(.inbox) == "⇧⌘K" && again.combos(.reply).isEmpty)
    #expect(Keymap(saved: ["no.such": ["x"]]).clashing.isEmpty, "an unknown name is left out")
    again.resetAll()
    #expect(again.label(.inbox) == "⇧⌘I")
}

@Test func someKeysCantBeUsed() {
    #expect(KeyAction.inbox.problem(with: KeyCombo("i")!) != nil, "the terminal would lose it")
    #expect(KeyAction.inbox.problem(with: KeyCombo("ctrl+i")!) != nil)
    #expect(KeyAction.inbox.problem(with: KeyCombo("f5")!) == nil)
    #expect(KeyAction.popupAnywhere.problem(with: KeyCombo("cmd+o")!) != nil, "every app would lose ⌘O")
    #expect(KeyAction.popupAnywhere.problem(with: KeyCombo("opt+cmd+o")!) == nil)
    #expect(KeyAction.save.problem(with: KeyCombo("s")!) != nil, "you'd be typing it")
    #expect(KeyAction.save.problem(with: KeyCombo("return")!) != nil)
    #expect(KeyAction.leave.problem(with: KeyCombo("ctrl+g")!) == nil)
    #expect(KeyAction.reply.problem(with: KeyCombo("3")!) != nil, "numbers answer options")
    #expect(KeyAction.duplicateJob.problem(with: KeyCombo("cmd+c")!) != nil, "copy")
    #expect(KeyAction.mark.problem(with: KeyCombo(key: 105)) != nil, "a key with no name")
}

@Test func aSharedShortcutIsShown() {
    let keys = Keymap()
    keys.set(.newJob, [KeyCombo("g")!])
    #expect(keys.clashes(KeyCombo("g")!, for: .newJob) == [.go])
    #expect(Set(keys.clashing) == [.newJob, .go])
    keys.reset(.newJob)
    keys.set(.runJob, [KeyCombo("d")!])
    #expect(keys.clashing.isEmpty, "the Inbox's d and Scheduled's never show together")
    keys.set(.save, [KeyCombo("cmd+n")!])
    #expect(keys.clashing.isEmpty, "a reply being written takes its own keys first")
    keys.set(.popupAnywhere, [KeyCombo("shift+cmd+i")!])
    #expect(Set(keys.clashing) == [.popupAnywhere, .inbox])
}

@Test func theHintsFollowTheKeymap() {
    let keys = Keymap()
    #expect(keys.inboxHint.hasPrefix("↑↓ or ⌃K ⌃J move   ↩ open or answer   x mark   ⇥ next unread"))
    #expect(keys.popupHint.contains("r reply") && keys.popupHint.contains("u unread") && keys.popupHint.contains("x mark"))
    #expect(keys.scheduledHint.contains("⌘D duplicate") && keys.scheduledHint.hasSuffix("⌘/ all keys"))
    keys.set(.reply, [KeyCombo("y")!])
    keys.set(.unread, [])
    #expect(keys.inboxHint.contains("y reply") && !keys.inboxHint.contains("u unread"), "a key with no shortcut is left out")
}

@MainActor @Test func theShortcutsListRecordsTheNextKey() {
    let keys = Keymap()
    let model = ShortcutsModel(keys: keys)
    var closed = false
    model.close = { closed = true }
    model.record(.reply)
    #expect(keys.recording && model.recording == .reply)
    #expect(model.handle(KeyCombo("3")!))
    #expect(model.problem?.text.contains("1 to 9") == true && model.recording == .reply, "it keeps listening")
    #expect(model.handle(KeyCombo("y")!))
    #expect(keys.labels(.reply) == "r y" && model.recording == nil && !keys.recording)

    model.record(.inbox)
    #expect(model.handle(KeyCombo("escape")!))
    #expect(model.recording == nil && keys.label(.inbox) == "⇧⌘I", "esc cancels")
    #expect(!closed)
    #expect(model.handle(KeyCombo("escape")!))
    #expect(closed, "and otherwise closes")
}

@Test func theListSaysWhatEachKeyDoes() {
    let keys = Keymap()
    for action in KeyAction.allCases {
        #expect(!action.title.isEmpty && !action.section.title.isEmpty, "\(action)")
        _ = action.note
    }
    #expect(KeyAction.session4.title == "Show session 4" && KeyAction.session4.section == .goTo)
    #expect(Set(KeyAction.allCases.map(\.section)) == Set(KeySection.allCases), "every section has keys")
    #expect(FixedKeys.all.contains { $0.keys == "⌘K" && $0.section.hasPrefix("Terminal") })
    #expect(keys.choiceHint == "←→ or ⌃H ⌃L move   ↩ choose   esc leave")
    #expect(keys.replyHint == "⌘↩ send   esc leave")
    #expect(keys.jobFormHint.hasPrefix("⌘↩ save   esc cancel"))
    #expect(KeyCombo("ctrl+opt+shift+cmd+o")!.carbonMods == controlKey | optionKey | shiftKey | cmdKey)
    #expect(KeyCombo("ctrl+opt+shift+cmd+f1")!.display == "⌃⌥⇧⌘F1")
    #expect(KeyCombo(key: 105, mods: .command).display == "⌘key 105" && KeyCombo(key: 105).text == "key105")
    #expect(KeyCombo("space")!.types && !KeyCombo("cmd+space")!.types && !KeyCombo("tab")!.types)
}
