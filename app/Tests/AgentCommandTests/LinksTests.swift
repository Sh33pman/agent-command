import Foundation
import Testing

@testable import AgentCommand

@Test func webAndMailLinksOpen() {
    #expect(TerminalLinks.url("https://github.com/brettmostert/agent-command/pull/195")?.host == "github.com")
    #expect(TerminalLinks.url("http://localhost:8080/x")?.port == 8080)
    #expect(TerminalLinks.url("  https://example.com\n")?.absoluteString == "https://example.com")
    #expect(TerminalLinks.url("mailto:someone@example.com")?.scheme == "mailto")
    #expect(TerminalLinks.url("HTTPS://EXAMPLE.COM") != nil)
    #expect(TerminalLinks.url("example.com/docs")?.absoluteString == "https://example.com/docs", "a bare link is https")
}

@Test func otherLinksDont() {
    for text in ["file:///Applications/Calculator.app", "x-apple.systempreferences:", "javascript:alert(1)",
                 "ssh://host", "https://", "", "   ", "./build/out", "/usr/bin/env", "not a link", "~/notes.md"] {
        #expect(TerminalLinks.url(text) == nil, "\(text) shouldn't open")
    }
}

@Test func ghosttysStringsAreRead() {
    let text = "https://example.com/é"
    let read = text.withCString { TerminalLinks.string($0, text.utf8.count) }
    #expect(read == text)
    #expect(TerminalLinks.string(nil, 3).isEmpty)
    #expect("abc".withCString { TerminalLinks.string($0, 0) }.isEmpty)
}

@Test func pathsInATerminalAreFiles() {
    #expect(FileLink.from("src/app/main.swift:42") == FileLink(path: "src/app/main.swift", line: 42))
    #expect(FileLink.from("./build/out") == FileLink(path: "./build/out"))
    #expect(FileLink.from("/usr/bin/env") == FileLink(path: "/usr/bin/env"))
    #expect(FileLink.from("~/notes.md:3:7") == FileLink(path: "~/notes.md", line: 3, column: 7))
    #expect(FileLink.from("../x/y.go:0") == FileLink(path: "../x/y.go:0"), "line 0 isn't a line")
    #expect(FileLink.from("file:///Users/me/a%20b.txt") == FileLink(path: "/Users/me/a b.txt"))
    #expect(FileLink.from("file://localhost/etc/hosts") == FileLink(path: "/etc/hosts"))
    #expect(FileLink.from("  /tmp/x:1:2:3 ") == FileLink(path: "/tmp/x:1", line: 2, column: 3))
    for text in ["https://example.com/a.go", "example.com/docs", "file://otherhost/etc/hosts", "ssh://host/x",
                 "x-apple.systempreferences:", "main.go", "", "not a path", "mailto:a@b.c"] {
        #expect(FileLink.from(text) == nil, "\(text) isn't a file")
    }
}

@Test func aFileIsFoundFromTheSessionsFolder() {
    let link = FileLink(path: "src/../src/main.go", line: 4, column: 2)
    #expect(link.resolved(in: "/w/proj") == "/w/proj/src/main.go")
    #expect(FileLink(path: "/abs/x").resolved(in: "/w") == "/abs/x")
    #expect(FileLink(path: "~/n.md").resolved(in: "/w", home: "/Users/me") == "/Users/me/n.md")
    #expect(FileLink(path: "~").resolved(in: "/w", home: "/Users/me") == "/Users/me")
    // What the daemon's editor.open gets.
    #expect(link.editorOpen(in: "/w/proj") == EditorOpen(path: "/w/proj/src/main.go", in: "/w/proj", line: 4, column: 2))
    #expect(FileLink(path: "a.go", line: 9).editorOpen(in: "/w") == EditorOpen(path: "/w/a.go", in: "/w", line: 9))
    #expect(FileLink(path: "a.go").editorOpen(in: "/w") == EditorOpen(path: "/w/a.go", in: "/w"))
    #expect(FileLink(path: "a.go", column: 3).editorOpen(in: "/w").column == nil, "a column only with a line")
}

/// Opening in the editor asks acmd's background service, which says why it
/// couldn't.
@MainActor @Test func openingInTheEditorAsksTheDaemon() throws {
    let store = SessionStore()
    let fake = FakeCalls()
    store.mutations = fake
    var failure: String? = "unset"
    store.openInEditor(EditorOpen(id: "s1")) { failure = $0 }
    #expect(fake.ops == ["editor.open"] && fake.last["id"] as? String == "s1" && failure == nil)
    fake.failures["editor.open"] = "no editor chosen yet"
    store.openInEditor(EditorOpen(path: "/w/a.go", in: "/w", line: 3)) { failure = $0 }
    #expect(fake.last["path"] as? String == "/w/a.go" && fake.last["line"] as? Int == 3 && failure == "no editor chosen yet")
}
