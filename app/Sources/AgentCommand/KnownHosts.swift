import Foundation
import Observation

/// What the Add Host and Edit Host sheets hold.
nonisolated struct KnownHostForm: Equatable {
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
        edit.version = host.version.isEmpty ? nil : host.version
        if name != host.name { edit.name = name }
        if address != host.address { edit.address = address }
        if byFile {
            let cert = Self.trim(cert)
            if cert != host.cert { edit.cert = cert }
        } else {
            let fingerprint = Self.trim(fingerprint)
            if fingerprint != host.fingerprint || !host.cert.isEmpty { edit.fingerprint = fingerprint }
        }
        var none = RemoteHostEdit(host: host.name)
        none.version = edit.version
        return edit == none ? nil : edit
    }
}

/// Settings → Remote → Known hosts: adds, edits, removes and retries hosts
/// through the daemon, which keeps them in its database. The list itself is
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
        // After a refused edit, measured against the host as it is now.
        let host = newer[host.name] ?? host
        guard let changes = form.changes(from: host) else { return done(true) }
        start(host.name)
        client.editHost(changes) { [weak self] result in
            guard let self else { return }
            if case .failure(let failure as DaemonError) = result, failure.code == "conflict" {
                self.refused(host)
                return done(false)
            }
            self.newer[host.name] = nil
            self.finish(result.failure)
            done(result.failure == nil)
        }
    }

    /// The hosts as they were after an edit was refused, by name: the next
    /// save goes from them, so it replaces what changed on purpose.
    private var newer: [String: RemoteHost] = [:]

    /// An edit refused because the host changed since the sheet opened: the
    /// sheet keeps your changes, and says what's there now.
    private func refused(_ host: RemoteHost) {
        client.hosts { [weak self] result in
            guard let self else { return }
            self.busy = nil
            guard let now = (try? result.get())?.first(where: { $0.name == host.name }) else {
                self.error = "\(host.name) was removed or renamed while you were editing it."
                return
            }
            self.newer[host.name] = now
            self.error = "\(host.name) changed while you were editing it: it's now at \(now.address). "
                + "Your changes are still here: Save again to replace it."
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
