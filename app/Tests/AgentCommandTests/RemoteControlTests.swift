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

@Test func remoteControlArguments() {
    #expect(RemoteControlSetting.arguments(on: true) == ["remote-control", "on"])
    #expect(RemoteControlSetting.arguments(on: false) == ["remote-control", "off"])
}
