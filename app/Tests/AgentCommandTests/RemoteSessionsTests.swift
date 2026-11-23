import AppKit
import Foundation
import Testing

@testable import AgentCommand

extension UsesSharedSpeech {
    /// Remote sessions in the store, the sidebar's filter, the palette and the Inbox.
    @Suite @MainActor
    struct RemoteSessionsTests {
        private func store() throws -> SessionStore {
            _ = NSApplication.shared
            try isolateFromTheRealDaemon()
            let s = SessionStore()
            s.defaults = try #require(UserDefaults(suiteName: "acmd.tests.store.\(UUID().uuidString)"))
            s.filter = .all
            s.sessions = [
                try makeRemoteSession(id: "a"), try makeRemoteSession(id: "b", remote: true),
                try makeRemoteSession(id: "c", host: "studio"), try makeRemoteSession(id: "d", host: "laptop", status: "waiting"),
            ]
            return s
        }

        private func snapshot(_ fields: [String: Any]) throws -> Snapshot {
            try JSONDecoder().decode(Snapshot.self, from: JSONSerialization.data(withJSONObject: fields))
        }

        @Test func theFilterNarrowsTheSidebarAndItsKeys() throws {
            let s = try store()
            #expect(s.sidebarIDs == ["a", "b", "c", "d"] && s.filterHosts == ["studio", "laptop"])
            s.filter = .local
            #expect(s.sidebarIDs == ["a", "b"] && s.sessionID(number: 2) == "b" && s.sessionID(number: 3) == nil)
            #expect(SidebarFilter.load(s.defaults) == .local, "it's kept")
            s.filter = .host("laptop")
            #expect(s.sidebarIDs == ["d"])
            #expect(s.attentionIDs == ["d"], "what needs you counts whatever the filter shows")
            s.filter = .local
            #expect(s.attentionIDs == ["d"] && s.nextAttentionID == "d")
            s.show("d")
            #expect(s.filter == .all && s.selectedID == "d", "showing a hidden session lists everything again")
            s.filter = .host("gone")
            #expect(s.shownFilter == .all && s.sidebarIDs.count == 4, "a host that's gone lists everything")
        }

        @Test func theSnapshotCarriesTheHosts() async throws {
            let s = try store()
            s.publish(try snapshot(["remote_hosts": [["name": "studio", "state": "connected"], ["name": "laptop", "state": "unreachable"]]]))
            try await until { s.remoteHosts.count == 2 }
            let c = try makeRemoteSession(id: "c", host: "studio")
            #expect(s.place(of: c).note == nil && s.place(of: c).hostBadge == "↗ studio")
            #expect(s.place(of: try makeRemoteSession(id: "d", host: "laptop")).note == "reconnecting…")
            let b = try makeRemoteSession(id: "b", remote: true)
            #expect(s.place(of: b, clients: ["b": 3]).shared == "shared · 3")
        }

        /// A host's items keep the host's own ids, which can be the same as this
        /// Mac's: each host's new ones are news, told apart by host.
        @Test func newItemsFromHostsAreNewsToo() async throws {
            let s = try store()
            var arrived: [InboxKey] = []
            s.onNewItems = { arrived += $0.map(\.id) }
            func item(_ id: Int, on host: String = "studio") -> [String: Any] {
                ["id": id, "session_id": "c", "kind": "done", "state": "open", "host": host]
            }
            s.publish(try snapshot(["inbox": [item(1), item(1, on: "")]]))
            try await until { s.queue.count == 2 }
            #expect(arrived.isEmpty, "items from before launch aren't news")
            #expect(Set(s.queue.map(\.id)).count == 2, "the same id on two machines is two items")
            s.publish(try snapshot(["inbox": [item(1), item(1, on: ""), item(2)]]))
            try await until { s.queue.count == 3 }
            #expect(arrived == [InboxKey(host: "studio", id: 2)] && s.queue.last?.host == "studio")
            s.publish(try snapshot(["inbox": [item(1), item(1, on: ""), item(2), item(2, on: "")]]))
            try await until { s.queue.count == 4 }
            #expect(arrived.last == 2, "this Mac's 2 is news of its own")
        }

