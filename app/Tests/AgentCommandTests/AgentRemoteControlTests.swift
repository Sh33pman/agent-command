import Testing

@testable import AgentCommand

@Test func agentRemoteControlIsOffUnlessTurnedOn() {
    #expect(!AgentRemoteControlSetting.isOn(inConfig: ""))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "claude_command = \"claude\"\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "agent_remote_control = false\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "# comment\nagent_remote_control = true\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "  agent_remote_control=true # mine\n[speech]\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "# agent_remote_control = true\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "[other]\nagent_remote_control = true\n"), "only the top level counts")
}

@Test func agentRemoteControlReadsTheOldKey() {
    #expect(AgentRemoteControlSetting.isOn(inConfig: "# comment\nremote_control = true\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "  remote_control=true # mine\n[speech]\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "remote_control = false\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "# remote_control = true\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "[other]\nremote_control = true\n"), "only the top level counts")
}

@Test func agentRemoteControlNewKeyWins() {
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "remote_control = true\nagent_remote_control = false\n"))
    #expect(!AgentRemoteControlSetting.isOn(inConfig: "agent_remote_control = false\nremote_control = true\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "remote_control = false\nagent_remote_control = true\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "agent_remote_control = true\nremote_control = false\n"))
    #expect(AgentRemoteControlSetting.isOn(inConfig: "remote_control = true\n[x]\nagent_remote_control = false\n"),
            "a key in a section isn't the setting")
}

@Test func agentRemoteControlPaletteSaysWhatItDoes() {
    #expect(AgentRemoteControlSetting.paletteTitle(isOn: false) == "Turn Agent Remote Control on for new sessions")
    #expect(AgentRemoteControlSetting.paletteTitle(isOn: true) == "Turn Agent Remote Control off for new sessions")
    let command = PopupCommand(section: "Claude", title: AgentRemoteControlSetting.paletteTitle(isOn: false)) {}
    #expect(command.matches("remote") && command.matches("agent") && command.matches("claude"),
            "found by typing remote, agent or claude")
}

@Test func agentRemoteControlArguments() {
    #expect(AgentRemoteControlSetting.arguments(on: true) == ["agent-remote-control", "on"])
    #expect(AgentRemoteControlSetting.arguments(on: false) == ["agent-remote-control", "off"])
}
