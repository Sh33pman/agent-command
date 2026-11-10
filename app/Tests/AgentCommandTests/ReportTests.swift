import Foundation
import Testing

@testable import AgentCommand

@Test func reportNeedsATitleADescriptionAndTheDetails() {
    var draft = ReportDraft(kind: .bug)
    #expect(!draft.canSend(opening: false))
    draft.title = "Crash"
    draft.description = "  \n"
    draft.details = "acmd 0.45.1"
    #expect(!draft.canSend(opening: false), "a blank description doesn't count")
    draft.description = "It quit."
    #expect(draft.canSend(opening: false))
    #expect(!draft.canSend(opening: true), "not twice while it opens")
    draft.details = nil
    #expect(!draft.canSend(opening: false), "not before the details are read")
}

@Test func reportShowsWhatItWillSend() {
    var bug = ReportDraft(kind: .bug)
    #expect(bug.shownDetails == "Reading…")
    bug.details = "acmd 0.45.1, macOS 26.1\nlog line 1\nlog line 2"
    #expect(bug.shownDetails == bug.details, "a bug includes the log")
    bug.includesLog = false
    #expect(bug.shownDetails == "acmd 0.45.1, macOS 26.1")

    var feature = ReportDraft(kind: .feature, details: "acmd 0.45.1, macOS 26.1\nlog")
    feature.includesLog = true
    #expect(feature.shownDetails == "acmd 0.45.1, macOS 26.1", "a feature request never has the log")

    bug.description = "  It quit.\n"
    #expect(bug.body == "It quit.\n\n---\n\nacmd 0.45.1, macOS 26.1")
    bug.title = "Crash on quit"
    #expect(bug.pageArguments == ["report", "page", "--kind", "bug", "--title", "Crash on quit"])
    #expect(feature.detailsArguments == ["report", "details", "--kind", "feature"])
}

@Test func reportKindsSayWhatTheyAsk() {
    #expect(ReportKind.bug.title == "Report a bug")
    #expect(ReportKind.feature.title == "Request a feature")
    #expect(ReportKind.bug.question.contains("expect"))
    #expect(ReportKind.feature.question.contains("why"))
}

@Test func reportPageIsReadFromAcmdsAnswer() {
    let json = Data(#"{"page_url":"https://github.com/o/r/issues/new?title=x","body":"full","cut":true}"#.utf8)
    let page = ReportPage.from(status: 0, output: json)
    #expect(page?.url?.host == "github.com")
    #expect(page?.wasCut == true)
    #expect(page?.body == "full")
    let uncut = ReportPage.from(status: 0, output: Data(#"{"page_url":"https://github.com","body":""}"#.utf8))
    #expect(uncut?.wasCut == false, "cut is left out when it wasn't")
    #expect(ReportPage.from(status: 1, output: json) == nil, "a failed run")
    #expect(ReportPage.from(status: 0, output: Data("nope".utf8)) == nil)
    #expect(ReportPage.from(status: 0, output: Data(#"{"page_url":"","body":""}"#.utf8)) == nil, "no page to open")
}

/// The runner that ReportView uses for acmd, tried on programs that aren't acmd.
@Test func reportRunnerPassesInputAndReturnsOutput() {
    let echoed = ReportProcess.run("/bin/cat", [], input: Data("hello\n".utf8))
    #expect(echoed.status == 0)
    #expect(String(decoding: echoed.output, as: UTF8.self) == "hello\n")
    let failed = ReportProcess.run("/bin/sh", ["-c", "echo out; echo err >&2; exit 3"], input: nil)
    #expect(failed.status == 3)
    #expect(String(decoding: failed.output, as: UTF8.self) == "out\n", "errors aren't mixed into the output")
    #expect(ReportProcess.run("/nonexistent/acmd", [], input: nil).status == -1)
}
