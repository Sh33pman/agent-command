import Foundation

/// What the host can do (internal/ipc.Features): the app shows what works.
///
/// Every field is optional on the wire. The app and acmd upgrade separately,
/// so the daemon can be older than the app and leave out a field this one
/// knows: that field takes its default, and the snapshot still decodes.
struct HostFeatures: Decodable, Equatable {
    struct Host: Decodable, Equatable {
        var os = ""
        /// The kinds an installed harness has, and terminal.
        var harnessKinds: [String] = []
        /// It runs sessions here. A client-only daemon (Windows) only uses
        /// its hosts', and refuses what needs sessions here. A daemon from
        /// before it was sent (0.87 and older) always runs them.
        var sessions = true
        var wake = false
        var hosting = false

        enum CodingKeys: String, CodingKey {
            case os, sessions, wake, hosting
            case harnessKinds = "harness_kinds"
        }

        init(os: String = "", harnessKinds: [String] = [], sessions: Bool = true, wake: Bool = false, hosting: Bool = false) {
            self.os = os
            self.harnessKinds = harnessKinds
            self.sessions = sessions
            self.wake = wake
            self.hosting = hosting
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            os = try c.decodeIfPresent(String.self, forKey: .os) ?? ""
            harnessKinds = try c.decodeIfPresent([String].self, forKey: .harnessKinds) ?? []
            sessions = try c.decodeIfPresent(Bool.self, forKey: .sessions) ?? true
            wake = try c.decodeIfPresent(Bool.self, forKey: .wake) ?? false
            hosting = try c.decodeIfPresent(Bool.self, forKey: .hosting) ?? false
        }
    }

    struct Device: Decodable, Equatable {
        var speech = false
        var editor = false

        enum CodingKeys: String, CodingKey { case speech, editor }

        init(speech: Bool = false, editor: Bool = false) {
            self.speech = speech
            self.editor = editor
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            speech = try c.decodeIfPresent(Bool.self, forKey: .speech) ?? false
            editor = try c.decodeIfPresent(Bool.self, forKey: .editor) ?? false
        }
    }

    enum CodingKeys: String, CodingKey { case host, device }

    init(host: Host = Host(), device: Device = Device()) {
        self.host = host
        self.device = device
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        host = try c.decodeIfPresent(Host.self, forKey: .host) ?? Host()
        device = try c.decodeIfPresent(Device.self, forKey: .device) ?? Device()
    }

    var host = Host()
    var device = Device()
}

/// acmd's own version, and the newest release when it's newer (internal/ipc.Update).
struct UpdateState: Decodable, Equatable {
    var current = ""
    var latest: String? = nil
    var url: String? = nil

    var releaseURL: URL? { url.flatMap(URL.init(string:)) }
}

/// The daemon's health, setup and report ops. The app never runs acmd for
/// them, nor reads the agents' files: the daemon does, for every client.
struct HealthClient {
    var calls: DaemonCalls = DaemonSocketCalls()

    private struct Ref: Encodable {
        let client_id: String
        var id: String?
    }

    private struct ReportRequest: Encodable {
        let kind: String
        var title: String?
        var body: String?
    }

    struct InitResult: Decodable, Equatable {
        var output = ""
        var done = false
    }

    struct ReportDetails: Decodable { let details: String }

    private struct Nothing: Codable {}

    /// Stops showing a warning here, or (dismissed false) shows every one again.
    func dismiss(_ id: String?, client: String, done: @escaping (Result<[String], Error>) -> Void = { _ in }) {
        if let id {
            call("health.dismiss", Ref(client_id: client, id: id), done)
        } else {
            call("health.restore", Ref(client_id: client), done)
        }
    }

    /// Sets acmd up, as `acmd init` does: the daemon runs it.
    func setUp(done: @escaping (Result<InitResult, Error>) -> Void) {
        call("init", Nothing(), done)
    }

    /// Calls a warning's fix op, with its input.
    func fix(_ op: String, data: [String: SettingValue], done: @escaping (Error?) -> Void) {
        call(op, data) { (result: Result<Nothing, Error>) in done(result.failure) }
    }

    func reportDetails(_ kind: ReportKind, done: @escaping (Result<String, Error>) -> Void) {
        call("report.details", ReportRequest(kind: kind.rawValue)) { (result: Result<ReportDetails, Error>) in
            done(result.map(\.details))
        }
    }

    func reportPage(_ draft: ReportDraft, done: @escaping (Result<ReportPage, Error>) -> Void) {
        call("report.page", ReportRequest(kind: draft.kind.rawValue, title: draft.title, body: draft.body), done)
    }

    private func call<Out: Decodable>(_ op: String, _ input: some Encodable, _ done: @escaping (Result<Out, Error>) -> Void) {
        let data: Data
        do {
            data = try JSONEncoder().encode(input)
        } catch {
            return done(.failure(error))
        }
        calls.call(op, data) { result in
            done(result.flatMap { reply in Result { try JSONDecoder().decode(Out.self, from: reply) } })
        }
    }
}

/// The daemon's health entries as the app's warnings.
enum HealthWarnings {
    /// Setup is offered once at launch, or done again quietly: not a warning.
    static let setupIDs: Set<String> = ["setup.offer", "setup.refresh"]

    /// fix runs an entry's fix: its op, or on this Mac its acmd command.
    static func warnings(_ health: [HealthItem], fix: @escaping (HealthItem.Fix) -> Void) -> [AppWarning] {
        health.filter { !setupIDs.contains($0.id) }.map { item in
            let f = item.fix
            let runnable = f.map { $0.op != nil || $0.command != nil } ?? false
            return AppWarning(
                id: item.id, title: item.title, detail: item.detail, fix: f?.step ?? "",
                actionTitle: runnable ? (f?.title ?? "Fix") : "",
                action: { if let f, runnable { fix(f) } })
        }
    }
}
