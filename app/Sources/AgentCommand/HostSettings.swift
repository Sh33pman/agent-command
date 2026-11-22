import Foundation
import Observation

/// A setting's value, as the daemon sends it (internal/config): on or off, a
/// number, text, or a list of text.
enum SettingValue: Codable, Equatable {
    case bool(Bool)
    case number(Double)
    case string(String)
    case list([String])

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let b = try? c.decode(Bool.self) {
            self = .bool(b)
        } else if let n = try? c.decode(Double.self) {
            self = .number(n)
        } else if let s = try? c.decode(String.self) {
            self = .string(s)
        } else {
            self = .list(try c.decode([String].self))
        }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .bool(let b): try c.encode(b)
        case .number(let n): try c.encode(n)
        case .string(let s): try c.encode(s)
        case .list(let l): try c.encode(l)
        }
    }

    var bool: Bool? { if case .bool(let b) = self { b } else { nil } }
    var string: String? { if case .string(let s) = self { s } else { nil } }
}

/// What a setting is (internal/config.Spec): the daemon describes each one.
struct SettingSpec: Decodable, Equatable {
    let key: String
    let type: String
    var description = ""
    /// "host", or "device": the editor and speech, which belong to the Mac in front of you.
    var place = "host"
    var choices: [String]? = nil
    var suggestions: [String]? = nil
    var runsCode: Bool? = nil
    var readOnly: Bool? = nil

    enum CodingKeys: String, CodingKey {
        case key, type, description, choices, suggestions
        case place = "where", runsCode = "runs_code", readOnly = "read_only"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        type = try c.decodeIfPresent(String.self, forKey: .type) ?? "string"
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
        place = try c.decodeIfPresent(String.self, forKey: .place) ?? "host"
        choices = try c.decodeIfPresent([String].self, forKey: .choices)
        suggestions = try c.decodeIfPresent([String].self, forKey: .suggestions)
        runsCode = try c.decodeIfPresent(Bool.self, forKey: .runsCode)
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly)
    }
}

/// What's wrong with config.toml, while changes wait for it to be fixed.
struct ConfigProblem: Decodable, Equatable {
    var line: Int? = nil
    let message: String

    var text: String {
        line.map { "config.toml, line \($0): \(message)" } ?? "config.toml: \(message)"
    }
}

/// config.get's answer (internal/ipc.ConfigState).
struct ConfigState: Decodable {
    var path = ""
    var settings: [SettingSpec] = []
    var values: [String: SettingValue] = [:]
    /// Each setting's version, sent back with a change to it (config.set).
    var versions: [String: String] = [:]
    var problem: ConfigProblem? = nil

    enum CodingKeys: String, CodingKey { case path, settings, values, versions, problem }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        settings = try c.decodeIfPresent([SettingSpec].self, forKey: .settings) ?? []
        values = try c.decodeIfPresent([String: SettingValue].self, forKey: .values) ?? [:]
        versions = try c.decodeIfPresent([String: String].self, forKey: .versions) ?? [:]
        problem = try c.decodeIfPresent(ConfigProblem.self, forKey: .problem)
    }
}

/// The daemon's settings ops: this Mac's (config.get, config.set), or a Remote
/// host's through it (remote.config.get, remote.config.set). Nothing in the app
/// reads or writes config.toml.
struct SettingsClient {
    var calls: DaemonCalls = DaemonSocketCalls()

    private struct HostRef: Encodable { let host: String }
    private struct Change: Encodable {
        var host: String?
        let values: [String: SettingValue]
        var versions: [String: String]?
    }
    private struct Nothing: Encodable {}

    /// The settings and what each one is. host nil is this Mac.
    func get(host: String? = nil, done: @escaping (Result<ConfigState, Error>) -> Void) {
        if let host {
            call("remote.config.get", HostRef(host: host), done)
        } else {
            call("config.get", Nothing(), done)
        }
    }

    /// Changes settings, all or none. versions are theirs as they were loaded:
    /// one changed since refuses the change ("conflict"); nil applies whatever.
    func set(_ values: [String: SettingValue], host: String? = nil, versions: [String: String]? = nil,
             done: @escaping (Result<ConfigState, Error>) -> Void) {
        call(host == nil ? "config.set" : "remote.config.set", Change(host: host, values: values, versions: versions), done)
    }

    private func call(_ op: String, _ input: some Encodable, _ done: @escaping (Result<ConfigState, Error>) -> Void) {
        let data: Data
        do {
            data = try JSONEncoder().encode(input)
        } catch {
            return done(.failure(error))
        }
        calls.call(op, data) { result in
            done(result.flatMap { reply in Result { try JSONDecoder().decode(ConfigState.self, from: reply) } })
        }
    }
}

/// This Mac's settings in effect, from the daemon's snapshot: what the app
/// reads outside the Settings window, such as what ⌘N starts.
@Observable final class HostSettings {
    static let shared = HostSettings()

    var values: [String: SettingValue] = [:]

    func bool(_ key: String) -> Bool { values[key]?.bool ?? false }
    func string(_ key: String) -> String { values[key]?.string ?? "" }

    /// Changes this Mac's settings. A failure is reported to fail.
    @ObservationIgnored var client = SettingsClient()

    func set(_ values: [String: SettingValue], failed: @escaping (String) -> Void = { _ in }) {
        client.set(values) { [weak self] result in
            switch result {
            case .success(let state): self?.values = state.values
            case .failure(let error): failed(errorText(error))
            }
        }
    }
}