        @Test func startingOnAHostGoesThroughTheClient() throws {
            let s = try store()
            let fake = FakeCalls()
            s.remoteClient = RemoteHostsClient(calls: fake)
            s.mutations = fake

            fake.replies["remote.new"] = [
                "id": "N1", "name": "fresh", "cwd": "/w", "status": "busy", "kind": "hosted", "unseen": false,
                "harness_kind": "claude", "claude_session_id": "", "host": "studio", "remote_id": "r1",
            ]
            var failure: String? = "unset"
            s.newRemoteSession(host: "studio", dir: "/w", harness: .claude) { failure = $0 }
            #expect(failure == nil && s.selectedID == "N1" && s.sessions.last?.id == "N1")
            #expect(fake.last as? [String: String] == ["host": "studio", "dir": "/w", "harness_id": "", "harness_kind": "claude"])
            s.newRemoteSession(host: "studio", dir: "/w", harness: nil) { _ in }
            #expect(s.sessions.filter { $0.id == "N1" }.count == 1, "listed once")
            fake.failures["remote.new"] = "no such folder: /w"
            s.newRemoteSession(host: "studio", dir: "/w", harness: nil) { failure = $0 }
            #expect(failure == "no such folder: /w")
        }

        @Test func remoteOnAndOffAndNewSessionsReachTheDaemon() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            let s = try store()
            let fake = FakeCalls()
            s.mutations = fake
            s.setRemote(s.sessions, true)
            #expect(fake.ops == ["mutate"] && fake.last["kind"] as? String == "session.remote" && fake.last["flag"] as? Bool == true)
            #expect(targetIDs(fake.last) == ["a", "b"] && targetHosts(fake.last) == ["", ""], "this Mac's only")

            daemon.reply("new", [
                "id": "Z", "name": "z", "cwd": "/tmp", "status": "busy", "kind": "hosted", "unseen": false,
                "harness_kind": "terminal", "claude_session_id": "",
            ])
            s.newSession(harness: .terminal, in: "/tmp", remote: true)
            s.newSession(harness: .claude, in: "/tmp")
            try await until { daemon.received("new").count == 2 }
            let news = daemon.received("new")
            #expect(news[0]["remote"] as? Bool == true)
            #expect(news[1]["remote"] == nil, "⌘N leaves it to the daemon's config")
            try await until { s.selectedID == "Z" }
        }

        @Test func theClientTalksToTheDaemonOnItsSocket() async throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("remote.browse", ["host": "studio", "sessions": [["id": "s1"]]])
            var got: RemoteBrowse?
            RemoteHostsClient().browse("studio") { got = try? $0.get() }
            try await until { got != nil }
            #expect(got?.sessions.first?.id == "s1" && daemon.received("remote.browse").first?["host"] as? String == "studio")
            daemon.fail("remote.harnesses", "studio is unreachable")
            var failure: String?
            RemoteHostsClient().harnesses(host: "studio") { failure = $0.failure.map(errorText) }
            try await until { failure != nil }
            #expect(failure == "studio is unreachable")
        }

        @Test func theInboxNamesTheHost() throws {
            let s = try store()
            let inbox = InboxModel(store: s, filter: .queue)
            #expect(inbox.sessionName("c") == "c ↗ studio" && inbox.sessionName("a") == "a" && inbox.sessionName("zz") == "Deleted session")
        }

        @Test func thePaletteTurnsRemoteOnAndOff() throws {
            let local = try makeRemoteSession(id: "a")
            let shared = try makeRemoteSession(id: "b", remote: true)
            let added = try makeRemoteSession(id: "c", host: "studio")
            let gone = try makeRemoteSession(id: "d", host: "studio", gone: true)
            func entries(_ targets: [Session], hosts: [String] = []) -> [PaletteEntry] {
                Palette.entries(PaletteState(targets: targets, sessions: targets, hosts: hosts))
            }
            #expect(entries([local]).first { $0.title == "Turn Remote on" }?.action == .setRemote([local], true))
            #expect(entries([shared]).first { $0.title == "Turn Remote off" }?.action == .setRemote([shared], false))
            #expect(!entries([added]).contains { $0.title.hasPrefix("Turn Remote") }, "not for a session from a host")
            #expect(!entries([added]).contains { $0.title == "Open in editor" }, "its folder isn't on this Mac")
            #expect(entries([added]).contains { $0.title == "Archive…" }, "Archive, which archives it on its host")
            #expect(!entries([added, gone]).contains { $0.title == "Remove from this Mac" }, "a host's sessions are its own")
            let several = entries([local, shared, added, gone])
            #expect(several.first { $0.title == "Turn Remote on" }?.action == .setRemote([local, shared], true))
            #expect(entries([shared, try makeRemoteSession(id: "e", remote: true)]).contains { $0.title == "Turn Remote off" })

            let withHosts = entries([], hosts: ["studio"])
            #expect(withHosts.first { $0.title == "Session on studio…" }?.action == .newOnHost("studio"))
            #expect(withHosts.first { $0.title == "Remote hosts…" }?.action == .remoteHosts)
        }
    }
}
