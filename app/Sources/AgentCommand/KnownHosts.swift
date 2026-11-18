import Foundation
import Observation

/// What the Add Host and Edit Host sheets hold.
struct KnownHostForm: Equatable {
    var name = ""
    /// host:port; the port is Hosting's, 7433 unless its owner changed it.
    var address = ""
    /// Pinned by the host's cert file rather than a fingerprint pasted in.
    var byFile = false
    var fingerprint = ""
    var cert = ""

    init() {}

    /// To edit a host: what it has now.
    init(host: RemoteHost) {
        name = host.name
        address = host.address
        byFile = !host.cert.isEmpty
        fingerprint = host.cert.isEmpty ? host.fingerprint : ""
        cert = host.cert
    }

    private static func trim(_ text: String) -> String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// What's wrong with it, to show under the fields; nil when it can be saved.
    var problem: String? {
        if Self.trim(name).isEmpty { return "Give it a name." }
        if Self.trim(address).isEmpty { return "Type its address, as host:port." }
        if byFile {
            if Self.trim(cert).isEmpty { return "Choose the host's cert file." }
        } else if !RemoteFingerprint.looksValid(fingerprint) {
            return Self.trim(fingerprint).isEmpty
                ? "Paste the host's fingerprint: acmd remote identity shows it."
                : "That doesn't look like a fingerprint (SHA256:…)."
        }
        return nil
    }

    var ready: Bool { problem == nil }

    /// What remote.hosts.add needs.
    var addition: RemoteHostEdit {
        RemoteHostEdit(
            name: Self.trim(name), address: Self.trim(address),
            fingerprint: byFile ? nil : Self.trim(fingerprint), cert: byFile ? Self.trim(cert) : nil)
    }

    /// What changed from the host as it is, for remote.hosts.edit; nil when nothing did.
    func changes(from host: RemoteHost) -> RemoteHostEdit? {
        var edit = RemoteHostEdit(host: host.name)
        let name = Self.trim(name), address = Self.trim(address)
        if name != host.name { edit.name = name }
        if address != host.address { edit.address = address }
        if byFile {
            let cert = Self.trim(cert)
            if cert != host.cert { edit.cert = cert }
        } else {
            let fingerprint = Self.trim(fingerprint)
            if fingerprint != host.fingerprint || !host.cert.isEmpty { edit.fingerprint = fingerprint }
        }
        return edit == RemoteHostEdit(host: host.name) ? nil : edit
    }
}

/// Settings → Remote → Known hosts: adds, edits, removes and retries hosts
/// through the daemon, which keeps them in config.toml. The list itself is
/// the snapshot's.
@Observable final class KnownHostsModel {
    @ObservationIgnored var client = RemoteHostsClient()
    /// The host being changed, so its row can wait; "" while one is added.
    private(set) var busy: String?
    private(set) var error: String?

    /// done(true) once it's added; false keeps the sheet open, with the error.
    func add(_ form: KnownHostForm, done: @escaping (Bool) -> Void = { _ in }) {
        guard form.ready else {
            error = form.problem
            return done(false)
        }
        start("")
        client.addHost(form.addition) { [weak self] result in
            self?.finish(result.failure)
            done(result.failure == nil)
        }
    }

    func edit(_ host: RemoteHost, _ form: KnownHostForm, done: @escaping (Bool) -> Void = { _ in }) {
        guard form.ready else {
            error = form.problem
            return done(false)
        }
        guard let changes = form.changes(from: host) else { return done(true) }
        start(host.name)
        client.editHost(changes) { [weak self] result in
            self?.finish(result.failure)
            done(result.failure == nil)
        }
    }

    func remove(_ host: RemoteHost) {
        start(host.name)
        client.removeHost(host.name) { [weak self] failure in self?.finish(failure) }
    }

    func retry(_ host: RemoteHost) {
        start(host.name)
        client.retry(host.name) { [weak self] result in self?.finish(result.failure) }
    }

    func clearError() { error = nil }

    private func start(_ key: String) {
        busy = key
        error = nil
    }

    private func finish(_ failure: Error?) {
        busy = nil
        error = failure.map(errorText)
    }

    /// The confirmation before removing a host.
    static func removePrompt(_ host: RemoteHost) -> (title: String, note: String) {
        let sessions = host.sessions == 1 ? "The session added from it is" : "The \(host.sessions) sessions added from it are"
        let note = host.sessions == 0
            ? "This Mac stops connecting to it. Nothing on \(host.name) changes."
            : "\(sessions) removed from this Mac only. They keep running on \(host.name), and you can add them again."
        return ("Remove \(host.name)?", note)
    }
}
