import AppKit
import Foundation
import Testing

@testable import AgentCommand

/// Answers the daemon's Remote host ops itself, and records what it was asked.
/// It answers at once, unless held: then each answer waits for release.
final class FakeCalls: DaemonCalls {
    private(set) var asked: [(op: String, input: [String: Any])] = []
    var replies: [String: Any] = [:]
    var failures: [String: String] = [:]
    var holding = false
    private var held: [() -> Void] = []

    func call(_ op: String, _ input: Data, done: @escaping (Result<Data, Error>) -> Void) {
        asked.append((op, (try? JSONSerialization.jsonObject(with: input)) as? [String: Any] ?? [:]))
        let answer: Result<Data, Error>
        if let failure = failures[op] {
            answer = .failure(DaemonError(message: failure))
        } else {
            let data = (try? JSONSerialization.data(withJSONObject: replies[op] ?? [String: Any](), options: .fragmentsAllowed)) ?? Data()
            answer = .success(data)
        }
        if holding { held.append { done(answer) } } else { done(answer) }
    }

    /// Answers what's held, oldest first, or only the one at index.
    func release(_ index: Int? = nil) {
        if let index {
            held.remove(at: index)()
        } else {
            let all = held
            held = []
            all.forEach { $0() }
        }
    }

    var ops: [String] { asked.map(\.op) }
    var last: [String: Any] { asked.last?.input ?? [:] }
}

private func client(_ fake: FakeCalls) -> RemoteHostsClient { RemoteHostsClient(calls: fake) }

private let session: [String: Any] = [
    "id": "L9", "name": "api", "cwd": "/w/api", "status": "busy", "kind": "hosted", "unseen": false,
    "harness": "claude", "claude_session_id": "", "host": "studio", "remote_id": "s1",
]

private func connected(_ names: String...) -> [RemoteHost] { names.map { RemoteHost(name: $0, state: .connected) } }

// MARK: The client

@Test func eachRemoteOpSendsWhatTheDaemonReads() throws {
    let fake = FakeCalls()
    let c = client(fake)
    fake.replies = ["remote.new": session, "remote.hosts.add": ["name": "studio", "state": "connecting"]]
    var results: [String] = []
    func note<T>(_ r: Result<T, Error>) { results.append(r.failure == nil ? "ok" : "fail") }

    c.addHost(RemoteHostEdit(name: "studio", address: "s:7433", fingerprint: "SHA256:x")) { note($0) }
    #expect(fake.ops.last == "remote.hosts.add" && fake.last as? [String: String] == ["name": "studio", "address": "s:7433", "fingerprint": "SHA256:x"])
    c.editHost(RemoteHostEdit(host: "studio", name: "studio2")) { note($0) }
    #expect(fake.ops.last == "remote.hosts.edit" && fake.last as? [String: String] == ["host": "studio", "name": "studio2"])
    c.removeHost("studio") { results.append($0 == nil ? "ok" : "fail") }
    #expect(fake.ops.last == "remote.hosts.remove" && fake.last as? [String: String] == ["host": "studio"])
    c.retry("studio") { note($0) }
    #expect(fake.ops.last == "remote.hosts.retry" && fake.last as? [String: String] == ["host": "studio"])
    c.browse("studio") { note($0) }
    #expect(fake.ops.last == "remote.browse" && fake.last as? [String: String] == ["host": "studio"])
    c.newSession(host: "studio", dir: "~/src", harness: "claude") { note($0) }
    #expect(fake.ops.last == "remote.new" && fake.last as? [String: String] == ["host": "studio", "dir": "~/src", "harness": "claude"])
    c.newSession(host: "studio", dir: "/w", harness: nil) { note($0) }
    #expect(fake.last as? [String: String] == ["host": "studio", "dir": "/w"], "no harness is the host's default")
    c.recentFolders(host: "studio") { note($0) }
    #expect(fake.ops.last == "remote.folders.recent" && fake.last as? [String: String] == ["host": "studio"])
    c.listFolders(host: "studio", path: "") { note($0) }
    #expect(fake.ops.last == "remote.folders.list" && fake.last as? [String: String] == ["host": "studio", "path": ""])
    c.listFolders(host: "studio", path: "/w", hidden: true) { note($0) }
    #expect(fake.last["hidden"] as? Bool == true)
    c.makeFolder(host: "studio", path: "/w/new") { note($0) }
    #expect(fake.ops.last == "remote.folders.mkdir" && fake.last as? [String: String] == ["host": "studio", "path": "/w/new"])
    c.harnesses(host: "studio") { note($0) }
    #expect(fake.ops.last == "remote.harnesses" && fake.last as? [String: String] == ["host": "studio"])
    #expect(results.allSatisfy { $0 == "ok" } && results.count == 12)

    fake.failures["remote.browse"] = "there's no Remote host named \"x\""
    var message = ""
    c.browse("x") { if case .failure(let e) = $0 { message = errorText(e) } }
    #expect(message == "there's no Remote host named \"x\"", "the daemon's own words")
    fake.replies["remote.browse"] = "not an object"
    var failed = false
    fake.failures = [:]
    c.browse("x") { failed = $0.failure != nil }
    #expect(failed, "a reply that doesn't read is an error")
}

