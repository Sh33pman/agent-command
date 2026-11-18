import SwiftUI

/// Asks what a new session runs: Claude, Antigravity or a terminal. The folder
/// panel comes after. Its checkbox makes the agent picked what ⌘N starts.
struct HarnessPickerView: View {
    var title = "New session"
    let close: () -> Void
    /// The harness picked, and whether to make it the default.
    let choose: (Harness, Bool) -> Void

    @State private var choice = HarnessChoice()
    @State private var makeDefault: Bool

    init(title: String = "New session", makeDefault: Bool = false,
         close: @escaping () -> Void, choose: @escaping (Harness, Bool) -> Void) {
        self.title = title
        self.close = close
        self.choose = choose
        _makeDefault = State(initialValue: makeDefault)
    }

    /// Whether the checkbox counts for this pick: only an agent can be the default.
    static func makesDefault(_ harness: Harness, checked: Bool) -> Bool {
        checked && DefaultHarness.choices.contains(harness)
    }

    private func pick(_ harness: Harness) {
        choose(harness, Self.makesDefault(harness, checked: makeDefault))
    }

    private var theme: AppTheme { .shared }

    var body: some View {
        let list = choice.matches
        PopupBox(
            title: title,
            placeholder: "Search, or press a number",
            search: Binding(
                get: { choice.search },
                set: { text in
                    if let picked = choice.setSearch(text) { pick(picked) }
                }),
            close: close,
            onMove: { choice.move($0) },
            onSubmit: {
                if let picked = choice.chosen { pick(picked) }
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
                        .onTapGesture { pick(harness) }
                }
                Toggle("Make it the default for ⌘N", isOn: $makeDefault)
                    .toggleStyle(.checkbox)
                    .disabled(choice.chosen.map { !DefaultHarness.choices.contains($0) } ?? true)
                    .help("Claude or Antigravity: ⌘N starts it without asking. Change it in Settings.")
                    .foregroundStyle(theme.dim)
                    .padding(.horizontal, 24)
                    .padding(.top, 8)
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
