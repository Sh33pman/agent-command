import Foundation
import Testing
@testable import AgentCommand

struct DefaultHarnessTests {
    @Test func readsTheSetting() {
        #expect(DefaultHarness.value("") == nil, "unset: ⌘N asks")
        #expect(DefaultHarness.value("antigravity") == .antigravity)
        #expect(DefaultHarness.value("claude") == .claude)
        #expect(DefaultHarness.value("terminal") == nil, "not an agent")
        #expect(DefaultHarness.value("copilot") == nil)
    }

    @Test func menuTitlesAndChanges() {
        #expect(DefaultHarness.menuTitle(.claude) == "New Claude Session")
        #expect(DefaultHarness.menuTitle(.antigravity) == "New Antigravity Session")
        #expect(DefaultHarness.menuTitle(nil) == "New Session (Pick a Default)…")
        #expect(DefaultHarness.change(.antigravity) == ["default_harness": .string("antigravity")])
        #expect(DefaultHarness.change(nil) == ["default_harness": .string("")])
    }

    @Test func onlyAnAgentBecomesTheDefault() {
        #expect(HarnessPickerView.makesDefault(.antigravity, checked: true))
        #expect(HarnessPickerView.makesDefault(.claude, checked: true))
        #expect(!HarnessPickerView.makesDefault(.terminal, checked: true))
        #expect(!HarnessPickerView.makesDefault(.claude, checked: false))
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