// MARK: The browser

@Suite @MainActor
struct RemoteBrowserTests {
    private func model(_ fake: FakeCalls, hosts: [RemoteHost] = connected("studio", "laptop"), host: String? = nil) -> RemoteBrowserModel {
        RemoteBrowserModel(client: client(fake), hosts: { hosts }, host: host)
    }

    private let listing: [String: Any] = ["host": "studio", "sessions": [
        ["id": "s1", "name": "api", "status": "busy", "cwd": "/w/api", "host": "studio"],
        ["id": "s2", "name": "web", "status": "done", "cwd": "/w/web", "host": "studio"],
    ]]

    @Test func itListsTheFirstHostsSessions() {
        let fake = FakeCalls()
        fake.replies["remote.browse"] = listing
        let m = model(fake)
        #expect(m.host?.name == "studio")
        m.load()
        #expect(fake.last as? [String: String] == ["host": "studio"])
        #expect(m.sessions.map(\.id) == ["s1", "s2"] && m.listed == "studio" && !m.loading && m.error == nil)
        m.choose(host: "studio")
        #expect(fake.asked.count == 1, "the host on show isn't listed again")
        m.choose(host: "laptop")
        #expect(m.host?.name == "laptop" && fake.last as? [String: String] == ["host": "laptop"])
    }

    @Test func aHostChosenFirstIsWhereItStarts() {
        _ = NSApplication.shared
        let fake = FakeCalls()
        let m = model(fake, host: "laptop")
        m.start()
        m.stop()
        #expect(m.host?.name == "laptop" && fake.last as? [String: String] == ["host": "laptop"])
        let none = model(fake, hosts: [])
        none.load()
        #expect(none.host == nil && fake.asked.count == 1, "nothing to list")
    }

    @Test func aHostsErrorShowsInline() {
        let fake = FakeCalls()
        fake.replies["remote.browse"] = listing
        let m = model(fake)
        m.load()
        fake.failures["remote.browse"] = "studio is unreachable"
        m.load(quiet: true)
        #expect(m.error == "studio is unreachable" && m.sessions.count == 2, "a refresh keeps what it showed")
        m.load()
        #expect(m.sessions.isEmpty && m.error == "studio is unreachable")
    }

    @Test func aLateListingIsIgnored() {
        let fake = FakeCalls()
        fake.holding = true
        fake.replies["remote.browse"] = listing
        let m = model(fake)
        m.load()
        m.choose(host: "laptop")
        fake.release(0) // studio's, after laptop was chosen
        #expect(m.sessions.isEmpty && m.loading)
        fake.release()
        #expect(m.listed == "laptop" && !m.loading)
    }

