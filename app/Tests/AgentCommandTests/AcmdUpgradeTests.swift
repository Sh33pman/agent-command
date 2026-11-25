import Testing
@testable import AgentCommand

/// Programs run by an AcmdUpgrade, with what each one answers.
@MainActor
private final class FakeRun {
    var ran: [[String]] = []
    var envs: [[String: String]] = []
    var answers: [String: (Bool, String)] = [:]
    /// Holds brew's answer until let go, to see the upgrade in progress.
    var held: (() -> Void)?
    var hold = false

    func upgrade(acmd: String = DaemonPaths.brewAcmd, app: String = "0.94.0", brew: Bool = true) -> AcmdUpgrade {
        let u = AcmdUpgrade()
        u.acmd = acmd
        u.appVersion = app
        u.executable = { $0 == AcmdUpgrade.brew && brew }
        u.run = { path, args, env, done in
            self.ran.append([path] + args)
            self.envs.append(env)
            let (ok, output) = self.answers[path] ?? (true, "")
            if self.hold && path == AcmdUpgrade.brew {
                self.held = { done(ok, output) }
            } else {
                done(ok, output)
            }
        }
        return u
    }
}

private let older = [HealthItem(id: HealthWarnings.acmdOlder, title: "acmd is older than the app", detail: "acmd 0.93.1 is older.",
                                fix: .init(step: "Run brew upgrade acmd."))]

@MainActor @Test func anOlderAcmdIsUpgradedOnceALaunch() {
    let fake = FakeRun()
    fake.answers[DaemonPaths.brewAcmd] = (true, "agent-command 0.94.0 (commit abc)")
    let u = fake.upgrade()
    u.consider(older)
    #expect(fake.ran == [[AcmdUpgrade.brew, "upgrade", "brettmostert/tap/acmd"], [DaemonPaths.brewAcmd, "version"]])
    #expect(fake.envs[0]["PATH"]?.hasPrefix("/opt/homebrew/bin:") == true, "brew finds its own tools")
    #expect(u.state == .upgraded("0.94.0"))
    u.consider(older)
    #expect(fake.ran.count == 2, "not again this launch")
}

@MainActor @Test func nothingRunsWithoutTheWarningOrBrewsAcmd() {
    let fake = FakeRun()
    fake.upgrade().consider([HealthItem(id: "wake", title: "w")])
    fake.upgrade(acmd: "/Users/me/dev/acmd").consider(older)
    fake.upgrade(brew: false).consider(older)
    #expect(fake.ran.isEmpty)
}

@MainActor @Test func aFailedUpgradeSaysWhy() {
    let fake = FakeRun()
    fake.answers[AcmdUpgrade.brew] = (false, "==> Upgrading\nline 2\nline 3\nError: acmd is locked")
    let u = fake.upgrade()
    u.consider(older)
    #expect(u.state == .failed("line 2\nline 3\nError: acmd is locked"), "brew's last lines")
    let warning = HealthWarnings.warnings(older, upgrade: u.state) { _ in }[0]
    #expect(warning.detail == "acmd 0.93.1 is older.\n\nThe app couldn't upgrade it: line 2\nline 3\nError: acmd is locked")
    #expect(warning.fix == "Run brew upgrade acmd.", "the manual step stays")
}

@MainActor @Test func brewThatLeavesAcmdBehindIsAFailure() {
    let fake = FakeRun()
    fake.answers[DaemonPaths.brewAcmd] = (true, "agent-command 0.93.1 (commit abc)")
    let u = fake.upgrade(app: "0.94.0")
    u.consider(older)
    #expect(u.state == .failed("brew left acmd at 0.93.1, older than the app (0.94.0)."))
    let newer = fake.upgrade(app: "0.9.0")
    fake.answers[DaemonPaths.brewAcmd] = (true, "agent-command 0.10.0")
    newer.consider(older)
    #expect(newer.state == .upgraded("0.10.0"), "versions compare as numbers")
}

@MainActor @Test func theWarningHidesWhileBrewRuns() {
    let fake = FakeRun()
    fake.hold = true
    fake.answers[DaemonPaths.brewAcmd] = (true, "agent-command 0.94.0")
    let u = fake.upgrade()
    u.consider(older)
    #expect(u.state == .upgrading)
    #expect(HealthWarnings.warnings(older, upgrade: u.state) { _ in }.isEmpty)
    fake.held?()
    let warning = HealthWarnings.warnings(older, upgrade: u.state) { _ in }[0]
    #expect(warning.detail == "The app upgraded acmd to 0.94.0. The daemon restarts on it within a minute.")
}

@MainActor @Test func lastLinesHasAReasonEvenForNoOutput() {
    #expect(AcmdUpgrade.lastLines("") == "It gave no reason.")
    #expect(AcmdUpgrade.lastLines("a\nb", count: 1) == "b")
}
