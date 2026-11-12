import Foundation
import Testing

@testable import AgentCommand

private func session(_ status: String, harness: String = "claude", claude: String = "c", detail: String? = nil,
                     description: String? = nil, cwd: String = "/tmp") throws -> Session {
    var s = try makeSession(id: UUID().uuidString, cwd: cwd, harness: harness, status: status, claude: claude)
    s.statusDetail = detail
    s.description = description
    return s
}

@Test func theStatusBarSumsUpWhatNeedsYou() throws {
    #expect(StatusSummary.parts([]).isEmpty)
    #expect(StatusSummary.parts([try session("done"), try session("paused")]).isEmpty, "nothing to say")
    let parts = StatusSummary.parts([
        try session("waiting"), try session("error"), try session("busy"), try session("busy"),
        try session("archived"), try session("done"),
    ])
    #expect(parts == [.init(text: "2 needs you", needsYou: true), .init(text: "2 busy", needsYou: false)])
    #expect(StatusSummary.parts([try session("busy")]) == [.init(text: "1 busy", needsYou: false)])
}

@Test func aRowMarksClaude() throws {
    #expect(RowMark(try session("done")) == .claude(dimmed: false, busy: false))
    #expect(RowMark(try session("busy")) == .claude(dimmed: false, busy: true))
    #expect(RowMark(try session("paused")) == .claude(dimmed: true, busy: false), "greyed out while paused")
    #expect(RowMark(try session("finished")) == .claude(dimmed: true, busy: false))
    #expect(RowMark(try session("busy", harness: "terminal")) == .claude(dimmed: false, busy: true),
            "Claude typed in a terminal")
    #expect(RowMark(try session("unknown", harness: "terminal", claude: "", detail: "claude"))
        == .claude(dimmed: true, busy: false), "Claude without hooks: its status isn't known")
    #expect(RowMark(try session("running", harness: "terminal", claude: "")) == .terminal)
    #expect(RowMark(try session("unknown", harness: "terminal", claude: "", detail: "opencode")) == .dot)
}

@Test func aSessionDescribesItself() throws {
    let s = try session("waiting", detail: "permission: Write", description: "the auth fix", cwd: "/w/api")
    #expect(s.statusLine == "waiting · permission: Write")
    #expect(s.details == "api\nwaiting · permission: Write\nthe auth fix\n/w/api\nClaude session c")
    #expect(try session("done").statusLine == "done")
    #expect(try session("done", detail: "").statusLine == "done")
}

@Test func aSessionWithoutDetailsDecodes() throws {
    let s = try session("done")
    #expect(s.statusDetail == nil && s.description == nil, "older daemons don't send them")
    let fields: [String: Any] = ["ID": "a", "Name": "a", "Cwd": "/", "Status": "busy", "Kind": "hosted", "Unseen": false,
                                 "Harness": "claude", "ClaudeSessionID": "c", "StatusDetail": "Bash", "Description": "d"]
    let decoded = try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
    #expect(decoded.statusDetail == "Bash" && decoded.description == "d")
}

@Test func theClaudeMarkIsTheCrab() {
    let rows = ClaudeMarkShape.pixels
    #expect(Set(rows.map(\.count)) == [16], "every row is the same width")
    #expect(rows[1] == "  ## ###### ##  ", "two eyes")
    #expect(rows[2].allSatisfy { $0 == "#" }, "arms out to the sides")
    #expect(rows[4].filter { $0 == "#" }.count == 4, "four legs")

    // It fits the rect it's given, centered and keeping its shape.
    let rect = CGRect(x: 0, y: 0, width: 13, height: 10)
    let bounds = ClaudeMarkShape().path(in: rect).boundingRect
    #expect(abs(bounds.width / bounds.height - ClaudeMarkShape.aspect) < 0.01)
    #expect(rect.insetBy(dx: -0.01, dy: -0.01).contains(bounds))
    #expect(abs(bounds.midX - rect.midX) < 0.01 && abs(bounds.midY - rect.midY) < 0.01)
}
