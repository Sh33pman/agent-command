import SwiftUI

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

/// A session's mark (Claude, $ or a dot) in its status's color, as on its row.
struct SessionMark: View {
    let session: Session
    /// Drawn on the selection's bar: in the background color.
    var onBar = false
    private var theme: AppTheme { .shared }

    var body: some View {
        switch RowMark(session) {
        case .claude(let dimmed, let spinning):
            ClaudeMark(color: onBar ? theme.bg : (dimmed ? theme.dim : color), spinning: spinning)
        case .terminal:
            Text("$").foregroundStyle(onBar ? theme.bg : color)
        case .dot:
            Text("●").foregroundStyle(onBar ? theme.bg : color)
        }
    }

    private var color: Color {
        switch session.status {
        case "busy", "starting": theme.yellow
        case "waiting": theme.blue
        case "done", "running": theme.green
        case "error": theme.red
        default: theme.dim
        }
    }
}

/// The line over a pane: what runs there and what it's doing.
struct PaneHeader: View {
    static let height: CGFloat = 22

    let store: SessionStore
    let sessionID: String
    let onTap: () -> Void
    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            if let session = store.sessions.first(where: { $0.id == sessionID }) {
                HStack(spacing: 6) {
                    SessionMark(session: session)
                    Text(session.displayName).fontWeight(.bold).foregroundStyle(theme.fg).lineLimit(1)
                    Text(session.statusLine).foregroundStyle(theme.dim).lineLimit(1).layoutPriority(1)
                    if let description = session.description, !description.isEmpty {
                        Text(description).foregroundStyle(theme.dim).lineLimit(1).truncationMode(.tail)
                    }
                    Spacer(minLength: 8)
                    Text((session.cwd as NSString).abbreviatingWithTildeInPath)
                        .foregroundStyle(theme.dim)
                        .lineLimit(1)
                        .truncationMode(.head)
                }
                .font(AppFont.ui(11))
                .padding(.horizontal, 8)
                .frame(maxHeight: .infinity)
                .help(session.details)
            } else {
                Spacer()
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
        }
        .frame(height: Self.height)
        .background(theme.bg)
        .contentShape(Rectangle())
        .onTapGesture(perform: onTap)
    }
}
