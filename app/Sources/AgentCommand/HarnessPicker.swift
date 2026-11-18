import SwiftUI

/// Asks where a new session runs (this Mac or a Remote host) and what it runs:
/// Claude, Antigravity or a terminal. The folder comes after: this Mac's folder
/// panel, or the host's folders. Its checkbox makes the agent picked what ⌘N starts.
struct HarnessPickerView: View {
    var title = "New session"
    let close: () -> Void
    /// The harness picked, whether to make it the default, and the host, if not this Mac.
    let choose: (Harness, Bool, String?) -> Void

    @State private var picker: NewSessionPicker
    @State private var search = ""
    @State private var makeDefault: Bool

    init(title: String = "New session", makeDefault: Bool = false, picker: NewSessionPicker = NewSessionPicker(hosts: []),
         close: @escaping () -> Void, choose: @escaping (Harness, Bool, String?) -> Void) {
        self.title = title
        self.close = close
        self.choose = choose
        _picker = State(initialValue: picker)
        _makeDefault = State(initialValue: makeDefault)
    }

    /// Whether the checkbox counts for this pick: only an agent on this Mac can be the default.
    static func makesDefault(_ harness: Harness, checked: Bool, host: String? = nil) -> Bool {
        checked && host == nil && DefaultHarness.choices.contains(harness)
    }

    private func pick(_ harness: Harness) {
        choose(harness, Self.makesDefault(harness, checked: makeDefault, host: picker.host), picker.host)
    }

    private var theme: AppTheme { .shared }

    var body: some View {
        let choice = picker.choice
        let list = choice.matches
        PopupBox(
            title: title,
            placeholder: picker.hosts.isEmpty ? "Search, or press a number" : "Search, or press a number; tab changes where",
            search: Binding(
                get: { search },
                set: { text in
                    search = text
                    if let picked = picker.setSearch(text) { pick(picked) }
                }),
            close: close,
            onMove: { picker.move($0) },
            onSubmit: {
                if let picked = picker.choice.chosen { pick(picked) }
            },
            onKey: { combo in
                guard picker.handle(combo) else { return false }
                search = ""
                return true
            }
        ) {
            VStack(alignment: .leading, spacing: 0) {
                if !picker.hosts.isEmpty { whereLine }
                if picker.loading {
                    Text("Asking \(picker.host ?? "the host") what it can run…")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                } else if let error = picker.error {
                    Text(error)
                        .foregroundStyle(theme.red)
                        .padding(.horizontal, 24)
                        .textSelection(.enabled)
                } else if list.isEmpty {
                    Text("No matching harness")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                }
                ForEach(list) { harness in
                    row(harness, choice: choice, isSelected: harness == choice.chosen)
                        .contentShape(Rectangle())
                        .onHover { if $0 { picker.select(harness) } }
                        .onTapGesture { pick(harness) }
                }
                if picker.canMakeDefault {
                    Toggle("Make it the default for ⌘N", isOn: $makeDefault)
                        .toggleStyle(.checkbox)
                        .disabled(choice.chosen.map { !DefaultHarness.choices.contains($0) } ?? true)
                        .help("Claude or Antigravity: ⌘N starts it without asking. Change it in Settings.")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                        .padding(.top, 8)
                } else {
                    Text("It runs on \(picker.host ?? ""), as a Remote session there, and shows here.")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                        .padding(.top, 8)
                }
            }
        }
    }

    /// Where it runs: this Mac, or a host. Tab, or left and right with nothing typed, move along it.
    private var whereLine: some View {
        HStack(spacing: 4) {
            Text("Where").foregroundStyle(theme.dim)
            ForEach(picker.places, id: \.self) { place in
                let isSelected = place == picker.host
                Text(NewSessionPicker.title(place))
                    .fontWeight(isSelected ? .bold : .regular)
                    .foregroundStyle(isSelected ? theme.bg : theme.fg)
                    .padding(.horizontal, 6)
                    .background(isSelected ? theme.yellow : Color.clear)
                    .contentShape(Rectangle())
                    .onTapGesture { picker.setHost(place) }
            }
            Spacer(minLength: 0)
        }
        .lineLimit(1)
        .padding(.horizontal, 24)
        .padding(.bottom, 12)
    }

    private func row(_ harness: Harness, choice: HarnessChoice, isSelected: Bool) -> some View {
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
