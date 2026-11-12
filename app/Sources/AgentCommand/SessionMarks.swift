import SwiftUI

/// Claude Code's crab, drawn so it takes any color. It's the pixel grid of the
/// logo Claude Code prints in the terminal: a body with two eyes, arms out to
/// the sides and four legs.
struct ClaudeMarkShape: Shape {
    /// One string per row, "#" for a filled pixel. Pixels are twice as tall as
    /// they are wide, like the terminal's quarter-block characters.
    static let pixels = [
        "  ############  ",
        "  ## ###### ##  ",
        "################",
        "  ############  ",
        "   # #    # #   ",
    ]
    static let columns = CGFloat(pixels[0].count)
    static let rows = CGFloat(pixels.count)
    /// Width over height.
    static let aspect = columns / (rows * 2)

    func path(in rect: CGRect) -> Path {
        // Fit the grid in the rect, centered, keeping its shape.
        let width = min(rect.width, rect.height * Self.aspect)
        let pixel = CGSize(width: width / Self.columns, height: width / Self.aspect / Self.rows)
        let origin = CGPoint(x: rect.midX - width / 2, y: rect.midY - pixel.height * Self.rows / 2)
        var path = Path()
        for (y, row) in Self.pixels.enumerated() {
            for (x, cell) in row.enumerated() where cell == "#" {
                path.addRect(CGRect(x: origin.x + CGFloat(x) * pixel.width,
                                    y: origin.y + CGFloat(y) * pixel.height,
                                    width: pixel.width, height: pixel.height))
            }
        }
        return path
    }
}

/// The mark on a Claude row. It hops while Claude is busy.
struct ClaudeMark: View {
    let color: Color
    let busy: Bool
    @State private var hop = 0.0

    var body: some View {
        ClaudeMarkShape()
            .fill(color)
            .frame(width: 13, height: 10)
            .offset(y: hop)
            .onAppear(perform: animate)
            .onChange(of: busy) { animate() }
            .accessibilityLabel("Claude")
    }

    private func animate() {
        if busy {
            withAnimation(.easeInOut(duration: 0.4).repeatForever(autoreverses: true)) { hop = -1.5 }
        } else {
            withAnimation(.default) { hop = 0 }
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
        case .claude(let dimmed, let busy):
            ClaudeMark(color: onBar ? theme.bg : (dimmed ? theme.dim : color), busy: busy)
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
