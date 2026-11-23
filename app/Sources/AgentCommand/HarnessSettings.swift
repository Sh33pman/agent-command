import AppKit
import SwiftUI

/// The rules for a harness's name, as the daemon checks them: letters, with
/// single spaces between words, unique ignoring case and "_" for a space.
enum HarnessName {
    /// Why name can't be a harness's name, or nil when it can.
    static func problem(_ name: String, others: [Harness]) -> String? {
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        if trimmed.isEmpty { return "Give it a name." }
        if trimmed.range(of: "^[A-Za-z]+( [A-Za-z]+)*$", options: .regularExpression) == nil {
            return "Use letters, with single spaces between words: no digits or other characters."
        }
        if others.contains(where: { slug($0.name) == slug(trimmed) }) {
            return "Another harness is already called that (names ignore case)."
        }
        return nil
    }

    /// The name as compared and as it goes in a folder: lower case, "_" for spaces.
    static func slug(_ name: String) -> String {
        name.trimmingCharacters(in: .whitespaces).lowercased().replacingOccurrences(of: " ", with: "_")
    }

    /// A new Claude harness's folder: ~/.claude-<slug>.
    static func defaultFolder(_ name: String) -> String { "~/.claude-" + slug(name) }
}

/// Settings → Harnesses: what sessions and jobs run in. Each is Claude or
/// Antigravity with its own name and settings; a Claude harness has its own
/// folder (CLAUDE_CONFIG_DIR), so two can be signed in to different accounts.
struct HarnessSettings: View {
    var list = HarnessList.shared
    @State private var selectedID: String?
    @State private var adding: String?
    @State private var removing: Harness?
    @State private var problem: String?

    private var selected: Harness? { list.harnesses.first { $0.harnessID == selectedID } ?? list.harnesses.first }

    var body: some View {
        Section {
            ForEach(list.harnesses) { h in
                HarnessRow(harness: h, isDefault: h.harnessID == list.defaultID, isSelected: h == selected)
                    .contentShape(Rectangle())
                    .onTapGesture { selectedID = h.harnessID }
            }
            HStack(spacing: 4) {
                Menu {
                    Button("Claude…") { adding = "claude" }
                    Button("Antigravity…") { adding = "antigravity" }
                } label: { Image(systemName: "plus") }
                    .menuStyle(.borderlessButton).fixedSize().help("Add a harness")
                Button { removing = selected } label: { Image(systemName: "minus") }
                    .buttonStyle(.borderless).disabled(selected == nil).help("Remove the selected harness")
                Spacer()
            }
            if let problem { Text(problem).foregroundStyle(.red).font(.caption) }
        } header: {
            Text("Harnesses")
        } footer: {
            Text("What sessions and scheduled jobs run in. Each harness is Claude or Antigravity, with its own name and settings. Each Claude harness has its own folder, so each can be signed in to a different account.")
        }
        if let h = selected {
            HarnessForm(harness: h, others: list.harnesses.filter { $0 != h })
                .id(h.id)
        }
        Group {}
            .sheet(item: Binding(get: { adding.map(KindToAdd.init) }, set: { adding = $0?.kind })) { k in
                AddHarnessSheet(kind: k.kind, others: list.harnesses) { added in
                    adding = nil
                    if let added { selectedID = added.harnessID }
                }
            }
            .confirmationDialog(removing.map { "Remove \($0.name)?" } ?? "", isPresented: Binding(get: { removing != nil }, set: { if !$0 { removing = nil } }),
                                presenting: removing) { h in
                Button("Remove", role: .destructive) { remove(h) }
            } message: { h in
                Text(h.isClaude && !h.folder.isEmpty
                     ? "Its sessions keep running. Its folder, \((h.folder as NSString).abbreviatingWithTildeInPath), stays, with its sign-in."
                     : "Its sessions keep running.")
            }
    }

    private func remove(_ h: Harness) {
        problem = nil
        list.remove(h) { why in
            problem = why
            if why == nil, selectedID == h.harnessID { selectedID = nil }
        }
    }
}

private struct KindToAdd: Identifiable {
    let kind: String
    var id: String { kind }
}

private struct HarnessRow: View {
    let harness: Harness
    let isDefault: Bool
    let isSelected: Bool

    var body: some View {
        HStack {
            Image(systemName: isSelected ? "largecircle.fill.circle" : "circle")
                .foregroundStyle(isSelected ? Color.accentColor : .secondary)
            Text(harness.name)
            Text(harness.kindTitle).foregroundStyle(.secondary)
            Spacer()
            if !harness.installed { Text("Not installed").foregroundStyle(.orange).font(.caption) }
            if isDefault { Text("Default").foregroundStyle(.secondary).font(.caption) }
        }
    }
}

/// A Claude harness's folder: one typed, or chosen with the folder panel,
/// which can make a new one.
private struct FolderField: View {
    @Binding var folder: String
    var placeholder: String

    var body: some View {
        HStack {
            TextField(placeholder, text: $folder).labelsHidden()
            Button("Choose…", action: choose)
        }
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true // its New Folder button
        panel.prompt = "Use Folder"
        panel.message = "Choose a folder for this Claude harness (CLAUDE_CONFIG_DIR), or make a new one."
        panel.directoryURL = URL(fileURLWithPath: (folder.isEmpty ? "~" : folder as NSString).expandingTildeInPath)
        if panel.runModal() == .OK, let url = panel.url {
            folder = (url.path as NSString).abbreviatingWithTildeInPath
        }
    }
}

