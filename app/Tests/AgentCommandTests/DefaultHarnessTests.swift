import Foundation
import Testing
@testable import AgentCommand

struct DefaultHarnessTests {
    @Test func readsTheSetting() {
        #expect(DefaultHarness.value("") == nil, "unset: ⌘N asks")
        #expect(DefaultHarness.value("antigravity") == nil, "a kind isn't a harness: it's by id")
        #expect(DefaultHarness.value("terminal") == nil && DefaultHarness.value("nope") == nil)
    }

    @Test func menuTitlesAndChanges() {
        #expect(DefaultHarness.menuTitle(.claude) == "New Claude Session")
        #expect(DefaultHarness.menuTitle(.antigravity) == "New Antigravity Session")
        #expect(DefaultHarness.menuTitle(nil) == "New Session (Pick a Default)…")
        #expect(DefaultHarness.change(Harness(id: "HA", kind: "antigravity")) == ["default_harness": .string("HA")], "by id")
        #expect(DefaultHarness.change(nil) == ["default_harness": .string("")])
    }

    @Test func onlyAnAgentBecomesTheDefault() {
        #expect(HarnessPickerView.makesDefault(.antigravity, checked: true))
        #expect(HarnessPickerView.makesDefault(.claude, checked: true))
        #expect(!HarnessPickerView.makesDefault(.terminal, checked: true))
        #expect(!HarnessPickerView.makesDefault(.claude, checked: false))
    }

    /// The new-session checkbox shows only while there's no default, for this
    /// Mac: once one is set, only Settings changes it. ⌘D ticks it.
    @Test func theCheckboxShowsOnlyWithoutADefault() {
        #expect(HarnessPickerView.showsDefault(offered: true, host: nil))
        #expect(!HarnessPickerView.showsDefault(offered: false, host: nil), "a default is set")
        #expect(!HarnessPickerView.showsDefault(offered: true, host: "studio"), "a host's session")
        #expect(!HarnessPickerView.makesDefault(.claude, checked: true, offered: false), "hidden: a pick never changes it")
        #expect(HarnessPickerView.makesDefault(.claude, checked: true, offered: true))
        #expect(HarnessPickerView.toggleDefaultKey == KeyCombo("cmd+d"))
    }

    @Test func theShortcutShowsOnTheDefault() {
        let keys = PaletteState().keys
        func shortcut(_ title: String, _ harness: Harness?) -> String? {
            Palette.entries(PaletteState(defaultHarness: harness)).first { $0.section == "New" && $0.title == title }?.shortcut
        }
        #expect(shortcut("Antigravity session", .antigravity) == keys.label(.newClaude))
        #expect(shortcut("Claude session", .antigravity) == "")
        #expect(shortcut("Claude session", nil) == "" && shortcut("Antigravity session", nil) == "")
    }
}
