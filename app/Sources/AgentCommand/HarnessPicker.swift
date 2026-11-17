import SwiftUI

/// Asks what a new session runs: Claude, Antigravity or a terminal. The folder
/// panel comes after.
struct HarnessPickerView: View {
    let close: () -> Void
    let choose: (Harness) -> Void

    @State private var choice = HarnessChoice()

    private var theme: AppTheme { .shared }

    var body: some View {
        let list = choice.matches
        PopupBox(
            title: "New session",
            placeholder: "Search, or press a number",
            search: Binding(
                get: { choice.search },
                set: { text in
                    if let picked = choice.setSearch(text) { choose(picked) }
                }),
            close: close,
            onMove: { choice.move($0) },
            onSubmit: {
                if let picked = choice.chosen { choose(picked) }
            }
        ) {
            VStack(alignment: .leading, spacing: 0) {
                if list.isEmpty {
                    Text("No matching harness")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                }
                ForEach(list) { harness in
                    row(harness, isSelected: harness == choice.chosen)
                        .contentShape(Rectangle())
                        .onHover { if $0 { choice.select(harness) } }
                        .onTapGesture { choose(harness) }
                }
            }
        }
    }

    private func row(_ harness: Harness, isSelected: Bool) -> some View {
        HStack {
            Text(harness.title)
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
            Spacer(minLength: 16)
            Text("\(choice.number(of: harness))")
                .foregroundStyle(isSelected ? theme.bg : theme.dim)
        }
        .padding(.horizontal, 8)
        .frame(height: 19)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }
}
