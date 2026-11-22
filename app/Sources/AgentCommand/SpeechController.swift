import Foundation
import Observation

/// How speech stands on this Mac, as the daemon says (internal/ipc.SpeechState).
struct SpeechState: Decodable, Equatable {
    var supported = false
    var enabled = false
    var installed = false
    var older = false
    var installing = false
    var progress: Int?
    var failure: String?
    var speaking = false
    var voice = ""

    enum CodingKeys: String, CodingKey { case supported, enabled, installed, older, installing, progress, failure, speaking, voice }

    init(supported: Bool = false, enabled: Bool = false, installed: Bool = false, older: Bool = false, installing: Bool = false,
         progress: Int? = nil, failure: String? = nil) {
        (self.supported, self.enabled, self.installed, self.older) = (supported, enabled, installed, older)
        (self.installing, self.progress, self.failure) = (installing, progress, failure)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        supported = try c.decodeIfPresent(Bool.self, forKey: .supported) ?? false
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        older = try c.decodeIfPresent(Bool.self, forKey: .older) ?? false
        installing = try c.decodeIfPresent(Bool.self, forKey: .installing) ?? false
        progress = try c.decodeIfPresent(Int.self, forKey: .progress)
        failure = try c.decodeIfPresent(String.self, forKey: .failure)
        speaking = try c.decodeIfPresent(Bool.self, forKey: .speaking) ?? false
        voice = try c.decodeIfPresent(String.self, forKey: .voice) ?? ""
    }
}

/// Reads messages aloud through this Mac's daemon (speech.say, speech.stop), one
/// at a time. The daemon runs the speech helper; the app starts no process for it.
/// What's set up, and whether speech is on (the device setting speech.enabled),
/// comes with each snapshot.
@Observable
final class SpeechController {
    static let shared = SpeechController()

    /// The message being read.
    private(set) var speakingItem: InboxKey?
    /// Why the last attempt couldn't be read.
    private(set) var failure: String?
    /// The daemon's word on speech here.
    private(set) var state = SpeechState()

    /// This Mac can read aloud at all: without it, read aloud is hidden.
    var isSupported: Bool { state.supported }
    /// Whether speech is on. Off, the app has no speech buttons and downloads nothing.
    var isEnabled: Bool { state.supported && state.enabled }
    /// Whether the model is here and ready.
    var isInstalled: Bool { state.installed }
    /// Whether the model is being downloaded and set up.
    var isSettingUp: Bool { state.installing }
    /// Whether that replaces an older engine, rather than being a first install.
    var isUpgrading: Bool { state.older }
    /// How much of the model has downloaded, from 0 to 1, once that's known.
    var setupProgress: Double? { state.installing ? state.progress.map { Double($0) / 100 } : nil }
    /// Why setting up failed. It tries again when asked.
    var setupFailure: String? { state.failure }

    var isSpeaking: Bool { speakingItem != nil }

    /// The daemon; tests swap it.
    @ObservationIgnored var calls: DaemonCalls = DaemonSocketCalls()
    @ObservationIgnored var settings = HostSettings.shared
    @ObservationIgnored var defaults = UserDefaults.standard
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var started = false

    /// Where speech on or off was kept before it was a device setting.
    static let oldEnabledKey = "speech.enabled"

    private init() {}

    /// For tests: a controller of its own.
    init(calls: DaemonCalls, settings: HostSettings, defaults: UserDefaults) {
        (self.calls, self.settings, self.defaults) = (calls, settings, defaults)
    }

    /// Starts reading a message, or stops if it's the one being read.
    func toggle(_ item: InboxItem) {
        if speakingItem == item.id { stop() } else { speak(item) }
    }

    func speak(_ item: InboxItem) {
        if speakingItem != nil { stop() }
        failure = nil
        guard isEnabled else {
            failure = "Speech is off. Turn it on from the bell in the status bar."
            return
        }
        if isSettingUp {
            failure = "Speech is still being set up. It'll be ready soon."
            return
        }
        generation += 1
        let mine = generation
        speakingItem = item.id
        let data = (try? JSONEncoder().encode(["text": item.text])) ?? Data()
        calls.call("speech.say", data) { [weak self] result in
            guard let self, mine == self.generation else { return } // another took its place
            self.speakingItem = nil
            if case .failure(let error) = result { self.failure = errorText(error) }
        }
    }

    func stop() {
        failure = nil
        guard speakingItem != nil else { return }
        generation += 1
        speakingItem = nil
        calls.call("speech.stop", Data("{}".utf8)) { _ in }
    }

    /// From each snapshot. The first one also moves where speech on or off was
    /// kept, and sets speech up if it's on and isn't yet.
    func apply(_ next: SpeechState?) {
        guard let next else { return }
        state = next
        guard !started else { return }
        started = true
        moveOldSetting()
    }

    /// The app kept speech on or off in its own defaults; now it's this Mac's
    /// device setting. Someone who had it set up, even with an older engine,
    /// keeps it on.
    private func moveOldSetting() {
        if let chosen = defaults.object(forKey: Self.oldEnabledKey) as? Bool {
            defaults.removeObject(forKey: Self.oldEnabledKey)
            if chosen != state.enabled { return setEnabled(chosen) }
        }
        if isEnabled && !state.installed && !state.installing { install() }
    }

    /// Turning it on downloads the model if it isn't here. Turning it off keeps the model.
    func setEnabled(_ enabled: Bool) {
        state.enabled = enabled
        if !enabled { stop() }
        settings.set(["speech.enabled": .bool(enabled)]) { [weak self] why in self?.failure = why }
        if enabled && !state.installed { install() }
    }

    func retrySetup() {
        install()
    }

    /// Another voice was chosen: it needs downloading, if speech is on.
    func voiceChanged() {
        state.installed = false
        if isEnabled { install() }
    }

    private func install() {
        guard isSupported, !state.installing else { return }
        state.installing = true
        state.failure = nil
        calls.call("speech.install", Data("{}".utf8)) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let data):
                if let next = try? JSONDecoder().decode(SpeechState.self, from: data) { self.state = next }
            case .failure(let error):
                self.state.installing = false
                self.state.failure = errorText(error)
            }
        }
    }

    /// Reports something that stopped a message being read.
    func fail(_ reason: String) {
        failure = reason
    }
}
