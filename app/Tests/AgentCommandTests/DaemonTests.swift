import Foundation
import Testing

@testable import AgentCommand

private func decode<T: Decodable>(_ type: T.Type, _ fields: [String: Any]) throws -> T {
    try JSONDecoder().decode(T.self, from: JSONSerialization.data(withJSONObject: fields))
}

private func iso(_ secondsAgo: TimeInterval) -> String {
    ISO8601DateFormatter().string(from: Date().addingTimeInterval(-secondsAgo))
}

@Test func anItemDescribesItself() throws {
    let question = try makeItem(1, session: "s", kind: "question", options: ["Yes"])
    #expect(question.kindLabel == "Question" && question.needsOption && question.isOpen && question.stateLabel == "Open")
    #expect(try makeItem(1, session: "s", kind: "permission").kindLabel == "Permission")
    #expect(try makeItem(1, session: "s", kind: "error").kindLabel == "Error")
    let done = try makeItem(1, session: "s")
    #expect(done.kindLabel == "Finished" && !done.needsOption)
    for (state, label) in [("responded", "Responded"), ("dismissed", "Dismissed"), ("stale", "No longer relevant")] {
        let item = try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "done", "state": state])
        #expect(item.stateLabel == label && !item.isOpen)
    }
    let parts = try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "question", "multipart": true])
    #expect(!parts.needsOption, "a question in several parts can't be answered with one option")
}

@Test func anItemsPreviewAndTextRender() throws {
    let item = try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "done", "text": "First **line**\nsecond"])
    #expect(item.preview == "First line", "a finished turn's preview drops its Markdown")
    let heading = try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "done", "text": "## Summary\nmore"])
    #expect(heading.preview == "Summary")
    let permission = try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "permission", "text": "rm *.log"])
    #expect(permission.preview == "rm *.log" && !permission.isMarkdown, "acmd's own text is shown as it is")
    let plain = String(item.rendered.characters)
    #expect(plain.contains("First line") && plain.contains("second"), "bold goes, and the lines stay")
    #expect(try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "done"]).preview.isEmpty)
}

@Test func anItemsAgeIsShort() throws {
    func age(_ at: String) throws -> String {
        try decode(InboxItem.self, ["id": 1, "session_id": "s", "kind": "done", "at": at]).age
    }
    #expect(try age(iso(5)) == "now")
    #expect(try age(iso(5 * 60)) == "5m")
    #expect(try age(iso(3 * 3600)) == "3h")
    #expect(try age(iso(2 * 86400)) == "2d")
    #expect(try age(iso(-500)) == "now", "a clock ahead isn't negative")
    #expect(try age("not a date").isEmpty)
    #expect(try age("2020-01-01T00:00:00.123Z").hasSuffix("d"), "fractional seconds parse too")
}

@Test func aNoticeFillsInWhatItOmits() throws {
    let notice = try decode(Notice.self, ["seq": 4, "session_id": "s", "kind": "withdraw"])
    #expect(notice.isWithdrawal && !notice.isForApp && notice.title.isEmpty && notice.sound.isEmpty)
    let shown = try decode(Notice.self, ["seq": 5, "session_id": "s", "kind": "done", "emitter": "app", "title": "T"])
    #expect(!shown.isWithdrawal && shown.isForApp && shown.title == "T")
}

