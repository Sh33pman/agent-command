import Foundation
import Testing

@testable import AgentCommand

private func decode<T: Decodable>(_ json: String) throws -> T {
    try JSONDecoder().decode(T.self, from: Data(json.utf8))
}

private let fp = "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde"

@Test func remoteStatusDecodes() throws {
    let s: RemoteStatus = try decode("""
        {"enabled": true, "listening": true, "listen": "all", "address": "0.0.0.0:7434", "port": 7434,
         "configured_port": 7433, "port_fallback": true, "since": "2026-11-18T03:00:00.123456789Z",
         "fingerprint": "\(fp)", "name": "mac", "cert": "/c.pem", "proto": 1, "authorized": 2, "connected": 1, "pending": 3}
        """)
    #expect(s.enabled && s.listening && s.listen == "all" && s.address == "0.0.0.0:7434" && s.port == 7434)
    #expect(s.configuredPort == 7433 && s.portFallback && s.fingerprint == fp && s.name == "mac")
    #expect(s.authorized == 2 && s.connected == 1 && s.pending == 3 && s.since.hasPrefix("2026-"))

    let empty: RemoteStatus = try decode("{}")
    #expect(empty == RemoteStatus(), "what's missing is the default")
    #expect(empty.configuredPort == 7433 && empty.portFallback && empty.listen == "localhost")

    let snap: Snapshot = try decode(#"{"remote": {"enabled": true, "pending": 1}}"#)
    #expect(snap.remote?.pending == 1)
    let old: Snapshot = try decode("{}")
    #expect(old.remote == nil, "an older daemon has no Hosting")
}

@Test func remoteStatusLine() {
    var s = RemoteStatus()
    #expect(s.line == "Off" && !s.isFailing)
    s.enabled = true
    #expect(s.line == "Starting…" && !s.isFailing)
    s.error = "port 7433 is in use, so Hosting can't start"
    #expect(s.line == "Not listening: port 7433 is in use, so Hosting can't start" && s.isFailing)
    s = RemoteStatus()
    (s.enabled, s.listening, s.address, s.port, s.configuredPort) = (true, true, "127.0.0.1:7433", 7433, 7433)
    #expect(s.line == "Listening on 127.0.0.1:7433")
    (s.address, s.port) = ("127.0.0.1:50123", 50123)
    #expect(s.line == "Listening on 127.0.0.1:50123 (port 7433 was taken)")
    s.enabled = false
    #expect(s.line == "Off")
}

@Test func remoteClientsAndRequestsDecode() throws {
    let clients: [RemoteClient] = try decode("""
        [{"name": "laptop", "fingerprint": "\(fp)", "connections": [
           {"id": "c1", "name": "laptop", "fingerprint": "\(fp)", "common_name": "Laptop.local", "addr": "10.0.0.5:51000",
            "version": "1.2.0", "since": "2026-11-18T03:00:00Z", "sessions": ["s1", "s2"]}]},
         {"name": "desk", "fingerprint": "SHA256:x"}]
        """)
    #expect(clients.count == 2 && clients[0].isConnected && !clients[1].isConnected && clients[0].id == fp)
    let c = clients[0].connections[0]
    #expect(c.id == "c1" && c.commonName == "Laptop.local" && c.addr == "10.0.0.5:51000" && c.version == "1.2.0")
    #expect(c.sessions == ["s1", "s2"] && c.since == "2026-11-18T03:00:00Z" && c.name == "laptop" && c.fingerprint == fp)

    let requests: [RemoteRequest] = try decode("""
        [{"id": "r1", "fingerprint": "\(fp)", "common_name": "Stranger", "addr": "10.0.0.9:40000",
          "first_seen": "2026-11-18T02:00:00Z", "last_seen": "2026-11-18T03:00:00Z", "count": 3},
         {"id": "r2", "fingerprint": "\(fp)"}]
        """)
    #expect(requests[0].commonName == "Stranger" && requests[0].count == 3 && requests[0].firstSeen.hasPrefix("2026"))
    #expect(requests[0].displayName == "Stranger" && requests[1].displayName == "SHA256:AbCdEfGh…bcde")
    #expect(requests[1].count == 1 && requests[1].addr.isEmpty)
    let now = RemoteTime.parse("2026-11-18T03:05:00Z")!
    #expect(requests[0].detail(now: now) == "From 10.0.0.9:40000 · last tried 5 min ago · 3 tries")
    #expect(requests[1].detail(now: now) == "1 try")
}

@Test func remoteIdentityDecodes() throws {
    let same: RemoteIdentity = try decode("""
        {"fingerprint": "\(fp)", "common_name": "mac", "cert": "/i/cert.pem", "key": "/i/key.pem",
         "used_for": ["hosting", "client"], "address": "127.0.0.1:7433"}
        """)
    #expect(same.host == RemoteIdentityRole(fingerprint: fp, commonName: "mac", cert: "/i/cert.pem", key: "/i/key.pem"))
    #expect(same.client == nil && same.clientFingerprint == fp)

    let two: RemoteIdentity = try decode("""
        {"fingerprint": "\(fp)", "common_name": "mac", "cert": "/mine.pem", "key": "/mine-key.pem", "used_for": ["hosting"],
         "client": {"fingerprint": "SHA256:other", "common_name": "mac", "cert": "/i/cert.pem", "key": "/i/key.pem", "used_for": ["client"]}}
        """)
    #expect(two.client?.cert == "/i/cert.pem" && two.clientFingerprint == "SHA256:other")
}

@Test func fingerprintShortForm() {
    #expect(RemoteFingerprint.short(fp) == "SHA256:AbCdEfGh…bcde")
    #expect(RemoteFingerprint.short("AbCdEfGhIjKlMnOpQrSt") == "SHA256:AbCdEfGh…QrSt", "the prefix is added")
    #expect(RemoteFingerprint.short("SHA256:short") == "SHA256:short", "too short to shorten")
    #expect(RemoteFingerprint.short("") == "")
    #expect(RemoteFingerprint.looksValid(fp) && RemoteFingerprint.looksValid("  \(fp)\n"))
    #expect(RemoteFingerprint.looksValid(String(fp.dropFirst(7))))
    #expect(!RemoteFingerprint.looksValid("SHA256:abc") && !RemoteFingerprint.looksValid("") && !RemoteFingerprint.looksValid(fp + "!"))
}

@Test func remoteTimes() {
    let now = RemoteTime.parse("2026-11-18T12:00:00Z")!
    #expect(RemoteTime.parse("2026-11-18T12:00:00.123456789Z") == now, "Go's nanoseconds, to the second")
    #expect(RemoteTime.parse("2026-11-18T13:59:30.5+02:00") == now.addingTimeInterval(-30))
    #expect(RemoteTime.parse("") == nil && RemoteTime.parse("0001-01-01T00:00:00Z") == nil && RemoteTime.parse("soon") == nil)
    #expect(RemoteTime.ago("2026-11-18T11:59:30Z", now: now) == "just now")
    #expect(RemoteTime.ago("2026-11-18T12:00:30Z", now: now) == "just now", "a clock a little ahead")
    #expect(RemoteTime.ago("2026-11-18T11:55:00Z", now: now) == "5 min ago")
    #expect(RemoteTime.ago("2026-11-18T09:00:00Z", now: now) == "3 h ago")
    #expect(RemoteTime.ago("2026-11-17T10:00:00Z", now: now) == "1 day ago")
    #expect(RemoteTime.ago("2026-11-15T12:00:00Z", now: now) == "3 days ago")
    #expect(RemoteTime.ago("", now: now) == "")
}

/// acmd played by the test: it answers the --json reads, and records the rest.
@MainActor
private final class FakeRemoteAcmd {
    var calls: [[String]] = []
    var clients = "[]"
    var requests = "[]"
    var identity = #"{"fingerprint": "SHA256:mine", "common_name": "mac", "cert": "/c", "key": "/k"}"#
    var fail = false
    var said = ""

    func run(_ args: [String], _ done: @escaping @MainActor (Bool, String) -> Void) {
        calls.append(args)
        switch args {
        case ["remote", "clients", "--json"]: done(true, clients)
        case ["remote", "requests", "--json"]: done(true, requests)
        case ["remote", "identity", "--json"]: done(!fail, fail ? said : identity)
        default: done(!fail, said)
        }
    }

    var changes: [[String]] { calls.filter { $0.last != "--json" } }
}

@MainActor
private func remoteModel(_ acmd: FakeRemoteAcmd) -> RemoteSettingsModel {
    let m = RemoteSettingsModel()
    m.runAcmd = { args, done in acmd.run(args, done) }
    return m
}

@MainActor @Test func remoteModelReadsTheListsWhenTheStatusChanges() {
    let acmd = FakeRemoteAcmd()
    acmd.clients = #"[{"name": "laptop", "fingerprint": "SHA256:l"}]"#
    let m = remoteModel(acmd)
    #expect(!m.hostingOn && m.pendingCount == 0)
    m.update(nil)
    #expect(acmd.calls.count == 2 && m.clients.map(\.name) == ["laptop"], "the first look reads them, even with no Hosting")
    m.update(nil)
    #expect(acmd.calls.count == 2, "nothing changed")

    var on = RemoteStatus()
    on.enabled = true
    m.update(on)
    #expect(m.hostingOn && acmd.calls.count == 4)
    m.update(on)
    #expect(acmd.calls.count == 4)

    m.refresh()
    #expect(m.identity?.host.fingerprint == "SHA256:mine" && acmd.calls.count == 7)

    acmd.clients = "not json"
    m.loadLists()
    #expect(m.clients.map(\.name) == ["laptop"], "a bad answer leaves the list")
    acmd.fail = true
    acmd.said = "the identity can't be read"
    m.loadIdentity()
    #expect(m.error == "the identity can't be read")
    acmd.said = ""
    m.loadIdentity()
    #expect(m.error == "the identity can't be read", "the first error stays")
    acmd.fail = false
    m.setHosting(true) // a change clears it
    acmd.fail = true
    m.loadIdentity()
    #expect(m.error == "Couldn't read this Mac's identity.")
}

@MainActor @Test func newRequestsAreNewsOnlyOnce() {
    let acmd = FakeRemoteAcmd()
    acmd.requests = #"[{"id": "r1", "fingerprint": "SHA256:a"}]"#
    let m = remoteModel(acmd)
    var told: [[String]] = []
    m.onNewRequests = { told.append($0.map(\.id)) }
    m.update(nil)
    #expect(told.isEmpty && m.requests.map(\.id) == ["r1"], "requests from before the app started aren't news")

    var status = RemoteStatus()
    status.pending = 2
    acmd.requests = #"[{"id": "r1", "fingerprint": "SHA256:a"}, {"id": "r2", "fingerprint": "SHA256:b", "common_name": "desk"}]"#
    m.update(status)
    #expect(told == [["r2"]] && m.pendingCount == 2)
    m.loadLists()
    #expect(told == [["r2"]], "told once")

    m.highlighted = "r2"
    m.receive(requests: [RemoteRequest(id: "r2")])
    #expect(m.highlighted == "r2")
    m.receive(requests: [])
    #expect(m.highlighted == nil, "an approved or expired request isn't highlighted")
    #expect(told == [["r2"]])
}

@MainActor @Test func eachChangeRunsAcmd() {
    let acmd = FakeRemoteAcmd()
    let m = remoteModel(acmd)
    var changed = 0
    m.onChange = { changed += 1 }
    let laptop = RemoteClient(name: "laptop", fingerprint: "SHA256:l")
    let request = RemoteRequest(id: "r1", fingerprint: "SHA256:a", commonName: "Stranger")
    m.setHosting(true)
    m.setHosting(false)
    m.approve(request)
    m.approve(request, name: "  desk ")
    m.approve(id: "r2")
    m.deny(request)
    m.authorize("  \(fp) ")
    m.authorize("/certs/laptop.pem", name: "laptop")
    m.authorize("  ")
    m.rename(laptop, to: " travel ")
    m.rename(laptop, to: "laptop")
    m.rename(laptop, to: "")
    m.revoke(laptop)
    m.disconnect(laptop)
    m.useOwnCert(.host, cert: "/mine.pem", key: "/mine-key.pem")
    m.useOwnCert(.client, cert: "/c.pem", key: "/k.pem")
    m.useAcmdIdentity(.client)
    m.regenerate()
    #expect(acmd.changes == [
        ["remote", "hosting", "on"], ["remote", "hosting", "off"],
        ["remote", "requests", "approve", "r1"], ["remote", "requests", "approve", "r1", "--name", "desk"],
        ["remote", "requests", "approve", "r2"], ["remote", "requests", "deny", "r1"],
        ["remote", "authorize", fp], ["remote", "authorize", "/certs/laptop.pem", "--name", "laptop"],
        ["remote", "rename", "SHA256:l", "travel"], ["remote", "revoke", "SHA256:l"], ["remote", "disconnect", "SHA256:l"],
        ["remote", "cert", "host", "/mine.pem", "/mine-key.pem"], ["remote", "cert", "client", "/c.pem", "/k.pem"],
        ["remote", "cert", "client", "--default"], ["remote", "identity", "--regenerate", "--yes"],
    ])
    #expect(changed == 15 && m.busy == nil && m.error == nil, "each change reads everything again")

    acmd.fail = true
    acmd.said = "the cert and key don't belong together"
    m.useOwnCert(.host, cert: "/a", key: "/b")
    #expect(m.error == "the cert and key don't belong together")
    acmd.said = ""
    m.revoke(laptop)
    #expect(m.error == "Couldn't change it.")
}

