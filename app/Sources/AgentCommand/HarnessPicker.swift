import SwiftUI

/// Asks where a new session runs (this Mac or a Remote host) and what it runs:
/// Claude, Antigravity or a terminal. The folder comes after: this Mac's folder
/// panel, or the host's folders. While there's no default, its checkbox (⌘D)
/// makes the harness picked what ⌘N starts; once there is one, only Settings
/// changes it.
struct HarnessPickerView: View {
    var title = "New session"
    /// Whether to offer the checkbox: there's no default yet.
    let offersDefault: Bool
    let close: () -> Void
    /// The harness picked, whether to make it the default, and the host, if not this Mac.
    let choose: (Harness, Bool, String?) -> Void

    @State private var picker: NewSessionPicker
    @State private var search = ""
    /// Starts unticked: picking a harness never makes it the default unless you say so.
    @State private var makeDefault = false

    init(title: String = "New session", offersDefault: Bool = false, picker: NewSessionPicker = NewSessionPicker(hosts: []),
         close: @escaping () -> Void, choose: @escaping (Harness, Bool, String?) -> Void) {
        self.title = title
        self.offersDefault = offersDefault
        self.close = close
        self.choose = choose
        _picker = State(initialValue: picker)
    }

    /// What ticks and unticks the checkbox.
    static let toggleDefaultKey = KeyCombo("cmd+d")

    /// Whether the checkbox shows: there's no default yet, and it runs on this Mac.
    static func showsDefault(offered: Bool, host: String?) -> Bool { offered && host == nil }

    /// Whether the checkbox counts for this pick: only a harness on this Mac can be the default.
    static func makesDefault(_ harness: Harness, checked: Bool, offered: Bool = true, host: String? = nil) -> Bool {
        checked && showsDefault(offered: offered, host: host) && DefaultHarness.choices.contains(harness)
    }

    private var showsDefault: Bool { Self.showsDefault(offered: offersDefault, host: picker.host) }

    private func pick(_ harness: Harness) {
        choose(harness, Self.makesDefault(harness, checked: makeDefault, offered: offersDefault, host: picker.host), picker.host)
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
                if combo == Self.toggleDefaultKey, showsDefault {
                    makeDefault.toggle()
                    return true
                }
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
                if showsDefault {
                    Toggle("Make it the default for ⌘N  (⌘D)", isOn: $makeDefault)
                        .toggleStyle(.checkbox)
                        .help("⌘N then starts it without asking. Change it in Settings → Harnesses.")
                        .foregroundStyle(theme.dim)
                        .padding(.horizontal, 24)
                        .padding(.top, 8)
                } else if picker.host != nil {
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
