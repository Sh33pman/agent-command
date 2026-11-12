import SwiftUI

/// How a sidebar row marks its session.
enum RowMark: Equatable {
    /// Claude: in the status's color, greyed out when stopped, hopping while busy.
    case claude(dimmed: Bool, busy: Bool)
    /// A plain terminal.
    case terminal
    /// Another agent, or one acmd can't name.
    case dot

    init(_ session: Session) {
        if session.runsClaude {
            self = .claude(dimmed: session.isStopped || session.isArchived || session.status == "unknown",
                           busy: session.status == "busy")
        } else if session.isTerminal && session.status != "unknown" {
            self = .terminal
        } else {
            self = .dot
        }
    }
}

/// The status bar's summary of every session, such as "1 needs you · 2 busy".
enum StatusSummary {
    struct Part: Equatable {
        let text: String
        /// waiting and error need you; the rest are for information.
        let needsYou: Bool
    }

    static func parts(_ sessions: [Session]) -> [Part] {
        let live = sessions.filter { !$0.isArchived }
        let needsYou = live.filter { $0.status == "waiting" || $0.status == "error" }.count
        let busy = live.filter { $0.status == "busy" }.count
        var parts: [Part] = []
        if needsYou > 0 { parts.append(Part(text: "\(needsYou) needs you", needsYou: true)) }
        if busy > 0 { parts.append(Part(text: "\(busy) busy", needsYou: false)) }
        return parts
    }
}
