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

@Test func reportKindsSayWhatTheyAsk() {
    #expect(ReportKind.bug.title == "Report a bug")
    #expect(ReportKind.feature.title == "Request a feature")
    #expect(ReportKind.bug.question.contains("expect"))
    #expect(ReportKind.feature.question.contains("why"))
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
}

@Test func theReportPageSaysIfItWasCut() throws {
    let page = try JSONDecoder().decode(ReportPage.self, from: Data(#"{"page_url":"https://github.com/x","body":"b","cut":true}"#.utf8))
    #expect(page.url?.absoluteString == "https://github.com/x" && page.wasCut)
    let whole = try JSONDecoder().decode(ReportPage.self, from: Data(#"{"page_url":"https://github.com","body":""}"#.utf8))
    #expect(!whole.wasCut)
}
