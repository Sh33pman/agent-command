import Foundation
import Testing
@testable import AgentCommand

struct DefaultHarnessTests {
    @Test func readsTheTopLevelKey() {
        #expect(DefaultHarness.value(inConfig: "") == nil, "unset: ⌘N asks")
        #expect(DefaultHarness.value(inConfig: "default_harness = \"\"\n") == nil)
        #expect(DefaultHarness.value(inConfig: "claude_args = []\ndefault_harness = \"antigravity\"\n") == .antigravity)
        #expect(DefaultHarness.value(inConfig: "default_harness = \"claude\" # mine\n") == .claude)
        #expect(DefaultHarness.value(inConfig: "default_harness = claude\n") == .claude)
        #expect(DefaultHarness.value(inConfig: "default_harness = \"terminal\"\n") == nil, "not an agent")
        #expect(DefaultHarness.value(inConfig: "default_harness = \"copilot\"\n") == nil)
        #expect(DefaultHarness.value(inConfig: "[editor]\ndefault_harness = \"claude\"\n") == nil, "not top level")
        #expect(DefaultHarness.value(inConfig: "# What a new session runs (⌘N...): claude or\ndefault_harness = \"\"\n") == nil, "as the template has it")
    }

    @Test func menuTitlesAndArguments() {
        #expect(DefaultHarness.menuTitle(.claude) == "New Claude Session")
        #expect(DefaultHarness.menuTitle(.antigravity) == "New Antigravity Session")
        #expect(DefaultHarness.menuTitle(nil) == "New Session (Pick a Default)…")
        #expect(DefaultHarness.arguments(.antigravity) == ["config", "set", "--", "default_harness", "antigravity"])
        #expect(DefaultHarness.arguments(nil) == ["config", "set", "--", "default_harness", ""])
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
