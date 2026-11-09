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
