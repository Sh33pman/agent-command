import Foundation
import Observation

/// Upgrades Homebrew's acmd when the daemon says it's older than the app.
/// Homebrew never upgrades a cask's formula, so `brew upgrade agent-command`
/// leaves acmd as it was: the app does it when it next opens. The daemon then
/// restarts on the new acmd by itself.
@Observable
final class AcmdUpgrade {
    static let shared = AcmdUpgrade()

    static let brew = "/opt/homebrew/bin/brew"
    static let formula = "brettmostert/tap/acmd"

    nonisolated enum State: Equatable, Sendable {
        case idle
        case upgrading
        /// Brew installed this version. The daemon restarts on it within a minute.
        case upgraded(String)
        /// What went wrong, as brew or acmd said it.
        case failed(String)
    }

    private(set) var state = State.idle

    /// Runs a program and says whether it worked, with its output.
    @ObservationIgnored var run: (_ path: String, _ args: [String], _ env: [String: String],
                                  _ done: @escaping @MainActor (Bool, String) -> Void) -> Void = { path, args, env, done in
        Command.run(path, args, env: env, done)
    }
    /// The acmd the app runs. Only Homebrew's is upgraded.
    @ObservationIgnored var acmd = DaemonPaths.acmd
    @ObservationIgnored var appVersion = DaemonConnection.appVersion
    @ObservationIgnored var executable: (String) -> Bool = { FileManager.default.isExecutableFile(atPath: $0) }

    /// Upgrades acmd, once a launch, when health says it's older than the app.
    func consider(_ health: [HealthItem]) {
        guard state == .idle, health.contains(where: { $0.id == HealthWarnings.acmdOlder }),
              acmd == DaemonPaths.brewAcmd, executable(Self.brew) else { return }
        state = .upgrading
        // The app's PATH is the system's: brew needs its own folder on it.
        let env = ["PATH": "/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin", "HOMEBREW_NO_ENV_HINTS": "1"]
        run(Self.brew, ["upgrade", Self.formula], env) { ok, output in
            guard ok else { return self.state = .failed(Self.lastLines(output)) }
            self.checkVersion()
        }
    }

    /// Brew succeeds when acmd is already its newest, so check acmd caught up.
    private func checkVersion() {
        run(acmd, ["version"], [:]) { ok, output in
            let words = output.split(separator: " ")
            guard ok, words.count > 1 else { return self.state = .failed(Self.lastLines(output)) }
            let version = String(words[1])
            if version.compare(self.appVersion, options: .numeric) == .orderedAscending {
                self.state = .failed("brew left acmd at \(version), older than the app (\(self.appVersion)).")
            } else {
                self.state = .upgraded(version)
            }
        }
    }

    /// The end of what a program said: brew's error is last.
    static func lastLines(_ output: String, count: Int = 3) -> String {
        let lines = output.split(separator: "\n").suffix(count)
        return lines.isEmpty ? "It gave no reason." : lines.joined(separator: "\n")
    }
}
