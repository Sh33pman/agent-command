import Foundation
import Observation

/// The two roles a cert can be for: Hosting, and connecting to hosts.
enum RemoteRole: String, CaseIterable, Identifiable {
    case host, client

    var id: String { rawValue }

    var title: String {
        switch self {
        case .host: "Hosting cert"
        case .client: "Client cert"
        }
    }

    /// The config key that names its cert: "" is acmd's own identity.
    func cert(in values: AcmdSettings.Values) -> String {
        self == .host ? values.hostCert : values.clientCert
    }
}

/// A client's connection, as the Connected clients list shows it.
struct RemoteConnectionRow: Identifiable, Equatable {
    let id: String
    let client: String
    let from: String
    /// "Attached to api, web" or "Not attached to a session".
    let sessions: String
}

/// What a click on a pending request's notification asks for.
enum RemoteRequestResponse: Equatable {
    case approve(String)
    case show(String)

    static let category = "remote-request"
    static let approveAction = "remote-request.approve"
    /// Where the notification keeps the request's id.
    static let userInfoKey = "remoteRequest"

    /// The Approve button approves; the banner itself, or anything else, shows it.
    init(action: String, request id: String) {
        self = action == Self.approveAction ? .approve(id) : .show(id)
    }
}

/// Hosting, its clients and its requests, for Settings, the palette and
/// notifications. The app lives with one for as long as it runs: a request
/// that arrives while Settings is closed still badges and notifies. It calls
/// the daemon's remote.* ops directly: no acmd process.
@Observable final class RemoteSettingsModel {
    /// From the daemon's snapshot; nil when it has no Hosting.
    private(set) var status: RemoteStatus?
    private(set) var clients: [RemoteClient] = []
    private(set) var requests: [RemoteRequest] = []
    private(set) var identity: RemoteIdentity?
    /// What went wrong last.
    private(set) var error: String?
    /// The change in progress, so its control can wait.
    private(set) var busy: String?
    /// The request a notification opened Settings at.
    var highlighted: String?

    /// The daemon; tests swap it.
    @ObservationIgnored var calls: DaemonCalls = DaemonSocketCalls()
    /// Told about requests that arrived since the last look, not ones from before the app started.
    @ObservationIgnored var onNewRequests: ([RemoteRequest]) -> Void = { _ in }
    /// After a change: Settings reads acmd's settings again.
    @ObservationIgnored var onChange: () -> Void = {}
    @ObservationIgnored private var seenRequests: Set<String>?
    @ObservationIgnored private var updated = false

    var hostingOn: Bool { status?.enabled ?? false }
    /// The Settings sidebar's badge.
    var pendingCount: Int { status?.pending ?? requests.count }

    /// Takes the status from a snapshot. A change to it (a request, a client
    /// connecting) reads the lists again.
    func update(_ new: RemoteStatus?) {
        guard new != status || !updated else { return }
        updated = true
        status = new
        loadLists()
    }

    /// Everything, as when the Remote section opens.
    func refresh() {
        loadLists()
        loadIdentity()
    }

    func loadLists() {
        call("remote.clients", Nothing()) { [weak self] (result: Result<[RemoteClient], Error>) in
            if case .success(let list) = result { self?.clients = list }
        }
        call("remote.requests", Nothing()) { [weak self] (result: Result<[RemoteRequest], Error>) in
            if case .success(let list) = result { self?.receive(requests: list) }
        }
    }

    func loadIdentity() {
        call("remote.identity", Nothing()) { [weak self] (result: Result<RemoteIdentity, Error>) in
            guard let self else { return }
            switch result {
            case .success(let id): self.identity = id
            case .failure(let failure):
                if self.error == nil { // a change's own error says more
                    let why = errorText(failure)
                    self.error = why.isEmpty ? "Couldn't read this Mac's identity." : why
                }
            }
        }
    }

    /// The requests now. Those not seen before are news, except on the first look.
    func receive(requests list: [RemoteRequest]) {
        requests = list
        let ids = Set(list.map(\.id))
        if let seen = seenRequests {
            let fresh = list.filter { !seen.contains($0.id) }
            if !fresh.isEmpty { onNewRequests(fresh) }
        }
        seenRequests = (seenRequests ?? []).union(ids)
        if let highlighted, !ids.contains(highlighted) { self.highlighted = nil }
    }

    // MARK: Changes

    func setHosting(_ on: Bool) {
        change("remote.hosting", Ref(enabled: on), key: "hosting")
    }

    func approve(_ request: RemoteRequest, name: String = "") {
        change("remote.approve", Ref(id: request.id, name: Self.trimmed(name)), key: "request:\(request.id)")
    }

    /// Approves by id, as a notification's Approve button does.
    func approve(id: String) {
        approve(RemoteRequest(id: id))
    }

    func deny(_ request: RemoteRequest) {
        change("remote.deny", Ref(id: request.id), key: "request:\(request.id)")
    }