@MainActor @Test func whoIsAttachedToWhat() throws {
    let acmd = FakeRemoteAcmd()
    acmd.clients = """
        [{"name": "laptop", "fingerprint": "SHA256:l", "connections": [
           {"id": "c1", "addr": "10.0.0.5:51000", "sessions": ["s1", "gone"]}, {"id": "c2", "addr": "10.0.0.5:51001"}]},
         {"name": "", "fingerprint": "\(fp)", "connections": [{"addr": "10.0.0.6:1", "since": "2026-11-18T03:00:00Z"}]},
         {"name": "desk", "fingerprint": "SHA256:d"}]
        """
    let m = remoteModel(acmd)
    m.loadLists()
    let rows = m.connectionRows { $0 == "s1" ? "api" : nil }
    #expect(rows.map(\.id) == ["c1", "c2", fp])
    #expect(rows[0] == RemoteConnectionRow(id: "c1", client: "laptop", from: "10.0.0.5:51000", sessions: "Attached to api, gone"))
    #expect(rows[1].sessions == "Not attached to a session")
    #expect(rows[2].client == "SHA256:AbCdEfGh…bcde" && rows[2].from.hasPrefix("10.0.0.6:1, connected "))
}

@Test func remoteWords() {
    let text = RemoteSettingsModel.notification(for: RemoteRequest(id: "r1", fingerprint: fp, commonName: "Laptop", addr: "10.0.0.5:1"))
    #expect(text.title == "Laptop wants to connect")
    #expect(text.body == "A request from 10.0.0.5:1, fingerprint SHA256:AbCdEfGh…bcde. Approve it to let it use this Mac's Remote sessions.")
    #expect(RemoteSettingsModel.notification(for: RemoteRequest(id: "r1", fingerprint: fp)).body.hasPrefix("A request, fingerprint"))
    #expect(RemoteSettingsModel.paletteTitle(hostingOn: false) == "Turn Hosting on")
    #expect(RemoteSettingsModel.paletteTitle(hostingOn: true) == "Turn Hosting off")
    #expect(RemoteSettingsModel.port("7433") == 7433 && RemoteSettingsModel.port(" 80 ") == 80)
    #expect(RemoteSettingsModel.port("0") == nil && RemoteSettingsModel.port("65536") == nil && RemoteSettingsModel.port("x") == nil)
    #expect(RemoteSettingsModel.portChanges(7433).map { [$0.0, $0.1] } == [["remote.host.port", "7433"]])
    #expect(RemoteSettingsModel.portChanges(7500).map { [$0.0, $0.1] } == [["remote.host.port", "7500"], ["remote.host.port_fallback", "off"]],
            "a port of your own doesn't fall back")
    #expect(RemoteRole.host.title == "Hosting cert" && RemoteRole.client.title == "Client cert" && RemoteRole.host.id == "host")
    var v = AcmdSettings.Values()
    v.hostCert = "/h.pem"
    #expect(RemoteRole.host.cert(in: v) == "/h.pem" && RemoteRole.client.cert(in: v).isEmpty)
}