/// The selected harness's settings: its name, program, extra arguments and,
/// for Claude, its folder.
private struct HarnessForm: View {
    let harness: Harness
    let others: [Harness]
    @State private var name = ""
    @State private var command = ""
    @State private var args = ""
    @State private var folder = ""
    @State private var saving = false
    @State private var problem: String?

    private var nameProblem: String? { HarnessName.problem(name, others: others) }

    private var changed: Bool {
        name.trimmingCharacters(in: .whitespaces) != harness.name || command.trimmingCharacters(in: .whitespaces) != harness.command
            || ShellWords.split(args) != harness.args || (harness.isClaude && folder != harness.configDir)
    }

    var body: some View {
        Section(harness.name) {
            LabeledContent("Name") {
                TextField("Name", text: $name).labelsHidden()
            }
            if let nameProblem, name != harness.name { Text(nameProblem).foregroundStyle(.red).font(.caption) }
            LabeledContent(harness.productName) {
                TextField(harness.isClaude ? "claude" : "agy", text: $command).labelsHidden()
            }
            Text("The program it runs. Empty: \(harness.isClaude ? "claude" : "agy") on your shell's PATH.")
                .font(.caption).foregroundStyle(.secondary)
            LabeledContent("Extra arguments") {
                TextField(harness.isClaude ? "--model opus" : "agy's own flags", text: $args).labelsHidden()
            }
            Text("Added to every session it starts or resumes, after the change. Quote an argument with spaces.")
                .font(.caption).foregroundStyle(.secondary)
            if harness.isClaude {
                LabeledContent("Folder") {
                    FolderField(folder: $folder, placeholder: "Claude's standard folder")
                }
                HStack {
                    Text(folder.isEmpty
                         ? "Claude's standard folder (~/.claude): Claude's usual sign-in and settings."
                         : "CLAUDE_CONFIG_DIR: its own sign-in, settings and history. Its first session there asks you to sign in.")
                        .font(.caption).foregroundStyle(.secondary)
                    Spacer()
                    if !folder.isEmpty {
                        Button("Use Claude's Standard Folder") { folder = "" }.controlSize(.small)
                    }
                }
            }
            if let problem { Text(problem).foregroundStyle(.red).font(.caption) }
            HStack {
                Spacer()
                Button("Revert", action: fill).disabled(!changed || saving)
                Button("Save", action: save).keyboardShortcut(.defaultAction)
                    .disabled(!changed || saving || (name != harness.name && nameProblem != nil))
            }
        }
        .onAppear(perform: fill)
        .onChange(of: harness.args) { fill() }
    }

    private func fill() {
        name = harness.name
        command = harness.command
        args = ShellWords.join(harness.args)
        folder = harness.configDir
        problem = nil
    }

    private func save() {
        var s = HarnessList.Save(id: harness.harnessID)
        let trimmed = name.trimmingCharacters(in: .whitespaces)
        if trimmed != harness.name { s.name = trimmed }
        let cmd = command.trimmingCharacters(in: .whitespaces)
        if cmd != harness.command { s.command = cmd }
        if ShellWords.split(args) != harness.args { s.args = ShellWords.split(args) }
        if harness.isClaude && folder != harness.configDir { s.config_dir = folder.trimmingCharacters(in: .whitespaces) }
        saving = true
        problem = nil
        HarnessList.shared.update(s) { result in
            saving = false
            if case .failure(let error) = result { problem = error.localizedDescription }
        }
    }
}

/// Adding a harness: its name, and for Claude its folder, which follows the
/// name until you change it.
struct AddHarnessSheet: View {
    let kind: String
    let others: [Harness]
    let done: (Harness?) -> Void
    @State private var name = ""
    @State private var folder = ""
    @State private var folderChosen = false
    @State private var saving = false
    @State private var problem: String?

    private var title: String { Harness.title(of: kind) }
    private var nameProblem: String? { HarnessName.problem(name, others: others) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Add a \(title) harness").font(.headline)
            TextField("Name, like Work", text: $name)
                .onChange(of: name) { if !folderChosen { folder = HarnessName.defaultFolder(name) } }
            if !name.isEmpty, let nameProblem { Text(nameProblem).foregroundStyle(.red).font(.caption) }
            if kind == "claude" {
                Text("Folder (CLAUDE_CONFIG_DIR)").font(.caption).foregroundStyle(.secondary)
                FolderField(folder: Binding(get: { folder }, set: { folder = $0; folderChosen = true }), placeholder: "~/.claude-name")
                Text("Its own sign-in, settings and history. Its first session asks you to sign in.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let problem { Text(problem).foregroundStyle(.red).font(.caption) }
            HStack {
                Spacer()
                Button("Cancel") { done(nil) }.keyboardShortcut(.cancelAction)
                Button("Add", action: add).keyboardShortcut(.defaultAction).disabled(nameProblem != nil || saving)
            }
        }
        .padding(20)
        .frame(width: 420)
    }

    private func add() {
        var s = HarnessList.Save(kind: kind, name: name.trimmingCharacters(in: .whitespaces))
        if kind == "claude" { s.config_dir = folder.trimmingCharacters(in: .whitespaces) }
        saving = true
        HarnessList.shared.add(s) { result in
            saving = false
            switch result {
            case .success(let h): done(h)
            case .failure(let error): problem = error.localizedDescription
            }
        }
    }
}