    /// Authorizes a client by its cert file or the fingerprint it shows.
    func authorize(_ certOrFingerprint: String, name: String = "") {
        let what = certOrFingerprint.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !what.isEmpty else { return }
        var ref = Ref(name: Self.trimmed(name))
        if what.hasPrefix("/") || what.hasPrefix("~") {
            ref.cert = (what as NSString).expandingTildeInPath
        } else {
            ref.fingerprint = what
        }
        change("remote.authorize", ref, key: "authorize")
    }

    func rename(_ client: RemoteClient, to name: String) {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != client.name else { return }
        change("remote.rename", Ref(client: client.fingerprint, name: name), key: "client:\(client.fingerprint)")
    }

    /// What a client may do here: read, act or admin.
    func setScope(_ client: RemoteClient, _ scope: String) {
        guard scope != client.scope else { return }
        change("remote.scope", Ref(client: client.fingerprint, scope: scope), key: "client:\(client.fingerprint)")
    }

    func revoke(_ client: RemoteClient) {
        change("remote.revoke", Ref(client: client.fingerprint), key: "client:\(client.fingerprint)")
    }

    func disconnect(_ client: RemoteClient) {
        change("remote.disconnect", Ref(client: client.fingerprint), key: "client:\(client.fingerprint)")
    }

    /// Your own cert and key for a role; the daemon checks they belong together first.
    func useOwnCert(_ role: RemoteRole, cert: String, key: String) {
        change("remote.cert", Cert(role: role.rawValue, cert: cert, key: key), key: "cert:\(role.rawValue)")
    }

    func useAcmdIdentity(_ role: RemoteRole) {
        change("remote.cert", Cert(role: role.rawValue, isDefault: true), key: "cert:\(role.rawValue)")
    }

    /// A new identity: every peer that trusts this Mac must trust the new fingerprint.
    func regenerate() {
        change("remote.identity.regenerate", Nothing(), key: "regenerate")
    }

    // MARK: The daemon

    /// remote.*'s argument (internal/ipc.RemoteRef): what's nil isn't sent.
    struct Ref: Encodable, Equatable {
        var id: String?
        var client: String?
        var name: String?
        var fingerprint: String?
        var cert: String?
        var enabled: Bool?
        var scope: String?
    }

    struct Cert: Encodable, Equatable {
        let role: String
        var cert: String?
        var key: String?
        var isDefault: Bool?

        enum CodingKeys: String, CodingKey {
            case role, cert, key
            case isDefault = "default"
        }
    }

    private struct Nothing: Codable {}

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

    /// A change: the control waits, and then everything is read again.
    private func change(_ op: String, _ input: some Encodable, key: String) {
        busy = key
        error = nil
        calls.call(op, (try? JSONEncoder().encode(input)) ?? Data("{}".utf8)) { [weak self] result in
            guard let self else { return }
            self.busy = nil
            if case .failure(let failure) = result {
                let why = errorText(failure)
                self.error = why.isEmpty ? "Couldn't change it." : why
            }
            self.refresh()
            self.onChange()
        }
    }

    // MARK: What's shown

    /// Who's attached to what, one row a connection. sessionName names a
    /// session from its id, when the app knows it.
    func connectionRows(sessionName: (String) -> String?) -> [RemoteConnectionRow] {
        clients.flatMap { client in
            client.connections.map { conn in
                let names = conn.sessions.map { sessionName($0) ?? $0 }
                let since = RemoteTime.ago(conn.since)
                return RemoteConnectionRow(
                    id: conn.id.isEmpty ? client.fingerprint : conn.id,
                    client: client.name.isEmpty ? RemoteFingerprint.short(client.fingerprint) : client.name,
                    from: since.isEmpty ? conn.addr : "\(conn.addr), connected \(since)",
                    sessions: names.isEmpty ? "Not attached to a session" : "Attached to \(names.joined(separator: ", "))")
            }
        }
    }

    /// A pending request's notification.
    static func notification(for request: RemoteRequest) -> (title: String, body: String) {
        let from = request.addr.isEmpty ? "" : " from \(request.addr)"
        return ("\(request.displayName) wants to connect",
                "A request\(from), fingerprint \(RemoteFingerprint.short(request.fingerprint)). Approve it to let it use this Mac's Remote sessions.")
    }

    /// The command palette's entry for the Hosting switch.
    static func paletteTitle(hostingOn: Bool) -> String {
        hostingOn ? "Turn Hosting off" : "Turn Hosting on"
    }

    /// A port typed in: 1 to 65535.
    static func port(_ text: String) -> Int? {
        guard let n = Int(text.trimmingCharacters(in: .whitespaces)), (1...65535).contains(n) else { return nil }
        return n
    }

    /// The settings a new port changes. A port of your own isn't swapped for
    /// another when it's taken, so choosing one turns the fallback off; the
    /// checkbox can turn it on again.
    static func portChanges(_ port: Int) -> [(String, String)] {
        let set = [("remote.host.port", String(port))]
        return port == defaultPort ? set : set + [("remote.host.port_fallback", "off")]
    }

    static let defaultPort = 7433

    /// A name typed in, or nil when there's none.
    static func trimmed(_ name: String) -> String? {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? nil : name
    }
}
