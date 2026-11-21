import Foundation

// Remote sessions, as the daemon and `acmd remote … --json` describe them
// (internal/ipc). Missing fields are their defaults, so an older or newer acmd
// still reads.

private extension KeyedDecodingContainer {
    func value<T: Decodable>(_ key: Key, _ fallback: T) -> T {
        (try? decodeIfPresent(T.self, forKey: key)) ?? fallback
    }
}

/// Hosting (internal/ipc.RemoteStatus): whether it's on, where it listens, and
/// why not when it can't. Every snapshot carries it.
struct RemoteStatus: Decodable, Equatable {
    var enabled = false
    var listening = false
    /// "localhost" or "all".
    var listen = "localhost"
    /// Where it listens now: port differs from configuredPort when that one was
    /// taken and the fallback picked another.
    var address = ""
    var port = 0
    var configuredPort = 7433
    var portFallback = true
    var since = ""
    /// Why it isn't listening.
    var error = ""
    /// The identity it shows clients.
    var fingerprint = ""
    var name = ""
    var authorized = 0
    /// Connections, not clients: one client can have several.
    var connected = 0
    /// Requests waiting for approval.
    var pending = 0
    /// How many client connections are attached to each session, by id.
    var attached: [String: Int] = [:]

    enum CodingKeys: String, CodingKey {
        case enabled, listening, listen, address, port, since, error, fingerprint, name, authorized, connected, pending, attached
        case configuredPort = "configured_port", portFallback = "port_fallback"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = RemoteStatus()
        enabled = c.value(.enabled, d.enabled)
        listening = c.value(.listening, d.listening)
        listen = c.value(.listen, d.listen)
        address = c.value(.address, d.address)
        port = c.value(.port, d.port)
        configuredPort = c.value(.configuredPort, d.configuredPort)
        portFallback = c.value(.portFallback, d.portFallback)
        since = c.value(.since, d.since)
        error = c.value(.error, d.error)
        fingerprint = c.value(.fingerprint, d.fingerprint)
        name = c.value(.name, d.name)
        authorized = c.value(.authorized, d.authorized)
        connected = c.value(.connected, d.connected)
        attached = c.value(.attached, d.attached)
        pending = c.value(.pending, d.pending)
    }

    /// The status line under the Hosting switch.
    var line: String {
        switch (enabled, listening) {
        case (false, _): "Off"
        case (true, true) where port != configuredPort && port != 0:
            "Listening on \(address) (port \(configuredPort) was taken)"
        case (true, true): "Listening on \(address)"
        case (true, false): error.isEmpty ? "Starting…" : "Not listening: \(error)"
        }
    }

    /// The line says what's wrong.
    var isFailing: Bool { enabled && !listening && !error.isEmpty }
}

/// A client connected now (internal/ipc.RemoteConnection).
struct RemoteConnection: Decodable, Equatable, Identifiable {
    var id = ""
    /// Its authorized name.
    var name = ""
    var fingerprint = ""
    /// The name in its cert.
    var commonName = ""
    var addr = ""
    /// Its acmd's version.
    var version = ""
    var since = ""
    /// The sessions it's attached to.
    var sessions: [String] = []

    enum CodingKeys: String, CodingKey {
        case id, name, fingerprint, addr, version, since, sessions
        case commonName = "common_name"
    }

    init(id: String = "", name: String = "", addr: String = "", sessions: [String] = []) {
        (self.id, self.name, self.addr, self.sessions) = (id, name, addr, sessions)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.value(.id, "")
        name = c.value(.name, "")
        fingerprint = c.value(.fingerprint, "")
        commonName = c.value(.commonName, "")
        addr = c.value(.addr, "")
        version = c.value(.version, "")
        since = c.value(.since, "")
        sessions = c.value(.sessions, [])
    }
}

/// An authorized client, and its connections (internal/ipc.RemoteClient).
struct RemoteClient: Decodable, Equatable, Identifiable {
    var name = ""
    var fingerprint = ""
    var connections: [RemoteConnection] = []
    /// Listed in config.toml: renamed or revoked there, not here.
    var inConfig = false

    var id: String { fingerprint }
    var isConnected: Bool { !connections.isEmpty }

    enum CodingKeys: String, CodingKey {
        case name, fingerprint, connections
        case inConfig = "in_config"
    }

    init(name: String, fingerprint: String, connections: [RemoteConnection] = []) {
        (self.name, self.fingerprint, self.connections) = (name, fingerprint, connections)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = c.value(.name, "")
        fingerprint = c.value(.fingerprint, "")
        connections = c.value(.connections, [])
        inConfig = c.value(.inConfig, false)
    }
}

/// A client this Mac doesn't know that tried to connect (internal/ipc.RemoteRequest).
/// It gets in once it's approved; repeats add to count.
struct RemoteRequest: Decodable, Equatable, Identifiable {
    var id = ""
    var fingerprint = ""
    var commonName = ""
    var addr = ""
    var firstSeen = ""
    var lastSeen = ""
    var count = 1

