import SwiftUI

/// The folder for a new session on a Remote host, in the popup's frame: the
/// host's recent folders, or its folders to browse, with New Folder. What it
/// lists and does is in RemoteFolderChooser.
struct RemoteFolderView: View {
    let model: RemoteFolderChooser
    /// What it's for, in its title: "Claude on studio: choose its folder".
    var title: String
    let close: () -> Void

    init(model: RemoteFolderChooser, harness: Harness, close: @escaping () -> Void) {
        self.init(model: model, title: "\(harness.title) on \(model.hostName): choose its folder", close: close)
    }

    init(model: RemoteFolderChooser, title: String, close: @escaping () -> Void) {
        (self.model, self.title, self.close) = (model, title, close)
    }

    private var theme: AppTheme { .shared }

    var body: some View {
        let rows = model.rows
        PopupBox(
            title: title,
            placeholder: model.naming ? "The new folder's name, then return" : "Search, or choose a folder",
            search: Binding(get: { model.search }, set: { model.setSearch($0) }),
            close: {
                if !model.back() { close() }
            },
            onMove: { model.move($0) },
            onSubmit: { model.submit() },
            onKey: { model.handle($0) }
        ) {
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text(model.place).fontWeight(.bold).foregroundStyle(theme.magenta).lineLimit(1).truncationMode(.head)
                    Spacer()
                    if model.loading { Text("…").foregroundStyle(theme.dim) }
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 4)
                if let error = model.error {
                    Text(error)
                        .foregroundStyle(theme.red)
                        .textSelection(.enabled)
                        .padding(.horizontal, 24)
                        .padding(.bottom, 4)
                }
                if model.naming {
                    Text("New folder in \(model.place)").foregroundStyle(theme.dim).padding(.horizontal, 24)
                }
                ScrollViewReader { proxy in
                    ScrollView {
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
                                line(row, isSelected: index == model.selected)
                                    .id(row.id)
                                    .contentShape(Rectangle())
                                    .onHover { if $0 { model.select(row) } }
                                    .onTapGesture {
                                        model.select(row)
                                        model.submit()
                                    }
                            }
                        }
                    }
                    .frame(maxHeight: 420)
                    .fixedSize(horizontal: false, vertical: true)
                    .onChange(of: model.selected) {
                        if rows.indices.contains(model.selected) { proxy.scrollTo(rows[model.selected].id) }
                    }
                }
                Text(RemoteFolderChooser.hint)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.dim)
                    .padding(.horizontal, 24)
                    .padding(.top, 12)
            }
        }
        .onAppear { model.start() }
    }

    private func label(_ row: RemoteFolderChooser.Row) -> (String, String) {
        switch row {
        case .here: ("Start here", model.place)
        case .up(let path): ("..", "up to \((path as NSString).lastPathComponent.isEmpty ? path : (path as NSString).lastPathComponent)")
        case .folder(let folder):
            (model.mode == .recent ? folder.path : folder.name + "/",
             model.mode == .recent && !model.picksFile ? "start here" : (folder.hidden ? "hidden" : ""))
        case .file(let file): (file.name, "use this file")
        case .browse: ("Browse folders…", "from the home folder")
        case .newFolder: ("New Folder…", "⌘N")
        }
    }

    private func line(_ row: RemoteFolderChooser.Row, isSelected: Bool) -> some View {
        let (title, note) = label(row)
        return HStack {
            Text(title)
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 16)
            Text(note)
                .foregroundStyle(isSelected ? theme.bg : theme.dim)
                .lineLimit(1)
                .truncationMode(.head)
        }
        .padding(.horizontal, 8)
        .frame(height: 19)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }
}
