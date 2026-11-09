import Testing

@testable import AgentCommand

@Test func remoteControlIsOffUnlessTurnedOn() {
    #expect(!RemoteControlSetting.isOn(inConfig: ""))
    #expect(!RemoteControlSetting.isOn(inConfig: "claude_command = \"claude\"\n"))
    #expect(!RemoteControlSetting.isOn(inConfig: "remote_control = false\n"))
    #expect(RemoteControlSetting.isOn(inConfig: "# comment\nremote_control = true\n"))
    #expect(RemoteControlSetting.isOn(inConfig: "  remote_control=true # mine\n[speech]\n"))
    #expect(!RemoteControlSetting.isOn(inConfig: "# remote_control = true\n"))
    #expect(!RemoteControlSetting.isOn(inConfig: "[other]\nremote_control = true\n"), "only the top level counts")
}

@Test func remoteControlPaletteSaysWhatItDoes() {
    #expect(RemoteControlSetting.paletteTitle(isOn: false) == "Turn Remote Control on for new sessions")
    #expect(RemoteControlSetting.paletteTitle(isOn: true) == "Turn Remote Control off for new sessions")
    let command = PopupCommand(section: "Claude", title: RemoteControlSetting.paletteTitle(isOn: false)) {}
    #expect(command.matches("remote") && command.matches("claude"), "found by typing remote or claude")
}

@Test func remoteControlArguments() {
    #expect(RemoteControlSetting.arguments(on: true) == ["remote-control", "on"])
    #expect(RemoteControlSetting.arguments(on: false) == ["remote-control", "off"])
}
