import AppKit
import Observation

/// acmd's settings, from config.toml, as `acmd config show --json` gives them.
struct AcmdSettings: Decodable, Equatable {
    struct Values: Decodable, Equatable {
        var claudeCommand = "claude"
        var claudeArgs: [String] = []
        var remoteControl = false
        var notify = true
        var onWaiting = true
        var onError = true
        var onDone = true
        var onPaused = false
        var doneMin = "10s"
        var cooldown = "20s"
        var sound = "default"
        var voice = "af_heart:0.6,af_bella:0.4"
        var speed = 1.0
        var editorPreset = ""
        var editorCommand: [String] = []
        var editorRunIn = "background"
        var editorTarget = "cwd"

        enum CodingKeys: String, CodingKey {
            case claudeCommand = "claude_command", claudeArgs = "claude_args", remoteControl = "remote_control"
            case notify = "notify.enabled", onWaiting = "notify.on_waiting", onError = "notify.on_error"
            case onDone = "notify.on_done", onPaused = "notify.on_paused", doneMin = "notify.done_min_duration"
            case cooldown = "notify.cooldown", sound = "notify.sound", voice = "speech.voice", speed = "speech.speed"
            case editorPreset = "editor.preset", editorCommand = "editor.command", editorRunIn = "editor.run_in"
            case editorTarget = "editor.target"
        }

        init() {}

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            let d = Values()
            claudeCommand = try c.decodeIfPresent(String.self, forKey: .claudeCommand) ?? d.claudeCommand
            claudeArgs = try c.decodeIfPresent([String].self, forKey: .claudeArgs) ?? d.claudeArgs
            remoteControl = try c.decodeIfPresent(Bool.self, forKey: .remoteControl) ?? d.remoteControl
            notify = try c.decodeIfPresent(Bool.self, forKey: .notify) ?? d.notify
            onWaiting = try c.decodeIfPresent(Bool.self, forKey: .onWaiting) ?? d.onWaiting
            onError = try c.decodeIfPresent(Bool.self, forKey: .onError) ?? d.onError
            onDone = try c.decodeIfPresent(Bool.self, forKey: .onDone) ?? d.onDone
            onPaused = try c.decodeIfPresent(Bool.self, forKey: .onPaused) ?? d.onPaused
            doneMin = try c.decodeIfPresent(String.self, forKey: .doneMin) ?? d.doneMin
            cooldown = try c.decodeIfPresent(String.self, forKey: .cooldown) ?? d.cooldown
            sound = try c.decodeIfPresent(String.self, forKey: .sound) ?? d.sound
            voice = try c.decodeIfPresent(String.self, forKey: .voice) ?? d.voice
            speed = try c.decodeIfPresent(Double.self, forKey: .speed) ?? d.speed
            editorPreset = try c.decodeIfPresent(String.self, forKey: .editorPreset) ?? d.editorPreset
            editorCommand = try c.decodeIfPresent([String].self, forKey: .editorCommand) ?? d.editorCommand
            editorRunIn = try c.decodeIfPresent(String.self, forKey: .editorRunIn) ?? d.editorRunIn
            editorTarget = try c.decodeIfPresent(String.self, forKey: .editorTarget) ?? d.editorTarget
        }
    }

    var path = ""
    var settings = Values()
    var voices: [String] = []
}

/// Lengths of time as Go writes them: "10s", "1m30s", "2h0m0s".
enum GoDuration {
    static func seconds(_ text: String) -> Double? {
        guard !text.isEmpty else { return nil }
        var total = 0.0
        var number = ""
        var units = Substring(text)
        while let c = units.first {
            if c.isNumber || c == "." {
                number.append(c)
                units = units.dropFirst()
                continue
            }
            let unit: (String, Double)? = [("ms", 0.001), ("h", 3600), ("m", 60), ("s", 1)].first { units.hasPrefix($0.0) }
            guard let unit, let value = Double(number) else { return nil }
            total += value * unit.1
            units = units.dropFirst(unit.0.count)
            number = ""
        }
        return number.isEmpty ? total : nil
    }

    /// "Off", "10 seconds", "1 minute", "1 min 30 s".
    static func label(_ seconds: Double) -> String {
        let s = Int(seconds.rounded())
        switch s {
        case 0: return "No wait"
        case 1..<60: return "\(s) seconds"
        case 60: return "1 minute"
        default:
            return s % 60 == 0 ? "\(s / 60) minutes" : "\(s / 60) min \(s % 60) s"
        }
    }
}

/// Arguments as you'd type them: words, with quotes around ones that have spaces.
enum ShellWords {
    static func split(_ text: String) -> [String] {
        var words: [String] = []
        var word = ""
        var quote: Character?
        var hasWord = false
        for c in text {
            if let q = quote {
                if c == q { quote = nil } else { word.append(c) }
            } else if c == "\"" || c == "'" {
                quote = c
                hasWord = true
            } else if c.isWhitespace {
                if hasWord { words.append(word) }
                word = ""
                hasWord = false
            } else {
                word.append(c)
                hasWord = true
            }
        }
        if hasWord { words.append(word) }
        return words
    }

    static func join(_ words: [String]) -> String {
        words.map { $0.isEmpty || $0.contains(where: \.isWhitespace) ? "\"\($0)\"" : $0 }.joined(separator: " ")
    }
}

/// The Settings window's state. acmd's settings go through `acmd config set`, so the
/// app, the command line and the daemon agree; the app's own are stored as before.
@Observable final class SettingsModel {
    private(set) var acmd = AcmdSettings()
    private(set) var loaded = false
    /// What went wrong last, said in the window.
    private(set) var error: String?
    /// What a command that finished said, such as acmd doctor's report.
    private(set) var report: String?
    /// The setting being changed, so its control can wait.
    private(set) var busy: String?

