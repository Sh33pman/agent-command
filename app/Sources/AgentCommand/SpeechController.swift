import Foundation
import Observation

/// Reads messages aloud with `agent-command speech say`, one at a time. Ending the
/// process stops the audio.
@Observable
final class SpeechController {
    static let shared = SpeechController()

    /// The message being read.
    private(set) var speakingItem: Int64?
    /// Why the last attempt couldn't be read.
    private(set) var failure: String?

    /// Whether speech is on. Off, the app has no speech buttons and downloads nothing.
    private(set) var isEnabled = false
    /// Whether the model is here and ready.
    private(set) var isInstalled = false
    /// Whether the model is being downloaded and set up.
    var isSettingUp: Bool { setupProcess != nil }
    /// Whether that replaces an older engine, rather than being a first install.
    private(set) var isUpgrading = false
    /// How much of the model has downloaded, from 0 to 1, once that's known.
    private(set) var setupProgress: Double?
    /// Why that failed. It tries again on the next launch, or when asked.
    private var setupProcess: Process?
    private(set) var setupFailure: String?

    @ObservationIgnored private var process: Process?
    private static let enabledKey = "speech.enabled"

    var isSpeaking: Bool { speakingItem != nil }

    private init() {}

    /// Starts reading a message, or stops if it's the one being read.
    func toggle(_ item: InboxItem) {
        if speakingItem == item.id { stop() } else { speak(item) }
    }

    func speak(_ item: InboxItem) {
        stop()
        guard isEnabled else {
            failure = "Speech is off. Turn it on from the bell in the status bar."
            return
        }
        if isSettingUp {
            failure = "Speech is still being set up. It'll be ready soon."
            return
        }
        let process = Process()
        // Through env: a development build finds the binary on the PATH.
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [DaemonPaths.acmd, "speech", "say", "-"]
        let input = Pipe()
        let errors = Pipe()
        process.standardInput = input
        process.standardOutput = FileHandle.nullDevice
        process.standardError = errors
        process.terminationHandler = { [weak self] ended in
            let output = errors.fileHandleForReading.readDataToEndOfFile()
            DispatchQueue.main.async {
                guard let self, self.process === ended else { return }
                self.process = nil
                self.speakingItem = nil
                if ended.terminationStatus != 0 { self.failure = Self.reason(from: output) }
            }
        }
        do {
            try process.run()
        } catch {
            failure = "Couldn't start speech: \(error.localizedDescription)"
            return
        }
        self.process = process
        speakingItem = item.id
        // A long message could fill the pipe. If the process has gone, the write fails
        // rather than ending the app.
        let writer = input.fileHandleForWriting
        _ = fcntl(writer.fileDescriptor, F_SETNOSIGPIPE, 1)
        let text = Data(item.text.utf8)
        DispatchQueue.global(qos: .userInitiated).async {
            try? writer.write(contentsOf: text)
            try? writer.close()
        }
    }

    func stop() {
        failure = nil
        speakingItem = nil
        process?.terminate()
        process = nil
    }

    /// On launch: finds out how speech is set up. Someone who has it, even with an older
    /// engine, keeps it. Someone who never set it up has it off, with nothing downloaded.
    func checkSetup() {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [DaemonPaths.acmd, "speech", "status"]
        let output = Pipe()
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        process.terminationHandler = { [weak self] _ in
            let text = String(decoding: output.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            let status = text.split(whereSeparator: \.isNewline).first.map(String.init) ?? ""
            DispatchQueue.main.async { self?.apply(status: status) }
        }
        try? process.run()
    }

    private func apply(status: String) {
        let ready = status == "Speech: set up"
        let older = status.hasPrefix("Speech: set up for an older")
        let chosen = UserDefaults.standard.object(forKey: Self.enabledKey) as? Bool
        isEnabled = chosen ?? (ready || older)
        isInstalled = ready
        if isEnabled && !ready { install(upgrading: older) }
    }

    /// Turning it on downloads the model if it isn't here. Turning it off keeps the model.
    func setEnabled(_ enabled: Bool) {
        isEnabled = enabled
        UserDefaults.standard.set(enabled, forKey: Self.enabledKey)
        if enabled {
            if !isInstalled { install(upgrading: false) }
        } else {
            stop()
            setupProcess?.terminate()
            setupProcess = nil
            setupFailure = nil
        }
    }

    func retrySetup() {
        install(upgrading: false)
    }

    /// Another voice was chosen: it needs downloading, if speech is on.
    func voiceChanged() {
        isInstalled = false
        if isEnabled { install(upgrading: false) }
    }

    private func install(upgrading: Bool) {
        guard setupProcess == nil else { return }
        setupFailure = nil
        setupProgress = nil
        isUpgrading = upgrading
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [DaemonPaths.acmd, "speech", "install", "--yes", "--progress"]
        let output = Pipe()
        process.standardInput = FileHandle.nullDevice
        process.standardOutput = output
        process.standardError = output
        var written = Data()
        output.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            DispatchQueue.main.async {
                written.append(data)
                let lines = String(decoding: data, as: UTF8.self).split(whereSeparator: \.isNewline)
                if let last = lines.last(where: { $0.hasPrefix("progress ") }), let percent = Double(last.dropFirst(9)) {
                    self?.setupProgress = percent / 100
                }
            }
        }
        process.terminationHandler = { [weak self] ended in
            output.fileHandleForReading.readabilityHandler = nil
            // It can end before its last words have been read, and those say why.
            let rest = output.fileHandleForReading.readDataToEndOfFile()
            DispatchQueue.main.async {
                guard let self, self.setupProcess === ended else { return }
                self.setupProcess = nil
                self.setupProgress = nil
                if ended.terminationStatus == 0 {
                    self.isInstalled = true
                } else {
                    self.setupFailure = Self.reason(from: written + rest)
                }
            }
        }
        do {
            try process.run()
            setupProcess = process
        } catch {
            setupFailure = error.localizedDescription
        }
    }

    /// Reports something that stopped a message being read.
    func fail(_ reason: String) {
        failure = reason
    }

    /// The last line the command wrote, which says what went wrong.
    private static func reason(from output: Data) -> String {
        let lines = String(decoding: output, as: UTF8.self).split(whereSeparator: \.isNewline)
        guard let last = lines.last else { return "Speech stopped unexpectedly." }
        return last.replacingOccurrences(of: "^error: ", with: "", options: [.regularExpression, .caseInsensitive])
    }
}
