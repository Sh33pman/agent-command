import Foundation

/// Carries one call to the daemon: its op and input as JSON, and the reply's
/// JSON back on the main queue. Tests swap it for one that answers itself.
protocol DaemonCalls {
    func call(_ op: String, _ input: Data, done: @escaping (Result<Data, Error>) -> Void)
}

/// The daemon on its socket, off the main thread.
struct DaemonSocketCalls: DaemonCalls {
    func call(_ op: String, _ input: Data, done: @escaping (Result<Data, Error>) -> Void) {
        DispatchQueue.global().async {
            let result = Result { try DaemonConnection(socket: DaemonPaths.socket).raw(op, input) }
            DispatchQueue.main.async { done(result) }
        }
    }
}

/// A known host to add, or the changes to one (internal/ipc.RemoteHostEdit).
/// For an edit, host names the entry, and what's nil stays as it is.
struct RemoteHostEdit: Encodable, Equatable {
    var host: String?
    var name: String?
    var address: String?
    var fingerprint: String?
    var cert: String?
}

/// The client daemon's Remote host ops (internal/ipc/remotehosts.go). The app
/// never talks to a host itself: the daemon does, and sessions added from one
/// attach through a socket here, as this Mac's own do.
struct RemoteHostsClient {
    var calls: DaemonCalls = DaemonSocketCalls()

    private struct Ref: Encodable {
        var host: String?
        var id: String?
    }

    private struct FolderRequest: Encodable {
        let host: String
        let path: String
        var hidden: Bool?
    }

    private struct NewRequest: Encodable {
        let host: String
        let dir: String
        var harness: String?
        /// A session from the same host to put it in a set with, for a split.
        var set_with: String?
    }

    private struct Nothing: Decodable {}

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

    // MARK: Known hosts

    func addHost(_ edit: RemoteHostEdit, done: @escaping (Result<RemoteHost, Error>) -> Void) {
        call("remote.hosts.add", edit, done)
    }

    func editHost(_ edit: RemoteHostEdit, done: @escaping (Result<RemoteHost, Error>) -> Void) {
        call("remote.hosts.edit", edit, done)
    }

    /// Forgets a host, and removes the sessions added from it here (not on the host).
    func removeHost(_ name: String, done: @escaping (Error?) -> Void) {
        call("remote.hosts.remove", Ref(host: name)) { (result: Result<Nothing, Error>) in done(result.failure) }
    }

    /// Connects now, without waiting out the backoff.
    func retry(_ name: String, done: @escaping (Result<RemoteHost, Error>) -> Void) {
        call("remote.hosts.retry", Ref(host: name), done)
    }

    // MARK: Sessions

    func browse(_ host: String, done: @escaping (Result<RemoteBrowse, Error>) -> Void) {
        call("remote.browse", Ref(host: host), done)
    }

    /// Adds a host's session here, by its id there: done gets the session here.
    func addSession(host: String, id: String, done: @escaping (Result<Session, Error>) -> Void) {
        call("remote.sessions.add", Ref(host: host, id: id), done)
    }

    /// Removes an added session here, by its id here. The host keeps it.
    func removeSession(_ id: String, done: @escaping (Error?) -> Void) {
        call("remote.sessions.remove", Ref(id: id)) { (result: Result<Nothing, Error>) in done(result.failure) }
    }

    /// Starts a session on a host, Remote there, and adds it here.
    /// setWith is a session from the same host to put it in a set with (a split).
    func newSession(host: String, dir: String, harness: String?, setWith: String? = nil,
                    done: @escaping (Result<Session, Error>) -> Void) {
        call("remote.new", NewRequest(host: host, dir: dir, harness: harness, set_with: setWith), done)
    }

    // MARK: Folders and harnesses

    func recentFolders(host: String, done: @escaping (Result<RemoteFolderList, Error>) -> Void) {
        call("remote.folders.recent", Ref(host: host), done)
    }

    /// A folder's subfolders: path is absolute, or from the host's home ("" is home).
    func listFolders(host: String, path: String, hidden: Bool = false, done: @escaping (Result<RemoteFolderList, Error>) -> Void) {
        call("remote.folders.list", FolderRequest(host: host, path: path, hidden: hidden ? true : nil), done)
    }

    func makeFolder(host: String, path: String, done: @escaping (Result<RemoteFolder, Error>) -> Void) {
        call("remote.folders.mkdir", FolderRequest(host: host, path: path), done)
    }

    func harnesses(host: String, done: @escaping (Result<RemoteHarnesses, Error>) -> Void) {
        call("remote.harnesses", Ref(host: host), done)
    }
}

extension Result {
    var failure: Failure? {
        if case .failure(let error) = self { return error }
        return nil
    }
}

/// An error's message, as the daemon gave it.
func errorText(_ error: Error) -> String {
    (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
}