    enum CodingKeys: String, CodingKey {
        case id, fingerprint, addr, count
        case commonName = "common_name", firstSeen = "first_seen", lastSeen = "last_seen"
    }

    init(id: String, fingerprint: String = "", commonName: String = "", addr: String = "") {
        (self.id, self.fingerprint, self.commonName, self.addr) = (id, fingerprint, commonName, addr)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.value(.id, "")
        fingerprint = c.value(.fingerprint, "")
        commonName = c.value(.commonName, "")
        addr = c.value(.addr, "")
        firstSeen = c.value(.firstSeen, "")
        lastSeen = c.value(.lastSeen, "")
        count = c.value(.count, 1)
    }

    /// What it calls itself, or its fingerprint's short form.
    var displayName: String { commonName.isEmpty ? RemoteFingerprint.short(fingerprint) : commonName }

    /// "From 10.0.0.5:51000 · last tried 5 min ago · 3 tries".
    func detail(now: Date = Date()) -> String {
        var parts: [String] = []
        if !addr.isEmpty { parts.append("From \(addr)") }
        let ago = RemoteTime.ago(lastSeen, now: now)
        if !ago.isEmpty { parts.append("last tried \(ago)") }
        parts.append(count == 1 ? "1 try" : "\(count) tries")
        return parts.joined(separator: " · ")
    }
}

/// One role's identity, from `acmd remote identity --json`.
struct RemoteIdentityRole: Decodable, Equatable {
    var fingerprint = ""
    var commonName = ""
    var cert = ""
    var key = ""

    enum CodingKeys: String, CodingKey {
        case fingerprint, cert, key
        case commonName = "common_name"
    }

    init(fingerprint: String = "", commonName: String = "", cert: String = "", key: String = "") {
        (self.fingerprint, self.commonName, self.cert, self.key) = (fingerprint, commonName, cert, key)
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        fingerprint = c.value(.fingerprint, "")
        commonName = c.value(.commonName, "")
        cert = c.value(.cert, "")
        key = c.value(.key, "")
    }
}

/// This Mac's identity for Remote sessions: the one it shows as a host, and the
/// one it shows hosts as a client when that's another.
struct RemoteIdentity: Decodable, Equatable {
    var host = RemoteIdentityRole()
    /// nil: connecting to hosts uses the same identity.
    var client: RemoteIdentityRole?

    enum CodingKeys: String, CodingKey { case client }

    init(host: RemoteIdentityRole, client: RemoteIdentityRole? = nil) {
        (self.host, self.client) = (host, client)
    }

    init(from decoder: Decoder) throws {
        host = try RemoteIdentityRole(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        client = try c.decodeIfPresent(RemoteIdentityRole.self, forKey: .client)
    }

    /// The fingerprint hosts authorize: the client's own, or the shared one.
    var clientFingerprint: String { client?.fingerprint ?? host.fingerprint }
}

/// Fingerprints: "SHA256:" and the cert's hash in base64.
enum RemoteFingerprint {
    static let prefix = "SHA256:"

    /// "SHA256:AbCdEfGh…wXyZ": enough to tell them apart at a glance.
    static func short(_ fingerprint: String) -> String {
        let hash = fingerprint.hasPrefix(prefix) ? String(fingerprint.dropFirst(prefix.count)) : fingerprint
        guard hash.count > 14 else { return fingerprint }
        return prefix + hash.prefix(8) + "…" + hash.suffix(4)
    }

    /// Looks like one: acmd checks it properly.
    static func looksValid(_ text: String) -> Bool {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let hash = t.hasPrefix(prefix) ? String(t.dropFirst(prefix.count)) : t
        return hash.count >= 40 && hash.allSatisfy { $0.isLetter || $0.isNumber || "+/=".contains($0) }
    }
}

/// Go's times (RFC 3339, with up to nine digits of fraction) and how long ago they were.
enum RemoteTime {
    static func parse(_ text: String) -> Date? {
        guard !text.isEmpty, !text.hasPrefix("0001-") else { return nil }
        // Foundation reads at most milliseconds: drop the rest of the fraction.
        let trimmed = text.replacingOccurrences(of: #"\.\d+"#, with: "", options: .regularExpression)
        return ISO8601DateFormatter().date(from: trimmed)
    }

    /// "just now", "5 min ago", "3 h ago", "2 days ago"; "" when it can't be read.
    static func ago(_ text: String, now: Date = Date()) -> String {
        guard let date = parse(text) else { return "" }
        let seconds = max(0, Int(now.timeIntervalSince(date)))
        switch seconds {
        case ..<60: return "just now"
        case ..<3600: return "\(seconds / 60) min ago"
        case ..<86400: return "\(seconds / 3600) h ago"
        case ..<172_800: return "1 day ago"
        default: return "\(seconds / 86400) days ago"
        }
    }
}
