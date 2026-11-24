import Foundation

nonisolated enum ReportKind: String {
    case bug, feature

    var title: String { self == .bug ? "Report a bug" : "Request a feature" }
    var question: String { self == .bug ? "What happened, and what did you expect?" : "What would you like, and why?" }
}

/// What the form holds, and the rules for what's sent.
nonisolated struct ReportDraft {
    let kind: ReportKind
    var title = ""
    var description = ""
    /// What the daemon said about this install; nil until it's read.
    var details: String?
    var includesLog = true

    func canSend(opening: Bool) -> Bool {
        !opening && details != nil && !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !description.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// The details as they'd be sent: without the log, only the first line.
    var shownDetails: String {
        guard let details else { return "Reading…" }
        if kind == .bug && includesLog { return details }
        return details.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? details
    }

    /// What the daemon's report page is made from.
    var body: String {
        description.trimmingCharacters(in: .whitespacesAndNewlines) + "\n\n---\n\n" + shownDetails
    }
}

/// What the daemon's report.page answers: the page to open, and the full report in
/// case the page's link had to be cut.
nonisolated struct ReportPage: Decodable, Equatable {
    let page_url: String
    let body: String
    let cut: Bool?

    var url: URL? { URL(string: page_url) }
    var wasCut: Bool { cut == true }

}