@Test func aRequestsNotificationAnswers() {
    #expect(RemoteRequestResponse(action: RemoteRequestResponse.approveAction, request: "r1") == .approve("r1"))
    #expect(RemoteRequestResponse(action: "com.apple.UNNotificationDefaultActionIdentifier", request: "r1") == .show("r1"))
    #expect(RemoteRequestResponse(action: "remote-request.show", request: "r1") == .show("r1"))
    #expect(AppNotifier.remoteRequestCategory.identifier == RemoteRequestResponse.category)
    #expect(AppNotifier.remoteRequestCategory.actions.map(\.identifier).contains(RemoteRequestResponse.approveAction))
    let content = AppNotifier.content(for: RemoteRequest(id: "r9", fingerprint: fp, commonName: "Laptop"))
    #expect(content.title == "Laptop wants to connect" && content.categoryIdentifier == RemoteRequestResponse.category)
    #expect(content.userInfo[RemoteRequestResponse.userInfoKey] as? String == "r9")
    AppNotifier().notify([RemoteRequest(id: "r9")]) // a test build can't post: nothing happens
}

@Test func paletteHasHosting() {
    var state = PaletteState()
    var entries = Palette.entries(state)
    #expect(entries.contains { $0.title == "Turn Hosting on" && $0.action == .toggleHosting && $0.section == "Remote" })
    #expect(entries.contains { $0.title == "Remote settings…" && $0.action == .remoteSettings })
    state.hostingOn = true
    entries = Palette.entries(state)
    #expect(entries.contains { $0.title == "Turn Hosting off" })
}
