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
/// that arrives while Settings is closed still badges and notifies. Every
/// change goes through acmd, which keeps config.toml's comments.
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

    /// Runs acmd; tests swap it.
    @ObservationIgnored var runAcmd: (_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) -> Void = Acmd.run
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
        runAcmd(["remote", "clients", "--json"]) { [weak self] ok, output in
            if ok, let list: [RemoteClient] = Self.decode(output) { self?.clients = list }
        }
        runAcmd(["remote", "requests", "--json"]) { [weak self] ok, output in
            if ok, let list: [RemoteRequest] = Self.decode(output) { self?.receive(requests: list) }
        }
    }

    func loadIdentity() {
        runAcmd(["remote", "identity", "--json"]) { [weak self] ok, output in
            guard let self else { return }
            if ok, let id: RemoteIdentity = Self.decode(output) {
                self.identity = id
            } else if !ok, self.error == nil { // a change's own error says more
                self.error = output.isEmpty ? "Couldn't read this Mac's identity." : output
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
        command(["remote", "hosting", on ? "on" : "off"], key: "hosting")
    }

    func approve(_ request: RemoteRequest, name: String = "") {
        command(["remote", "requests", "approve", request.id] + Self.nameArgs(name), key: "request:\(request.id)")
    }

    /// Approves by id, as a notification's Approve button does.
    func approve(id: String) {
        approve(RemoteRequest(id: id))
    }

    func deny(_ request: RemoteRequest) {
        command(["remote", "requests", "deny", request.id], key: "request:\(request.id)")
    }

    /// Authorizes a client by its cert file or the fingerprint it shows.
    func authorize(_ certOrFingerprint: String, name: String = "") {
        let what = certOrFingerprint.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !what.isEmpty else { return }
        command(["remote", "authorize", what] + Self.nameArgs(name), key: "authorize")
    }

    func rename(_ client: RemoteClient, to name: String) {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != client.name else { return }
        command(["remote", "rename", client.fingerprint, name], key: "client:\(client.fingerprint)")
    }

    func revoke(_ client: RemoteClient) {
        command(["remote", "revoke", client.fingerprint], key: "client:\(client.fingerprint)")
    }

    func disconnect(_ client: RemoteClient) {
        command(["remote", "disconnect", client.fingerprint], key: "client:\(client.fingerprint)")
    }

    /// Your own cert and key for a role; acmd checks they belong together first.
    func useOwnCert(_ role: RemoteRole, cert: String, key: String) {
        command(["remote", "cert", role.rawValue, cert, key], key: "cert:\(role.rawValue)")
    }

    func useAcmdIdentity(_ role: RemoteRole) {
        command(["remote", "cert", role.rawValue, "--default"], key: "cert:\(role.rawValue)")
    }

    /// A new identity: every peer that trusts this Mac must trust the new fingerprint.
    func regenerate() {
        command(["remote", "identity", "--regenerate", "--yes"], key: "regenerate")
    }

    private func command(_ args: [String], key: String) {
        busy = key
        error = nil
        runAcmd(args) { [weak self] ok, output in
            guard let self else { return }
            self.busy = nil
            if !ok { self.error = output.isEmpty ? "Couldn't change it." : output }
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

    static func nameArgs(_ name: String) -> [String] {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? [] : ["--name", name]
    }

    static func decode<T: Decodable>(_ output: String) -> T? {
        output.data(using: .utf8).flatMap { try? JSONDecoder().decode(T.self, from: $0) }
    }
}