@Test func aConversationCanBeFound() throws {
    let c = try decode(Conversation.self, [
        "id": "ABC123", "cwd": "/work/Parser", "title": "Fix the Lexer", "branch": "main",
        "updated_at": "2026-11-07T10:00:00Z", "running": true,
    ])
    #expect(c.displayTitle == "Fix the Lexer" && c.isRunning && c.updated != nil)
    for found in ["", "  ", "lexer", "PARSER", "main", "abc"] { #expect(c.matches(found), "\(found)") }
    #expect(!c.matches("nothing like it"))
    let bare = try decode(Conversation.self, ["id": "x", "cwd": "/", "updated_at": "2026-11-07T10:00:00.5Z"])
    #expect(bare.displayTitle == "Untitled" && !bare.isRunning && bare.updated != nil)
    #expect(try decode(Conversation.self, ["id": "x", "cwd": "/", "updated_at": "nope"]).updated == nil)
}

@Test func aConversationSaysWhoseItIs() throws {
    let older = try decode(Conversation.self, ["id": "x", "cwd": "/", "updated_at": "2026-11-07T10:00:00Z"])
    #expect(older.harness == nil && older.harnessKind == .claude, "an older daemon sends no harness: Claude's")
    let claude = try decode(Conversation.self, ["id": "x", "harness": "claude", "cwd": "/", "updated_at": "2026-11-07T10:00:00Z"])
    #expect(claude.harnessKind == .claude)
    let gravity = try decode(Conversation.self, ["id": "y", "harness": "antigravity", "cwd": "/w", "updated_at": "2026-11-07T10:00:00Z"])
    #expect(gravity.harnessKind == .antigravity)
    #expect(gravity.matches("antigrav") && !older.matches("antigrav"), "the agent's name finds it")
}

@Test func setupOffersTheHooksThatAreMissing() {
    func need(_ claude: Bool, _ claudeCurrent: Bool = true, agy: Bool = false, hooked: Bool = false, current: Bool = false) -> SetupNeed {
        SetupNeed(claudeHooked: claude, claudeCurrent: claudeCurrent, agyInstalled: agy, agyHooked: hooked, agyCurrent: current)
    }
    #expect(need(true) == .none, "no agy: nothing about it")
    #expect(need(true, false) == .refresh, "the app moved")
    #expect(need(false) == .offer(claude: true, antigravity: false))
    #expect(need(false, agy: true) == .offer(claude: true, antigravity: true))
    #expect(need(true, agy: true) == .offer(claude: false, antigravity: true), "agy installed since")
    #expect(need(true, agy: true, hooked: true, current: true) == .none)
    #expect(need(true, agy: true, hooked: true, current: false) == .refresh, "agy's hook runs an old acmd")
    #expect(need(true, agy: false, hooked: true, current: false) == .none, "agy's gone: leave its hook be")

    #expect(need(false).offerText.contains("to your Claude Code settings, so"))
    #expect(need(false, agy: true).offerText.contains("to your Claude Code settings and Antigravity's hooks.json, so"))
    #expect(need(true, agy: true).offerText.contains("to Antigravity's hooks.json, so"))
    #expect(need(true).offerText.isEmpty && need(true, false).offerText.isEmpty)
    #expect(need(false).isFirstSetup && need(false, agy: true).isFirstSetup)
    #expect(!need(true, agy: true).isFirstSetup && !need(true).isFirstSetup, "waking the Mac was offered the first time")
}

@Test func agyIsFoundWhereItsInstalled() {
    let home = "/Users/me"
    let there: Set<String> = ["/Users/me/.local/bin/agy"]
    #expect(DaemonPaths.agyPath(["PATH": "/usr/bin:/bin"], home: home) { there.contains($0) } == "/Users/me/.local/bin/agy",
            "the Dock's short PATH doesn't have it, the installer's folder does")
    #expect(DaemonPaths.agyPath(["PATH": "/opt/x::/usr/bin"], home: home) { $0 == "/opt/x/agy" } == "/opt/x/agy", "PATH first")
    #expect(DaemonPaths.agyPath([:], home: home) { $0 == "/opt/homebrew/bin/agy" } == "/opt/homebrew/bin/agy")
    #expect(DaemonPaths.agyPath([:], home: home) { _ in false } == nil)
}

