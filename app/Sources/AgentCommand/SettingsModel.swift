import AppKit
import Observation

/// acmd's settings, as config.get gives them: their values, typed, and what's wrong
/// with config.toml if anything is.
struct AcmdSettings: Equatable {
    struct Values: Decodable, Equatable {
        var claudeCommand = "claude"
        var claudeArgs: [String] = []
        var agyCommand = "agy"
        var agyArgs: [String] = []
        var agentRemoteControl = false
        var defaultHarness = ""
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
        var newSessionsRemote = false
        var hostEnabled = false
        var hostListen = "localhost"
        var hostPort = 7433
        var hostPortFallback = true
        var hostCert = ""
        var clientCert = ""

        enum CodingKeys: String, CodingKey {
            case claudeCommand = "claude_command", claudeArgs = "claude_args", agentRemoteControl = "agent_remote_control"
            case agyCommand = "agy_command", agyArgs = "agy_args", defaultHarness = "default_harness"
            case notify = "notify.enabled", onWaiting = "notify.on_waiting", onError = "notify.on_error"
            case onDone = "notify.on_done", onPaused = "notify.on_paused", doneMin = "notify.done_min_duration"
            case cooldown = "notify.cooldown", sound = "notify.sound", voice = "speech.voice", speed = "speech.speed"
            case editorPreset = "editor.preset", editorCommand = "editor.command", editorRunIn = "editor.run_in"
            case editorTarget = "editor.target"
            case newSessionsRemote = "remote.new_sessions_remote", hostEnabled = "remote.host.enabled"
            case hostListen = "remote.host.listen", hostPort = "remote.host.port", hostPortFallback = "remote.host.port_fallback"
            case hostCert = "remote.host.cert", clientCert = "remote.client.cert"
        }

        init() {}

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            let d = Values()
            claudeCommand = try c.decodeIfPresent(String.self, forKey: .claudeCommand) ?? d.claudeCommand
            claudeArgs = try c.decodeIfPresent([String].self, forKey: .claudeArgs) ?? d.claudeArgs
            agyCommand = try c.decodeIfPresent(String.self, forKey: .agyCommand) ?? d.agyCommand
            agyArgs = try c.decodeIfPresent([String].self, forKey: .agyArgs) ?? d.agyArgs
            agentRemoteControl = try c.decodeIfPresent(Bool.self, forKey: .agentRemoteControl) ?? d.agentRemoteControl
            defaultHarness = try c.decodeIfPresent(String.self, forKey: .defaultHarness) ?? d.defaultHarness
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
            newSessionsRemote = try c.decodeIfPresent(Bool.self, forKey: .newSessionsRemote) ?? d.newSessionsRemote
            hostEnabled = try c.decodeIfPresent(Bool.self, forKey: .hostEnabled) ?? d.hostEnabled
            hostListen = try c.decodeIfPresent(String.self, forKey: .hostListen) ?? d.hostListen
            hostPort = try c.decodeIfPresent(Int.self, forKey: .hostPort) ?? d.hostPort
            hostPortFallback = try c.decodeIfPresent(Bool.self, forKey: .hostPortFallback) ?? d.hostPortFallback
            hostCert = try c.decodeIfPresent(String.self, forKey: .hostCert) ?? d.hostCert
            clientCert = try c.decodeIfPresent(String.self, forKey: .clientCert) ?? d.clientCert
        }
    }

    var path = ""
    var settings = Values()
    /// Each setting's version as loaded, to send back with a change.
    var versions: [String: String] = [:]
    var voices: [String] = []
    var problem: ConfigProblem?

    init() {}

    init(state: ConfigState) {
        path = state.path
        versions = state.versions
        let json = (try? JSONEncoder().encode(state.values)) ?? Data("{}".utf8)
        settings = (try? JSONDecoder().decode(Values.self, from: json)) ?? Values()
        voices = state.settings.first { $0.key == "speech.voice" }?.suggestions ?? []
        problem = state.problem
    }
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

/// The Settings window's state. acmd's settings go through the daemon (config.get,
/// config.set), so the app, the command line and the daemon agree; the app's own are
/// stored as before.
@Observable final class SettingsModel {
    private(set) var acmd = AcmdSettings()
    private(set) var loaded = false
    /// What went wrong last, said in the window.
    private(set) var error: String?
    /// What a command that finished said, such as acmd doctor's report.
    private(set) var report: String?
    /// The setting being changed, so its control can wait.
    private(set) var busy: String?
    /// A change refused because the settings changed since they were shown
    /// (by hand, or from another device): the settings show as they are now,
    /// and useMine makes this change over them.
    private(set) var refused: [String: SettingValue]?
    /// The section on show.
    var section = SettingsSection.general {
        didSet { if Self.hostSections.contains(section) != Self.hostSections.contains(oldValue) && host != nil { load() } }
    }
    /// The Remote host whose settings show, in the sections a host has: nil is this Mac.
    var host: String? {
        didSet {
            guard host != oldValue else { return }
            error = nil
            load()
        }
    }

    /// The sections that show a host's settings when one is chosen. The rest are this Mac's.
    static let hostSections: Set<SettingsSection> = [.claude, .antigravity, .notifications]

    /// Whose settings the page on show changes: nil is this Mac.
    var target: String? { Self.hostSections.contains(section) ? host : nil }

