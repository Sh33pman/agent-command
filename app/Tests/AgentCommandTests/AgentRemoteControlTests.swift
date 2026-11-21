import Testing

@testable import AgentCommand

@Test func agentRemoteControlPaletteSaysWhatItDoes() {
    #expect(AgentRemoteControlSetting.paletteTitle(isOn: false) == "Turn Agent Remote Control on for new sessions")
    #expect(AgentRemoteControlSetting.paletteTitle(isOn: true) == "Turn Agent Remote Control off for new sessions")
    let command = PopupCommand(section: "Claude", title: AgentRemoteControlSetting.paletteTitle(isOn: false)) {}
    #expect(command.matches("remote") && command.matches("agent") && command.matches("claude"),
            "found by typing remote, agent or claude")
}

@Test func agentRemoteControlChange() {
    #expect(AgentRemoteControlSetting.change(on: true) == ["agent_remote_control": .bool(true)])
    #expect(AgentRemoteControlSetting.change(on: false) == ["agent_remote_control": .bool(false)])
}
