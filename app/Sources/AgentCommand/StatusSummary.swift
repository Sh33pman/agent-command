import SwiftUI

/// How a sidebar row marks its session.
enum RowMark: Equatable {
    /// Claude: in the status's color, greyed out when stopped, turning while busy.
    case claude(dimmed: Bool, spinning: Bool)
    /// A plain terminal.
    case terminal
    /// Another agent, or one acmd can't name.
    case dot

    init(_ session: Session) {
        if session.runsClaude {
            self = .claude(dimmed: session.isStopped || session.isArchived || session.status == "unknown",
                           spinning: session.status == "busy")
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

/// Claude's mark: a starburst of rounded rays, drawn so it takes any color.
struct ClaudeMarkShape: Shape {
    func path(in rect: CGRect) -> Path {
        let center = CGPoint(x: rect.midX, y: rect.midY)
        let radius = min(rect.width, rect.height) / 2
        // Rays of slightly different lengths, so it reads as a hand-drawn burst.
        let lengths: [CGFloat] = [1, 0.82, 0.95, 0.78, 1, 0.86, 0.92, 0.8, 0.98, 0.84, 0.9]
        var path = Path()
        for (i, length) in lengths.enumerated() {
            let angle = CGFloat(i) / CGFloat(lengths.count) * 2 * .pi - .pi / 2
            let tip = CGPoint(x: center.x + cos(angle) * radius * length, y: center.y + sin(angle) * radius * length)
            path.move(to: CGPoint(x: center.x + cos(angle) * radius * 0.05, y: center.y + sin(angle) * radius * 0.05))
            path.addLine(to: tip)
        }
        return path.strokedPath(StrokeStyle(lineWidth: max(1.2, radius * 0.3), lineCap: .round))
    }
}

/// The mark on a Claude row. It turns slowly while Claude is busy.
struct ClaudeMark: View {
    let color: Color
    let spinning: Bool
    @State private var angle = 0.0

    var body: some View {
        ClaudeMarkShape()
            .fill(color)
            .frame(width: 10, height: 10)
            .rotationEffect(.degrees(angle))
            .onAppear(perform: spin)
            .onChange(of: spinning) { spin() }
            .accessibilityLabel("Claude")
    }

    private func spin() {
        if spinning {
            withAnimation(.linear(duration: 3).repeatForever(autoreverses: false)) { angle = 360 }
        } else {
            withAnimation(.default) { angle = 0 }
        }
    }
}