    /// The daemon's settings ops; tests swap its calls.
    @ObservationIgnored var client = SettingsClient()
    /// Runs acmd, for what isn't an op yet (doctor, wake); tests swap it.
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
        let whose = target
        client.get(host: whose) { [weak self] result in
            guard let self, whose == self.target else { return } // another host was chosen meanwhile
            switch result {
            case .success(let state):
                self.acmd = AcmdSettings(state: state)
                self.loaded = true
            case .failure(let failure):
                self.error = whose.map { "Couldn't read \($0)'s settings: \(errorText(failure))" } ?? "Couldn't read acmd's settings: \(errorText(failure))"
            }
        }
    }

    /// Changes settings, all or none, then shows them as they are.
    func set(_ values: [String: SettingValue], then: @escaping () -> Void = {}) {
        guard let key = values.keys.sorted().first else { return }
        busy = key
        error = nil
        let whose = target
        refused = nil
        let versions = acmd.versions.filter { values[$0.key] != nil }
        client.set(values, host: whose, versions: versions) { [weak self] result in
            guard let self else { return }
            self.busy = nil
            switch result {
            case .success(let state):
                if whose == self.target { self.acmd = AcmdSettings(state: state) }
                if whose == nil { HostSettings.shared.values = state.values }
                then()
            case .failure(let failure as DaemonError) where failure.code == "conflict":
                let names = values.keys.sorted().joined(separator: ", ")
                self.error = "\(names) changed while Settings was open, by hand or from another device. "
                    + "It shows as it is now: Use Mine to make your change anyway."
                self.refused = values
                self.load()
            case .failure(let failure):
                let why = errorText(failure)
                self.error = why.isEmpty ? "Couldn't change \(key)." : why
                self.load()
            }
        }
    }

    /// Makes the refused change over the settings as they are now.
    func useMine() {
        guard let values = refused else { return }
        set(values)
    }

    /// Sets one setting from text as it's typed ("on", "10s", "7500").
    func set(_ key: String, _ value: String, then: @escaping () -> Void = {}) {
        set([key: .string(value)], then: then)
    }

    /// Sets several at once: all of them, or none.
    func setAll(_ changes: [(String, String)]) {
        guard !changes.isEmpty else { return }
        set(Dictionary(changes.map { ($0.0, SettingValue.string($0.1)) }, uniquingKeysWith: { _, last in last }))
    }

    func set(_ key: String, _ on: Bool) {
        set([key: .bool(on)])
    }

    /// Sets an agent's extra arguments (claude_args, agy_args) from shell words.
    func setArgs(_ text: String, key: String = "claude_args") {
        set([key: .list(ShellWords.split(text))])
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

    /// vscode, cursor, zed or idea, or custom with its command. The daemon finds it,
    /// and says if it isn't installed.
    func setEditor(_ preset: String, command words: [String] = []) {
        set(["editor.preset": .string(preset), "editor.command": .list(preset == "custom" ? words : []),
             "editor.run_in": .string("background")])
    }

    /// Reads the sample aloud, through the daemon.
    func playSample() {
        let data = (try? JSONEncoder().encode(["text": Self.voiceSample])) ?? Data()
        client.calls.call("speech.say", data) { [weak self] result in
            if case .failure(let failure) = result {
                let why = errorText(failure)
                self?.error = why.isEmpty ? "Couldn't read the sample." : why
            }
        }
    }

    /// Re-adds the agents' hooks and the rest of acmd's setup (the init op),
    /// then checks it again.
    func repairSetup() {
        busy = "doctor"
        client.calls.call("init", Data("{}".utf8)) { [weak self] result in
            guard let self else { return }
            if case .failure(let failure) = result {
                self.busy = nil
                self.report = "Couldn't set acmd up: \(errorText(failure))"
                return
            }
            self.checkSetup()
        }
    }

    /// The checks acmd doctor makes, by acmd's background service.
    func checkSetup() {
        busy = "doctor"
        error = nil
        client.calls.call("doctor", Data("{}".utf8)) { [weak self] result in
            guard let self else { return }
            self.busy = nil
            switch result {
            case .success(let data):
                self.report = ((try? JSONDecoder().decode(DoctorReport.self, from: data)) ?? DoctorReport()).text
            case .failure(let failure):
                self.report = "Couldn't check the setup: \(errorText(failure))"
            }
            self.load()
        }
    }

    private func command(_ args: [String], key: String, keepsOutput: Bool = false) {
        busy = key
        error = nil
        runAcmd(args) { [weak self] ok, output in
            guard let self else { return }
            self.busy = nil
            if keepsOutput { self.report = output }
            if !ok && !keepsOutput {
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

/// Whether the app's icon shows in the menu bar, with its places and Settings. On
/// by default.
@Observable final class MenuBarPreference {
    static let shared = MenuBarPreference(defaults: .standard)
    static let key = "menuBar.shown"

    var shown: Bool {
        didSet {
            defaults.set(shown, forKey: Self.key)
            if shown != oldValue { onChange() }
        }
    }

    /// Told when it's turned on or off: the app adds or removes the icon.
    @ObservationIgnored var onChange: () -> Void = {}
    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults) {
        self.defaults = defaults
        shown = defaults.object(forKey: Self.key) as? Bool ?? true
    }
}
