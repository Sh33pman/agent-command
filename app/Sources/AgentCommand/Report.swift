import Foundation

enum ReportKind: String {
    case bug, feature

    var title: String { self == .bug ? "Report a bug" : "Request a feature" }
    var question: String { self == .bug ? "What happened, and what did you expect?" : "What would you like, and why?" }
}

/// What the form holds, and the rules for what's sent.
struct ReportDraft {
    let kind: ReportKind
    var title = ""
    var description = ""
    /// What `acmd report details` said about this install; nil until it's read.
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

    /// What `acmd report page` is given on its input.
    var body: String {
        description.trimmingCharacters(in: .whitespacesAndNewlines) + "\n\n---\n\n" + shownDetails
    }

    var pageArguments: [String] { ["report", "page", "--kind", kind.rawValue, "--title", title] }
    var detailsArguments: [String] { ["report", "details", "--kind", kind.rawValue] }
}

/// What `acmd report page` answers: the page to open, and the full report in case
/// the page's link had to be cut.
struct ReportPage: Decodable, Equatable {
    let page_url: String
    let body: String
    let cut: Bool?

    var url: URL? { URL(string: page_url) }
    var wasCut: Bool { cut == true }

    /// The page from acmd's output, or nil if it failed.
    static func from(status: Int32, output: Data) -> ReportPage? {
        guard status == 0, let page = try? JSONDecoder().decode(ReportPage.self, from: output), page.url != nil else {
            return nil
        }
        return page
    }
}

enum ReportProcess {
    /// Runs a program with this input, and returns its exit status and output. Its
    /// errors are dropped.
    static func run(_ path: String, _ arguments: [String], input: Data?) -> (status: Int32, output: Data) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = arguments
        let output = Pipe()
        let stdin = Pipe()
        process.standardInput = input == nil ? FileHandle.nullDevice : stdin
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
        } catch {
            return (-1, Data())
        }
        if let input {
            _ = fcntl(stdin.fileHandleForWriting.fileDescriptor, F_SETNOSIGPIPE, 1)
            try? stdin.fileHandleForWriting.write(contentsOf: input)
            try? stdin.fileHandleForWriting.close()
        }
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        return (process.terminationStatus, data)
    }
}