    /// A host's sessions are here already, in its tree: opening one shows it.
    @Test func openShowsIt() throws {
        let fake = FakeCalls()
        fake.replies["remote.browse"] = listing
        let m = model(fake)
        var focused: [String] = []
        m.focus = { focused.append($0) }
        m.load()
        m.open(m.sessions[1])
        m.open(m.sessions[0])
        #expect(focused == ["s2", "s1"] && fake.ops == ["remote.browse"], "by the host's own ids, with no call")
        #expect(m.sessions[0].dir == "/w/api" && m.sessions[0].added && m.sessions[0].localID == "s1")
        #expect(RemoteBrowserModel.hint(Keymap()).contains("return open  n new session"))
    }

    @Test func retryConnectsNow() {
        let fake = FakeCalls()
        let m = model(fake)
        m.retry(RemoteHost(name: "studio", state: .unreachable))
        #expect(fake.ops == ["remote.hosts.retry", "remote.browse"] && m.retrying.isEmpty)
        fake.failures["remote.hosts.retry"] = "no such host"
        m.retry(RemoteHost(name: "laptop"))
        #expect(m.error == "no such host" && fake.ops.last == "remote.hosts.retry", "another host isn't listed again")
    }

    @Test func theKeysMoveOpenAndAct() throws {
        let fake = FakeCalls()
        fake.replies["remote.browse"] = listing
        let hosts = [RemoteHost(name: "studio", state: .connected), {
            var h = RemoteHost(name: "laptop", state: .pending)
            h.ourFingerprint = "SHA256:me"
            return h
        }()]
        let m = model(fake, hosts: hosts)
        var closed = 0, copied: [String] = [], started: [String] = [], focused: [String] = []
        m.close = { closed += 1 }
        m.copy = { copied.append($0) }
        m.newSession = { started.append($0) }
        m.focus = { focused.append($0) }
        m.load()
        func press(_ text: String) -> Bool { m.handle(try! #require(KeyCombo(text))) }

        #expect(press("down") && m.host?.name == "laptop")
        #expect(press("down") && m.host?.name == "laptop", "stops at the end")
        #expect(press("c") && copied == ["SHA256:me"])
        #expect(press("up") && m.host?.name == "studio")
        #expect(press("c") && copied.count == 1, "nothing to copy while connected")
        #expect(press("r") && !fake.ops.contains("remote.hosts.retry"), "a connected host has nothing to retry")
        #expect(press("n") && started == ["studio"])
        #expect(press("right") && m.panel == .sessions && m.cursorSession?.id == "s1")
        #expect(press("down") && m.cursorSession?.id == "s2")
        #expect(press("down") && m.cursorSession?.id == "s2")
        #expect(press("return") && focused == ["s2"])
        #expect(press("up") && press("a") && !fake.ops.contains("remote.sessions.add"), "a isn't Add any more")
        #expect(press("left") && m.panel == .hosts && m.cursorSession == nil)
        #expect(press("return") && m.panel == .sessions)
        #expect(press("escape") && m.panel == .hosts && closed == 0)
        #expect(press("escape") && closed == 1)
        #expect(press("cmd+w") && closed == 2)
        #expect(!press("cmd+q"), "the menus get the rest of ⌘")
        #expect(press("ctrl+x") && press("z"), "nothing else goes behind the browser")

        m.setCursor(m.sessions[1])
        #expect(m.panel == .sessions && m.sessionCursor == 1)
        m.setCursor(RemoteSession(id: "nope"))
        #expect(m.sessionCursor == 1)
        #expect(press("r"), "a key with nothing to do is still the browser's")
    }

    @Test func withNoSessionsTheHostsKeepTheKeyboard() {
        let fake = FakeCalls()
        fake.replies["remote.browse"] = ["host": "studio", "sessions": []]
        let m = model(fake)
        m.load()
        #expect(m.handle(KeyCombo("right")!) && m.panel == .hosts)
        #expect(m.handle(KeyCombo("return")!) && m.panel == .hosts)
        let empty = model(fake, hosts: [])
        #expect(empty.handle(KeyCombo("down")!) && empty.host == nil)
    }
}

// MARK: ⌘⇧N's Where

@Suite @MainActor
struct NewSessionPickerTests {
    @Test func thisMacOffersEveryHarness() {
        let fake = FakeCalls()
        let p = NewSessionPicker(hosts: ["studio"], client: client(fake))
        #expect(p.host == nil && p.canMakeDefault && p.choice.harnesses == Harness.allCases && fake.asked.isEmpty)
        #expect(p.places == [nil, "studio"] && NewSessionPicker.title(nil) == "This Mac" && NewSessionPicker.title("studio") == "studio")
    }

    @Test func aHostsHarnessesComeFromIt() {
        let fake = FakeCalls()
        fake.replies["remote.harnesses"] = ["harnesses": [["name": "terminal"], ["name": "claude"]], "default": "claude"]
        let p = NewSessionPicker(hosts: ["studio"], host: "studio", client: client(fake))
        #expect(fake.ops == ["remote.harnesses"] && fake.last as? [String: String] == ["host": "studio"])
        #expect(p.host == "studio" && !p.canMakeDefault && !p.loading)
        #expect(p.choice.harnesses == [.terminal, .claude] && p.choice.chosen == .claude, "its default comes first")
        p.move(-1)
        #expect(p.choice.chosen == .terminal)
        p.select(.claude)
        #expect(p.choice.chosen == .claude)
        #expect(p.setSearch("1") == .terminal)
    }

    @Test func aHostThatCantSayIsAnInlineError() {
        let fake = FakeCalls()
        fake.failures["remote.harnesses"] = "studio is unreachable"
        let p = NewSessionPicker(hosts: ["studio"], host: "studio", client: client(fake))
        #expect(p.error == "studio is unreachable" && p.choice.harnesses.isEmpty)
        fake.failures = [:]
        fake.replies["remote.harnesses"] = ["harnesses": [["name": "codex"]]]
        p.setHost("studio")
        #expect(p.error == "studio has nothing this app can start.")
        p.setHost(nil)
        #expect(p.error == nil && p.choice.harnesses == Harness.allCases)
        #expect(NewSessionPicker(hosts: [], host: "unknown", client: client(fake)).host == nil, "an unknown host is this Mac")
    }

    @Test func tabAndTheArrowsChangeWhere() {
        let fake = FakeCalls()
        fake.holding = true
        let p = NewSessionPicker(hosts: ["studio", "laptop"], client: client(fake))
        #expect(p.handle(KeyCombo("tab")!) && p.host == "studio" && p.loading)
        #expect(p.handle(KeyCombo("right")!) && p.host == "laptop")
        fake.replies["remote.harnesses"] = ["harnesses": [["name": "claude"]]]
        fake.release(0) // studio's answer, too late
        #expect(p.loading && p.choice.harnesses.isEmpty)
        #expect(p.handle(KeyCombo("tab")!) && p.host == nil, "round to this Mac")
        #expect(p.handle(KeyCombo("shift+tab")!) && p.host == "laptop")
        #expect(p.handle(KeyCombo("left")!) && p.host == "studio")
        fake.release()
        _ = p.setSearch("cl")
        #expect(!p.handle(KeyCombo("left")!), "with something typed, left moves along it")
        #expect(!p.handle(KeyCombo("down")!))
        #expect(!NewSessionPicker(hosts: []).handle(KeyCombo("tab")!), "no hosts, nothing to change")
        #expect(HarnessPickerView.makesDefault(.claude, checked: true) && !HarnessPickerView.makesDefault(.claude, checked: true, host: "studio"))
    }
}

// MARK: The remote folder chooser

@Suite @MainActor
struct RemoteFolderChooserTests {
    private let home: [String: Any] = ["path": "/Users/me", "parent": "/Users", "home": "/Users/me", "folders": [
        ["name": "api", "path": "/Users/me/api"], ["name": "web", "path": "/Users/me/web"],
    ]]
    private let api: [String: Any] = ["path": "/Users/me/api", "parent": "/Users/me", "home": "/Users/me", "folders": []]

    private func chooser(_ fake: FakeCalls) -> RemoteFolderChooser {
        RemoteFolderChooser(host: "studio", client: client(fake))
    }

    @Test func itStartsWithRecentFolders() {
        let fake = FakeCalls()
        fake.replies["remote.folders.recent"] = ["home": "/Users/me", "folders": [["name": "api", "path": "/Users/me/api"]]]
        let c = chooser(fake)
        var chosen: [String] = []
        c.choose = { chosen.append($0) }
        c.start()
        #expect(fake.ops == ["remote.folders.recent"] && c.mode == .recent && c.place == "Recent folders on studio")
        #expect(c.rows == [.folder(RemoteFolder(name: "api", path: "/Users/me/api")), .browse])
        c.submit()
        #expect(chosen == ["/Users/me/api"], "a recent folder is chosen")
        c.setSearch("zzz")
        #expect(c.rows == [.browse])
        c.submit()
        #expect(fake.ops.last == "remote.folders.list" && fake.last as? [String: String] == ["host": "studio", "path": ""])
    }

    @Test func withNoRecentFoldersItBrowsesHome() {
        let fake = FakeCalls()
        fake.replies["remote.folders.recent"] = ["home": "/Users/me", "folders": []]
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        c.start()
        #expect(fake.ops == ["remote.folders.recent", "remote.folders.list"] && c.mode == .browse && c.place == "~")
        #expect(c.rows.map(\.id) == ["here:/Users/me", "up:/Users", "folder:/Users/me/api", "folder:/Users/me/web", "new"])

        fake.failures["remote.folders.recent"] = "unreachable"
        let d = chooser(fake)
        d.start()
        #expect(d.mode == .browse && d.error == nil, "the listing worked, so its error went")
        fake.failures["remote.folders.list"] = "unreachable"
        let e = chooser(fake)
        e.start()
        #expect(e.error == "unreachable" && e.rows == [.browse], "browsing can be tried again")
    }

    @Test func returnOpensAFolderAndChoosesHere() {
        let fake = FakeCalls()
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        var chosen: [String] = []
        c.choose = { chosen.append($0) }
        c.browse("")
        c.move(2)
        #expect(c.chosen == .folder(RemoteFolder(name: "api", path: "/Users/me/api")))
        fake.replies["remote.folders.list"] = api
        c.submit()
        #expect(fake.last as? [String: String] == ["host": "studio", "path": "/Users/me/api"] && c.place == "~/api" && c.selected == 0)
        #expect(c.rows.map(\.id) == ["here:/Users/me/api", "up:/Users/me", "new"])
        c.submit()
        #expect(chosen == ["/Users/me/api"])
        c.move(1)
        fake.replies["remote.folders.list"] = home
        c.submit()
        #expect(fake.last["path"] as? String == "/Users/me", "up")
        c.move(-5)
        #expect(c.selected == 0)
        c.move(50)
        #expect(c.chosen == .newFolder)
        c.select(.here("/Users/me"))
        #expect(c.selected == 0)
    }

    @Test func aFolderThatCantBeListedSaysWhy() {
        let fake = FakeCalls()
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        c.browse("")
        fake.failures["remote.folders.list"] = "permission denied: /Users/me/api"
        c.browse("/Users/me/api")
        #expect(c.error == "permission denied: /Users/me/api" && c.place == "~", "what was listed stays")
    }

    @Test func newFolderMakesItThenOpensIt() {
        let fake = FakeCalls()
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        c.startNaming()
        #expect(!c.naming, "nothing listed yet to make it in")
        c.browse("")
        c.move(50)
        c.submit()
        #expect(c.naming && c.rows.isEmpty && c.search.isEmpty)
        c.submit()
        #expect(c.error == "Type the new folder's name.")
        c.setSearch("a/b")
        c.submit()
        #expect(c.error == "A folder's name can't have / in it.")
        c.setSearch("..")
        c.submit()
        #expect(c.error == "Choose another name.")

        fake.failures["remote.folders.mkdir"] = "there's already something at /Users/me/api"
        c.setSearch("api")
        c.submit()
        #expect(fake.last as? [String: String] == ["host": "studio", "path": "/Users/me/api"])
        #expect(c.naming && c.error == "there's already something at /Users/me/api")

        fake.failures = [:]
        fake.replies["remote.folders.mkdir"] = ["name": "new", "path": "/Users/me/new"]
        fake.replies["remote.folders.list"] = ["path": "/Users/me/new", "parent": "/Users/me", "home": "/Users/me", "folders": []]
        c.setSearch(" new ")
        c.submit()
        #expect(fake.ops.suffix(2) == ["remote.folders.mkdir", "remote.folders.list"] && !c.naming && c.place == "~/new")

        c.startNaming()
        #expect(c.back() && !c.naming, "escape stops naming")
        #expect(!c.back(), "then it closes")
    }

    @Test func theKeysBrowse() {
        let fake = FakeCalls()
        fake.replies["remote.folders.recent"] = ["home": "/Users/me", "folders": [["name": "api", "path": "/Users/me/api"]]]
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        c.start()
        #expect(c.handle(KeyCombo("right")!) && c.mode == .browse, "right opens the recent folder")
        #expect(fake.last["path"] as? String == "/Users/me/api")
        #expect(c.handle(KeyCombo("tab")!) && c.mode == .recent)
        #expect(c.handle(KeyCombo("tab")!) && c.mode == .browse)
        #expect(c.handle(KeyCombo("left")!) && fake.last["path"] as? String == "/Users", "left goes up")
        #expect(c.handle(KeyCombo("shift+cmd+.")!) && c.showsHidden && fake.last["hidden"] as? Bool == true)
        #expect(c.handle(KeyCombo("cmd+n")!) && c.naming)
        #expect(!c.handle(KeyCombo("left")!), "while naming, left moves along the name")
        _ = c.back()
        c.setSearch("a")
        #expect(!c.handle(KeyCombo("right")!), "with something typed too")
        c.setSearch("")
        #expect(!c.handle(KeyCombo("down")!))
        c.toggleHidden()
        #expect(!c.showsHidden)
        c.fail("studio: no such folder")
        #expect(c.error == "studio: no such folder" && !c.loading)
    }

    @Test func aLateListingIsIgnored() {
        let fake = FakeCalls()
        fake.holding = true
        fake.replies["remote.folders.list"] = home
        let c = chooser(fake)
        c.browse("")
        c.browse("/Users/me/api")
        fake.release(0)
        #expect(c.listing == nil && c.loading)
    }

    @Test func pathsInTheHomeFolderAreShort() {
        #expect(RemoteFolderChooser.abbreviate("/Users/me/src", home: "/Users/me") == "~/src")
        #expect(RemoteFolderChooser.abbreviate("/Users/me", home: "/Users/me") == "~")
        #expect(RemoteFolderChooser.abbreviate("/Users/meow", home: "/Users/me") == "/Users/meow")
        #expect(RemoteFolderChooser.abbreviate("/srv", home: "") == "/srv")
        #expect(RemoteFolderChooser.Row.browse.id == "browse" && RemoteFolderChooser.Row.up("/a").id == "up:/a")
    }
}

// MARK: Known hosts

@Test func theHostFormSaysWhatsMissing() {
    var f = KnownHostForm()
    #expect(f.problem == "Give it a name." && !f.ready)
    f.name = " studio "
    #expect(f.problem == "Type its address, as host:port.")
    f.address = "studio.local:7433"
    #expect(f.problem == "Paste the host's fingerprint: acmd remote identity shows it.")
    f.fingerprint = "nope"
    #expect(f.problem == "That doesn't look like a fingerprint (SHA256:…).")
    f.fingerprint = "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde"
    #expect(f.ready)
    #expect(f.addition == RemoteHostEdit(name: "studio", address: "studio.local:7433", fingerprint: f.fingerprint))
    f.byFile = true
    #expect(f.problem == "Choose the host's cert file.")
    f.cert = "/c/host.pem"
    #expect(f.ready && f.addition == RemoteHostEdit(name: "studio", address: "studio.local:7433", cert: "/c/host.pem"))
}

@Test func editingAHostSendsOnlyWhatChanged() {
    var host = RemoteHost(name: "studio", address: "s:7433")
    host.fingerprint = "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde"
    var f = KnownHostForm(host: host)
    #expect(!f.byFile && f.fingerprint == host.fingerprint && f.name == "studio")
    #expect(f.changes(from: host) == nil, "nothing changed")
    f.name = "studio2"
    f.address = "s:7434"
    #expect(f.changes(from: host) == RemoteHostEdit(host: "studio", name: "studio2", address: "s:7434"))
    f.byFile = true
    f.cert = "/c.pem"
    #expect(f.changes(from: host)?.cert == "/c.pem" && f.changes(from: host)?.fingerprint == nil)

    var pinnedByFile = host
    pinnedByFile.cert = "/old.pem"
    var g = KnownHostForm(host: pinnedByFile)
    #expect(g.byFile && g.cert == "/old.pem" && g.fingerprint.isEmpty)
    #expect(g.changes(from: pinnedByFile) == nil)
    g.byFile = false
    g.fingerprint = host.fingerprint
    #expect(g.changes(from: pinnedByFile)?.fingerprint == host.fingerprint, "from a file to a fingerprint")
}

@Suite @MainActor
struct KnownHostsModelTests {
    private func ready() -> KnownHostForm {
        var f = KnownHostForm()
        (f.name, f.address, f.fingerprint) = ("studio", "s:7433", "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789+/abcde")
        return f
    }

    @Test func addEditRemoveAndRetryGoThroughTheDaemon() {
        let fake = FakeCalls()
        let m = KnownHostsModel()
        m.client = client(fake)
        var done: [Bool] = []
        m.add(KnownHostForm()) { done.append($0) }
        #expect(fake.asked.isEmpty && m.error == "Give it a name." && done == [false])
        m.add(ready()) { done.append($0) }
        #expect(fake.ops == ["remote.hosts.add"] && fake.last["name"] as? String == "studio" && done.last == true && m.busy == nil)

        let host = RemoteHost(name: "studio", address: "s:7433")
        var form = KnownHostForm(host: host)
        form.fingerprint = ready().fingerprint
        m.edit(host, form) { done.append($0) }
        #expect(fake.ops.last == "remote.hosts.edit" && fake.last["host"] as? String == "studio" && done.last == true)
        var same = host
        same.fingerprint = form.fingerprint
        m.edit(same, form) { done.append($0) }
        #expect(fake.asked.count == 2 && done.last == true, "nothing changed: nothing to send")
        m.edit(host, KnownHostForm()) { done.append($0) }
        #expect(done.last == false && m.error == "Give it a name.")

        fake.failures["remote.hosts.remove"] = "there's no Remote host named \"studio\""
        m.remove(host)
        #expect(fake.ops.last == "remote.hosts.remove" && m.error == "there's no Remote host named \"studio\"")
        m.retry(host)
        #expect(fake.ops.last == "remote.hosts.retry" && m.error == nil && m.busy == nil)
        fake.failures["remote.hosts.add"] = "a Remote host named \"studio\" is already known"
        m.add(ready()) { done.append($0) }
        #expect(done.last == false && m.error == "a Remote host named \"studio\" is already known")
        m.clearError()
        #expect(m.error == nil)

        fake.holding = true
        m.retry(host)
        #expect(m.busy == "studio")
        m.add(ready())
        #expect(m.busy == "")
        fake.release()
        #expect(m.busy == nil)
    }

    @Test func removingAHostSaysWhatHappensToItsSessions() {
        var host = RemoteHost(name: "studio")
        #expect(KnownHostsModel.removePrompt(host).title == "Remove studio?")
        #expect(KnownHostsModel.removePrompt(host).note == "This Mac stops connecting to it. Nothing on studio changes.")
        host.sessions = 1
        #expect(KnownHostsModel.removePrompt(host).note.hasPrefix("The session added from it is removed from this Mac only."))
        host.sessions = 3
        #expect(KnownHostsModel.removePrompt(host).note.hasPrefix("The 3 sessions added from it are removed from this Mac only. They keep running on studio"))
    }
}
