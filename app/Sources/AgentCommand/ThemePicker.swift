import SwiftUI

/// Picks a theme. Moving through the list previews each one live; return keeps
/// it and esc goes back to what it was.
struct ThemePickerView: View {
    static let defaultLabel = "Ghostty default"

    let names: [String]
    let current: String?
    let preview: (String?) -> Void
    let commit: (String?) -> Void
    let cancel: () -> Void

    @State private var search = ""
    @State private var selected = 0
    @State private var positioned = false

    private var theme: AppTheme { .shared }

    private var rows: [String] {
        let all = [Self.defaultLabel] + names
        let query = search.trimmingCharacters(in: .whitespaces)
        return query.isEmpty ? all : all.filter { $0.localizedCaseInsensitiveContains(query) }
    }

    /// The config value for a row.
    private func value(_ row: String) -> String? { row == Self.defaultLabel ? nil : row }

    var body: some View {
        let list = rows
        PopupBox(
            title: "Themes",
            placeholder: "Search",
            dimsBackground: false,
            search: $search,
            close: cancel,
            onMove: { delta in
                guard !list.isEmpty else { return }
                selected = (selected + delta + list.count) % list.count
                preview(value(list[selected]))
            },
            onSubmit: {
                if list.indices.contains(selected) { commit(value(list[selected])) }
            }
        ) {
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        if list.isEmpty {
                            Text("No matching themes").foregroundStyle(theme.dim).padding(.horizontal, 24)
                        }
                        ForEach(list, id: \.self) { row in
                            let index = list.firstIndex(of: row) ?? 0
                            ThemeRow(name: row, isCurrent: value(row) == current, isSelected: index == selected)
                                .id(row)
                                .contentShape(Rectangle())
                                .onTapGesture { commit(value(row)) }
                        }
                    }
                }
                .frame(height: 380)
                .onAppear {
                    guard !positioned else { return }
                    positioned = true
                    selected = list.firstIndex { value($0) == current } ?? 0
                    if list.indices.contains(selected) {
                        DispatchQueue.main.async { proxy.scrollTo(list[selected], anchor: .center) }
                    }
                }
                .onChange(of: selected) {
                    if list.indices.contains(selected) { proxy.scrollTo(list[selected]) }
                }
            }
        }
        .onChange(of: search) {
            selected = 0
            if let first = rows.first { preview(value(first)) }
        }
    }
}

private struct ThemeRow: View {
    let name: String
    let isCurrent: Bool
    let isSelected: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        HStack {
            Text(name)
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
                .lineLimit(1)
            Spacer(minLength: 12)
            if isCurrent {
                Text("current").foregroundStyle(isSelected ? theme.bg : theme.dim)
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 19)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }
}
