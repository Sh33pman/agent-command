import Foundation
import Testing

@testable import AgentCommand

/// The daemon's answers as Go writes them (internal/ipc/testdata/wire): a field
/// renamed on either side fails here and in Go's tests.
private func fixture(_ name: String) throws -> Data {
    let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().deletingLastPathComponent()
    return try Data(contentsOf: root.appendingPathComponent("internal/ipc/testdata/wire/\(name)"))
}

@Test func theSnapshotDecodesAsTheDaemonWritesIt() throws {
    let snap = try JSONDecoder().decode(Snapshot.self, from: fixture("snapshot.json"))
    let sessions = try #require(snap.sessions)
    #expect(sessions.map(\.id) == ["s1", "s2", "s3", "s4", "s5"] && sessions.allSatisfy { $0.id.hasPrefix("s") })
    // s5 is in a folder, a type the app doesn't know: it shows in the folder's
    // group (design 7.2).
    #expect(snap.others?.first?.type == "folder" && (sessions[4].groupID ?? "").isEmpty)
    #expect(snap.sessionsInGroups().first { $0.id == "s5" }?.groupID == "g1")
    let api = sessions[0]
    #expect(api.name == "api" && api.cwd == "/w/api" && api.status == "waiting" && api.statusDetail == "Allow Bash?")
    #expect(api.unseen && api.isRemote && api.claudeSessionID == "c-1" && api.groupID == "g1" && api.description == "fix the login bug")
    #expect(api.attention?.reason == "waiting" && api.runningAgent == .claude && api.can(.attach) && api.displayName == "api")
    #expect(api.lastEventAt == "2026-11-20T09:01:00Z")
    let docs = sessions[1]
    #expect(docs.parent == "set1" && docs.order == 2 && !docs.unseen && docs.statusDetail == nil, "left out: not set")
    let build = sessions[3]
    #expect(build.isOnHost && build.hostName == "studio" && build.remoteID == "r9" && build.jobID == "j1")
    #expect(snap.sets?.first?.id == "set1" && snap.sets?.first?.parent == "g1")

    let groups = try #require(snap.groups)
    #expect(groups[0] == SessionGroup(id: "g1", name: "Backend", color: "blue", order: 10))
    #expect(groups[1].isDefault && groups[1].order == 0 && !groups[1].isArchived)

    #expect(snap.inbox?.first?.options == ["Yes", "No"] && snap.inbox?.first?.sessionID == "s1")
    let job = try #require(snap.jobs?.first)
    #expect(job.name == "nightly" && job.form?.kind == "daily" && job.form?.hour == 2)
    #expect(snap.settings?["speech.enabled"] == .bool(true))
    #expect(snap.health?.first?.id == "hooks" && snap.features?.host.os == "darwin" && snap.speech?.voice == "af_bella")
}

@Test func answersDecodeAsTheDaemonWritesThem() throws {
    let s = try JSONDecoder().decode(Session.self, from: fixture("session.json"))
    #expect(s.id == "s1" && s.status == "waiting")
    let results = try JSONDecoder().decode(BatchResults.self, from: fixture("results.json"))
    #expect(results.results.map(\.ok) == [true, false, false] && results.results[1].code == "host_unreachable")
    let error = try JSONSerialization.jsonObject(with: fixture("error.json")) as? [String: Any]
    #expect(error?["ok"] as? Bool == false && error?["code"] as? String == "not_supported")
    // The daemon's hello: the version agreed, its range and its ops.
    let hello = try JSONDecoder().decode(DaemonHello.self, from: fixture("hello.json"))
    #expect(hello.proto == 1 && hello.protoMin == 1 && hello.protoMax == 2 && hello.ops.contains("snapshot"))
    #expect((DaemonConnection.protoMin...DaemonConnection.proto).contains(hello.proto), "the app speaks the version agreed")
    let old = try JSONDecoder().decode(DaemonHello.self, from: Data(#"{"proto": 1, "version": "0.70.0"}"#.utf8))
    #expect(old.protoMin == 1 && old.protoMax == 1 && old.ops.isEmpty, "a daemon from before ranges speaks its one version")
}

/// A session in a container of a type the app doesn't know goes in its
/// nearest group, on its own host; with none, in its host's Default.
@Test func sessionsInUnknownContainersFindTheirGroup() throws {
    let json = #"""
    {"sessions": [
      {"id": "a", "name": "a", "parent": "f2", "kind": "hosted", "status": "done"},
      {"id": "b", "name": "b", "parent": "w1", "kind": "hosted", "status": "done"},
      {"id": "c", "name": "c", "parent": "f1", "host": "studio", "kind": "hosted", "status": "done"},
      {"id": "d", "name": "d", "parent": "loop", "kind": "hosted", "status": "done"},
      {"id": "e", "name": "e", "group_id": "g1", "parent": "f2", "kind": "hosted", "status": "done"}],
     "groups": [{"id": "g1", "name": "One"}, {"id": "g1", "name": "Theirs", "host": "studio"}],
     "others": [{"type": "folder", "id": "f1", "parent": "g1"}, {"type": "folder", "id": "f2", "parent": "f1"},
                {"type": "workspace", "id": "w1"}, {"type": "folder", "id": "f1", "host": "studio", "parent": "g1"},
                {"type": "folder", "id": "loop", "parent": "loop"}]}
    """#
    let snap = try JSONDecoder().decode(Snapshot.self, from: Data(json.utf8))
    let placed = Dictionary(uniqueKeysWithValues: snap.sessionsInGroups().map { ($0.id, $0.groupID) })
    #expect(placed["a"] == "g1", "two folders up")
    #expect(placed["b"] == .some(nil), "a container with no group above it: its host's Default")
    #expect(placed["c"] == "g1", "on its own host")
    #expect(placed["d"] == .some(nil), "a loop ends")
    #expect(placed["e"] == "g1", "a group it names stands")
}