@Test func agysHooksAreReadFromItsNamedHook() {
    let ours = """
        \u{FEFF}{"mine": {"Stop": [{"type": "command", "command": "say hi # managed by acmd"}]},
         "agent-command": {"enabled": true, "SessionStart": [{"type": "command", "timeout": 5,
           "command": "[ -x /A/acmd ] && /A/acmd hook antigravity:SessionStart || echo '{}' # managed by acmd"}],
           "PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "/A/acmd hook antigravity:PostToolUse"}]}]}}
        """
    let commands = DaemonPaths.agyHooks(ours)
    #expect(commands.count == 2 && commands.contains { $0.contains(DaemonPaths.hookMarker) })
    #expect(commands.allSatisfy { !$0.hasPrefix("say") }, "another named hook isn't ours, even with the marker")
    #expect(DaemonPaths.agyHooks(#"{"mine": {}}"#).isEmpty)
    #expect(DaemonPaths.agyHooks("").isEmpty && DaemonPaths.agyHooks("not json").isEmpty)
    #expect(DaemonPaths.agyHooks(#"{"agent-command": 3}"#).isEmpty)
}

extension UsesSharedSpeech {
    /// The real connection against a stand-in for the daemon.
    @Suite struct DaemonConnectionTests {
        private struct Empty: Codable {}
        private struct Named: Codable, Equatable { let name: String }

        @Test func callsSendTheirDataAndDecodeTheReply() throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.reply("thing", ["name": "back"])
            let conn = try DaemonConnection(socket: DaemonPaths.socket)
            let out: Named = try conn.call("thing", Named(name: "there"))
            #expect(out == Named(name: "back"))
            #expect(daemon.received("thing").first?["name"] as? String == "there")
            let again: Named = try conn.call("thing")
            #expect(again.name == "back")
            try conn.send("other", Named(name: "x"))
            #expect(daemon.received("other").count == 1)
        }

        @Test func aFailedCallSaysWhy() throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.fail("thing", "no such session")
            let conn = try DaemonConnection(socket: DaemonPaths.socket)
            #expect(throws: DaemonError.self) { try conn.send("thing", Empty()) }
            do {
                try conn.send("thing", Empty())
            } catch {
                #expect(error.localizedDescription == "no such session")
            }
        }

        @Test func anotherProtocolIsRefused() throws {
            let daemon = try FakeDaemon()
            defer { daemon.stop() }
            daemon.proto = 2
            #expect(throws: DaemonError.self) { try DaemonConnection(socket: DaemonPaths.socket) }
        }

        @Test func noDaemonOrABadPathIsAnError() throws {
            try isolateFromTheRealDaemon()
            #expect(throws: DaemonError.self) { try DaemonConnection(socket: DaemonPaths.socket + ".none") }
            #expect(throws: DaemonError.self) { try DaemonConnection(socket: "/tmp/" + String(repeating: "x", count: 200)) }
        }

        @Test func aDaemonThatGoesAwayIsNoticed() throws {
            let daemon = try FakeDaemon()
            let conn = try DaemonConnection(socket: DaemonPaths.socket)
            try conn.send("thing", Empty())
            daemon.stop()
            // More than once: writing to a closed socket mustn't end the app with SIGPIPE.
            for _ in 0..<20 {
                #expect(throws: DaemonError.self) { try conn.send("thing", Empty()) }
            }
            #expect(throws: DaemonError.self) { try DaemonConnection(socket: DaemonPaths.socket) }
        }
    }
}

@Test func theSocketPathMatchesTheDaemons() {
    #expect(DaemonPaths.socket(["ACMD_RUNTIME_DIR": "/x/run"]) == "/x/run/daemon.sock")
    #expect(DaemonPaths.socket(["TMPDIR": "/var/t/"]) == "/var/t/agent-command-\(getuid())/daemon.sock")
    let long = ["TMPDIR": "/" + String(repeating: "d", count: 120), "ACMD_STATE_DIR": "/x/state"]
    #expect(DaemonPaths.socket(long) == "/x/state/run/daemon.sock", "a long TMPDIR falls back to the user's own folder, not /tmp")
}