    /// Runs acmd; tests swap it.
    @ObservationIgnored var runAcmd: (_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) -> Void = Acmd.run
    /// The app's Ghostty config: read, written, and applied once written.
    @ObservationIgnored var readGhostty: () -> String = {
        (try? String(contentsOfFile: AppFont.configPath, encoding: .utf8)) ?? ""
    }
    @ObservationIgnored var writeGhostty: (String) throws -> Void = { text in
        let path = AppFont.configPath
        try FileManager.default.createDirectory(
            atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
        try text.write(toFile: path, atomically: true, encoding: .utf8)
    }
    @ObservationIgnored var ghosttyChanged: () -> Void = {}
    /// A new voice: speech downloads it, if it's on.
    @ObservationIgnored var voiceChanged: () -> Void = {}

    static let voiceSample = "Hello. This is how I'll read your messages to you."
    static let waits: [Double] = [0, 5, 10, 30, 60, 120, 300]

    var values: AcmdSettings.Values { acmd.settings }

    func load() {
        runAcmd(["config", "show", "--json"]) { [weak self] ok, output in
            guard let self else { return }
            guard ok, let data = output.data(using: .utf8),
                  let decoded = try? JSONDecoder().decode(AcmdSettings.self, from: data)
            else {
                self.error = output.isEmpty ? "Couldn't read acmd's settings." : output
                return
            }
            self.acmd = decoded
            self.loaded = true
        }
    }

    /// Sets one of acmd's settings, then reads them all back.
    func set(_ key: String, _ value: String, then: @escaping () -> Void = {}) {
        command(["config", "set", "--", key, value], key: key, then: then)
    }

    func set(_ key: String, _ on: Bool) {
        set(key, on ? "on" : "off")
    }

    func setArgs(_ text: String) {
        let words = ShellWords.split(text)
        let json = (try? JSONEncoder().encode(words)).map { String(decoding: $0, as: UTF8.self) } ?? "[]"
        set("claude_args", json)
    }

    func setVoice(_ voice: String) {
        set("speech.voice", voice) { [weak self] in self?.voiceChanged() }
    }

    /// The choices for a wait: the usual ones, and the one set if it's another.
    func waitChoices(current: String) -> [Double] {
        var list = Self.waits
        if let now = GoDuration.seconds(current), !list.contains(now) { list.append(now) }
        return list.sorted()
    }

    /// vscode, cursor, zed or idea, or custom with its command.
    func setEditor(_ preset: String, command words: [String] = []) {
        let args = preset == "custom" ? ["editor", "set", "custom", "--"] + words : ["editor", "set", preset]
        command(args, key: "editor.preset")
    }

    func playSample() {
        runAcmd(["speech", "say", Self.voiceSample]) { [weak self] ok, output in
            if !ok { self?.error = output.isEmpty ? "Couldn't read the sample." : output }
        }
    }

    /// Re-adds the Claude Code hooks and replaces a stale background service.
    func repairSetup() {
        command(["doctor", "--fix"], key: "doctor", keepsOutput: true)
    }

    func checkSetup() {
        command(["doctor"], key: "doctor", keepsOutput: true)
    }

    private func command(_ args: [String], key: String, keepsOutput: Bool = false, then: @escaping () -> Void = {}) {
        busy = key
        error = nil
        runAcmd(args) { [weak self] ok, output in
            guard let self else { return }
            self.busy = nil
            if keepsOutput { self.report = output }
            if ok {
                then()
            } else if !keepsOutput {
                self.error = output.isEmpty ? "Couldn't change \(key)." : output
            }
            self.load()
        }
    }

    // MARK: Waking the Mac

    func setWake(_ on: Bool) {
        command(["schedule", "wake", on ? "on" : "off"], key: "wake")
    }

    func setWakeAll(_ all: Bool) {
        command(["schedule", "wake", "on", "--all=\(all)"], key: "wake")
    }

    // MARK: The font, in the app's Ghostty config

    var fontFamily: String {
        AppFont.firstFontFamily(inConfig: readGhostty()) ?? AppFont.defaultFamily
    }

    var fontSize: Double {
        ThemeConfig.value("font-size", inConfig: readGhostty()).flatMap(Double.init) ?? 13
    }

    /// The fallback families stay: Ghostty reads later font-family lines as those.
    func setFontFamily(_ family: String?) {
        writeGhostty { ThemeConfig.setting("font-family", to: family.map { "\"\($0)\"" }, inConfig: $0, keepingOthers: true) }
    }

    func setFontSize(_ size: Double?) {
        let value = size.map { $0.rounded() == $0 ? String(Int($0)) : String($0) }
        writeGhostty { ThemeConfig.setting("font-size", to: value, inConfig: $0) }
    }

    private func writeGhostty(_ change: (String) -> String) {
        do {
            try writeGhostty(change(readGhostty()))
            ghosttyChanged()
        } catch {
            self.error = "Couldn't save the font: \(error.localizedDescription)"
        }
    }

    /// The monospaced families installed, for the terminal.
    static var monospacedFamilies: [String] {
        let names = NSFontManager.shared.availableFontNames(with: .fixedPitchFontMask) ?? []
        let families = Set(names.compactMap { NSFont(name: $0, size: 12)?.familyName })
        return families.union([AppFont.defaultFamily]).sorted { $0.localizedCaseInsensitiveCompare($1) == .orderedAscending }
    }
}
