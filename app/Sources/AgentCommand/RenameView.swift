import SwiftUI

/// The names a rename gives: the name itself for one session, or the name with
/// its position for several. This has no dependencies beyond the standard library.
enum RenameNames {
    static func names(base: String, count: Int) -> [String] {
        let name = base.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return [] }
        return count == 1 ? [name] : (1...max(count, 1)).map { "\(name) - \($0)" }
    }
}

/// A popup to rename one session, or to number several under one name.
struct RenameView: View {
    let sessions: [Session]
    let close: () -> Void
    let rename: (String) -> Void

    @State private var name: String

    private var theme: AppTheme { .shared }

    init(sessions: [Session], close: @escaping () -> Void, rename: @escaping (String) -> Void) {
        self.sessions = sessions
        self.close = close
        self.rename = rename
        _name = State(initialValue: sessions.count == 1 ? sessions[0].displayName : "")
    }

    private var title: String {
        sessions.count == 1 ? "Rename \(sessions[0].displayName)" : "Rename \(sessions.count) sessions"
    }

    var body: some View {
        let names = RenameNames.names(base: name, count: sessions.count)
        PopupBox(
            title: title,
            placeholder: "Name",
            hasList: false,
            search: $name,
            close: close,
            onMove: { _ in },
            onSubmit: { if !names.isEmpty { rename(name) } }
        ) {
            if sessions.count > 1 {
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(sessions.enumerated()), id: \.element.id) { index, session in
                            HStack {
                                Text(session.displayName).foregroundStyle(theme.dim).lineLimit(1)
                                Spacer(minLength: 16)
                                Text(names.indices.contains(index) ? names[index] : "name - \(index + 1)")
                                    .foregroundStyle(names.isEmpty ? theme.dim : theme.fg)
                                    .lineLimit(1)
                            }
                            .padding(.horizontal, 24)
                            .frame(height: 19)
                        }
                    }
                }
                .frame(maxHeight: 300)
                .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}
