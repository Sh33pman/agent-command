import AppKit
import SwiftUI

/// The full-window Scheduled view: jobs on the left; the selected one and its
/// runs on the right, or the form that adds or edits one.
struct ScheduledView: View {
    @Bindable var model: ScheduledModel
    @AppStorage("scheduled.listWidth") private var listWidth = 300.0

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { window in
                HStack(spacing: 0) {
                    list.frame(width: listWidth)
                    InboxDivider(vertical: true, value: $listWidth, range: 220...max(220, window.size.width - 480))
                    Group {
                        if model.draft != nil {
                            JobForm(model: model)
                        } else if let job = model.selected {
                            JobDetail(model: model, job: job)
                        } else {
                            empty
                        }
                    }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            footer
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
        .onChange(of: model.jobs) { model.jobsChanged() }
    }

    private var footer: some View {
        Text(model.isEditing
            ? "⌘↩ save   esc cancel   ⌘B ⌘I ⌘E ⌘K format   ⌥⌘1-3 headings   ⇧⌘7 ⇧⌘8 ⇧⌘9 lists"
            : "↑↓ or j k move   n new   ↩ or e edit   r run now   p pause   d duplicate   g go to last run   ⌫ delete   esc back")
            .font(AppFont.ui(11))
            .foregroundStyle(theme.dim)
            .lineLimit(1)
            .padding(.horizontal, 16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(height: 22)
    }

    private var list: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Scheduled").fontWeight(.bold).foregroundStyle(theme.yellow)
                Spacer()
                Button {
                    model.newJob()
                } label: {
                    Label("New Job", systemImage: "plus")
                }
                .buttonStyle(.borderless)
                .disabled(model.isEditing)
                .help("Add a scheduled job (n)")
            }
            .padding(.horizontal, 12)
            .frame(height: 34)
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            ScrollViewReader { scroll in
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(model.jobs) { job in
                            JobRow(job: job, isSelected: job.id == model.selectedID, place: job.placeName(sessionName: model.sessionName))
                                .id(job.id)
                                .contentShape(Rectangle())
                                .onTapGesture { model.select(job.id) }
                                .contextMenu {
                                    Button("Edit") { model.select(job.id); model.edit() }
                                    Button("Run Now") { model.select(job.id); model.runNow() }
                                    Button(job.paused ? "Resume" : "Pause") { model.select(job.id); model.togglePause() }
                                    Button("Duplicate") { model.select(job.id); model.duplicate() }
                                    Divider()
                                    Button("Delete…") { model.select(job.id); model.delete() }
                                }
                        }
                    }
                }
                .onChange(of: model.selectedID) { _, id in if let id { scroll.scrollTo(id) } }
            }
            .opacity(model.isEditing ? 0.5 : 1)
            .disabled(model.isEditing)
        }
    }

    private var empty: some View {
        VStack(spacing: 10) {
            Image(systemName: "calendar.badge.clock").font(.system(size: 34)).foregroundStyle(theme.dim)
            Text("No scheduled jobs").fontWeight(.bold)
            Text("A job runs a prompt on a schedule, in a folder, a new worktree or an existing session. Each run is a session like any other.")
                .foregroundStyle(theme.dim)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 380)
            Button("New Job") { model.newJob() }.padding(.top, 4)
        }
    }
}

/// A job in the list.
private struct JobRow: View {
    let job: Job
    let isSelected: Bool
    let place: String
    private var theme: AppTheme { .shared }

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: icon).foregroundStyle(onBar ? theme.bg : color).frame(width: 14).padding(.top, 2)
            VStack(alignment: .leading, spacing: 2) {
                Text(job.name).fontWeight(.bold).lineLimit(1)
                Text(job.words.isEmpty ? job.cron : job.words).lineLimit(1)
                    .foregroundStyle(onBar ? theme.bg : theme.dim)
                Text(status).font(AppFont.ui(11)).lineLimit(1)
                    .foregroundStyle(onBar ? theme.bg : (job.state == .failing ? theme.red : theme.dim))
            }
            Spacer(minLength: 0)
        }
        .foregroundStyle(onBar ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(onBar ? theme.yellow : Color.clear)
    }

    private var onBar: Bool { isSelected }

    private var icon: String {
        switch job.state {
        case .paused: "pause.circle"
        case .failing: "exclamationmark.triangle.fill"
        case .finished, .never: "checkmark.circle"
        case .scheduled: "clock"
        }
    }

    private var color: Color {
        switch job.state {
        case .paused, .finished, .never: theme.dim
        case .failing: theme.red
        case .scheduled: theme.green
        }
    }

    private var status: String {
        switch job.state {
        case .paused: return "Paused"
        case .failing: return "Last run failed: \(job.lastRun?.detail ?? "")"
        case .finished: return "Done"
        case .never: return "Won't run"
        case .scheduled:
            return "Next \(JobTime.describe(job.nextRun)) · \(place)"
        }
    }
}

/// The selected job: what it does, and its runs.
private struct JobDetail: View {
    @Bindable var model: ScheduledModel
    let job: Job
    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    banner
                    facts
                    section("Prompt") {
                        if job.promptFile.isEmpty {
                            MarkdownView(job.prompt, dim: theme.dim, panel: Color(nsColor: theme.line), code: theme.yellow)
                                .textSelection(.enabled)
                        } else {
                            Text("Read from \(JobTime.tilde(job.promptFile)) at each run.").foregroundStyle(theme.dim)
                        }
                    }
                    if !job.settings.isEmpty {
                        section("Claude Code settings") {
                            ForEach(job.settings.keys.sorted(), id: \.self) { key in
                                HStack(alignment: .top) {
                                    Text(label(key)).foregroundStyle(theme.dim).frame(width: 150, alignment: .leading)
                                    Text(job.settings[key, default: []].joined(separator: "\n")).textSelection(.enabled)
                                }
                            }
                        }
                    }
                    section("Runs") { runs }
                }
                .padding(16)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(job.name).fontWeight(.bold).font(AppFont.ui(15))
                if !job.description.isEmpty { Text(job.description).foregroundStyle(theme.dim).lineLimit(1) }
            }
            Spacer()
            Button("Run Now") { model.runNow() }.help("Run it now, outside its schedule (r)")
            Button(job.paused ? "Resume" : "Pause") { model.togglePause() }.help(job.paused ? "Resume (p)" : "Pause (p)")
            Button("Duplicate") { model.duplicate() }.help("A new job like this one (d)")
            Button("Edit") { model.edit() }.help("Edit (↩)").keyboardShortcut(.defaultAction)
            Button(role: .destructive) { model.delete() } label: { Image(systemName: "trash") }.help("Delete (⌫)")
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
    }

    @ViewBuilder private var banner: some View {
        if model.confirmingDelete {
            HStack {
                Label("Delete \(job.name)? Its runs' sessions stay.", systemImage: "trash").foregroundStyle(theme.red)
                Spacer()
                Button("Cancel") { model.confirmingDelete = false }
                Button("Delete", role: .destructive) { model.delete(confirmed: true) }
            }
            .padding(10)
            .overlay { Rectangle().strokeBorder(theme.red, lineWidth: 1) }
        }
        if let error = model.error {
            Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(theme.red)
                .fixedSize(horizontal: false, vertical: true)
        } else if let notice = model.notice {
            Label(notice, systemImage: "checkmark.circle").foregroundStyle(theme.green)
        }
    }

    private var facts: some View {
        Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 6) {
            fact("When", job.scheduleLine)
            fact("Next run", job.paused ? "Paused" : (job.next.map { JobTime.describe($0) } ?? "None"))
            fact("Where", job.placeLine(sessionName: model.sessionName))
            if JobPlace(rawValue: job.place) != .session { fact("Group", model.groupName(job.groupID)) }
            fact("Missed runs", job.runMissed ? "Run when the Mac wakes" : "Shown as missed")
        }
    }

    private func fact(_ name: String, _ value: String) -> some View {
        GridRow {
            Text(name).foregroundStyle(theme.dim)
            Text(value).textSelection(.enabled)
        }
    }

    private func section(_ title: String, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).fontWeight(.bold).foregroundStyle(theme.yellow)
            content()
        }
    }

    @ViewBuilder private var runs: some View {
        if model.runs.isEmpty {
            Text("It hasn't run yet.").foregroundStyle(theme.dim)
        } else {
            VStack(alignment: .leading, spacing: 4) {
                ForEach(model.runs) { run in
                    HStack(alignment: .top, spacing: 10) {
                        Text(run.resultLabel).foregroundStyle(color(run)).frame(width: 60, alignment: .leading)
                        Text(JobTime.describe(run.due)).foregroundStyle(theme.dim).frame(width: 130, alignment: .leading)
                        Text(run.detail).foregroundStyle(theme.dim).fixedSize(horizontal: false, vertical: true)
                        Spacer(minLength: 0)
                        if !run.sessionID.isEmpty, model.sessionName(run.sessionID) != nil {
                            Button("Show Session") { model.goToSession(run.sessionID) }.buttonStyle(.link)
                        }
                    }
                }
            }
        }
    }

    private func color(_ run: JobRun) -> Color {
        switch run.result {
        case "started": theme.green
        case "failed": theme.red
        case "missed", "skipped": theme.yellow
        default: theme.dim
        }
    }

    private func label(_ key: String) -> String {
        model.settings.first { $0.key == key }?.label ?? key
    }
}

/// Adds or edits a job.
private struct JobForm: View {
    @Bindable var model: ScheduledModel
    @State private var fileText = ""
    @State private var fileLoaded: String?
    @FocusState private var focus: Field?
    private enum Field { case name }
    private var theme: AppTheme { .shared }

    var body: some View {
        // Not Binding($model.draft): saving sets the draft to nil, and SwiftUI can
        // still read the form's fields once more through that binding, which
        // force-unwraps the nil and ends the app. This one reads a blank draft then.
        if model.draft != nil {
            let draft = Binding(get: { model.draft ?? JobDraft() }, set: { if model.draft != nil { model.draft = $0 } })
            VStack(spacing: 0) {
                HStack {
                    Text(draft.wrappedValue.isNew ? "New job" : "Edit \(model.original?.name ?? "job")").fontWeight(.bold).font(AppFont.ui(15))
                    Spacer()
                    Button("Cancel") { model.cancel() }
                    Button(model.saving ? "Saving…" : "Save") { model.save() }
                        .keyboardShortcut(.return, modifiers: .command)
                        .disabled(!model.canSave)
                        .help(model.check.isValid ? "Save (⌘↩)" : "Fix what's marked in red first")
                }
                .padding(.horizontal, 16)
                .frame(height: 52)
                Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        banners
                        field("Name", error: model.problem("name")) {
                            TextField("", text: draft.name, prompt: hint("Morning brief")).textFieldStyle(.plain).focused($focus, equals: .name)
                                .padding(6).overlay { outline(model.problem("name") != nil) }
                        }
                        field("Description", hint: "Optional. Shown on the run's session.") {
                            TextField("", text: draft.description, prompt: hint("What it does")).textFieldStyle(.plain)
                                .padding(6).overlay { outline(false) }
                        }
                        scheduleField(draft)
                        promptField(draft)
                        placeField(draft)
                        if draft.wrappedValue.place != .session { groupField(draft) }
                        settingsField(draft)
                        Toggle("Run a run the Mac slept through when it wakes (otherwise it's shown as missed)", isOn: draft.runMissed)
                    }
                    .padding(16)
                    .frame(maxWidth: 760, alignment: .leading)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .onAppear { focus = draft.wrappedValue.isNew ? .name : nil }
        }
    }

    @ViewBuilder private var banners: some View {
        if model.confirmingDiscard {
            HStack {
                Label("Throw away your changes?", systemImage: "exclamationmark.triangle").foregroundStyle(theme.yellow)
                Spacer()
                Button("Keep Editing") { model.confirmingDiscard = false }
                Button("Discard", role: .destructive) { model.cancel(force: true) }
            }
            .padding(10)
            .overlay { Rectangle().strokeBorder(theme.yellow, lineWidth: 1) }
        }
        if let error = model.error {
            Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(theme.red)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private func scheduleField(_ draft: Binding<JobDraft>) -> some View {
        field("When", hint: "In words, like \"weekdays at 09:00\", \"every Monday and Friday at 17:30\", \"on the 1st at 07:00\", \"every 2 hours\" or \"once on 2026-12-01 at 14:00\". Or cron, like 0 9 * * 1-5.",
              error: model.problem("schedule")) {
            VStack(alignment: .leading, spacing: 6) {
                TextField("", text: draft.schedule, prompt: hint("weekdays at 09:00")).textFieldStyle(.plain)
                    .font(.system(size: 13, design: .monospaced))
                    .padding(6).overlay { outline(model.problem("schedule") != nil) }
                if model.problem("schedule") == nil, !model.check.next.isEmpty {
                    VStack(alignment: .leading, spacing: 2) {
                        if !model.check.words.isEmpty { Text("In words: \(model.check.words)") }
                        if !model.check.cron.isEmpty { Text("As cron: \(model.check.cron)") }
                        Text("Next: " + model.check.next.prefix(3).map { JobTime.describe($0) }.joined(separator: ", "))
                    }
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.green)
                }
            }
        }
    }

    private func promptField(_ draft: Binding<JobDraft>) -> some View {
        field("Prompt", error: model.problem("prompt")) {
            VStack(alignment: .leading, spacing: 6) {
                Picker("", selection: draft.promptSource) {
                    ForEach(JobDraft.PromptSource.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented).labelsHidden().fixedSize()
                if draft.wrappedValue.promptSource == .text {
                    MarkdownEditor(text: draft.prompt, placeholder: "What should Claude do? Markdown works: headings, lists, **bold**, `code`.")
                        .frame(minHeight: 220)
                        .overlay { outline(model.problem("prompt") != nil) }
                } else {
                    promptFile(draft)
                }
            }
        }
    }

    @ViewBuilder private func promptFile(_ draft: Binding<JobDraft>) -> some View {
        HStack {
            TextField("", text: draft.promptFile, prompt: hint("~/notes/morning.md, or relative to the folder")).textFieldStyle(.plain)
                .padding(6).overlay { outline(model.problem("prompt") != nil) }
            Button("Choose…") { model.choosePromptFile() }
        }
        Text("Read at each run, so editing the file changes the next run.").font(AppFont.ui(11)).foregroundStyle(theme.dim)
        if let path = model.promptFilePath, FileManager.default.fileExists(atPath: path) {
            MarkdownEditor(text: $fileText)
                .frame(minHeight: 200)
                .overlay { outline(false) }
                .onAppear { load(path) }
                .onChange(of: path) { _, p in load(p) }
            HStack {
                Spacer()
                Button("Save File") { _ = model.writePromptFile(fileText) }
                    .disabled(fileText == (model.readPromptFile() ?? fileText))
                    .help("Write your edits back to \(JobTime.tilde(path))")
            }
        }
    }

    private func load(_ path: String) {
        guard fileLoaded != path else { return }
        fileLoaded = path
        fileText = model.readPromptFile() ?? ""
    }

    private func placeField(_ draft: Binding<JobDraft>) -> some View {
        field("Where it runs", hint: draft.wrappedValue.place.help,
              error: model.problem("where") ?? model.problem("cwd") ?? model.problem("session")) {
            VStack(alignment: .leading, spacing: 6) {
                Picker("", selection: draft.place) {
                    ForEach(JobPlace.allCases) { Text($0.label).tag($0) }
                }
                .pickerStyle(.radioGroup).labelsHidden()
                if draft.wrappedValue.place == .session {
                    Picker("Session", selection: draft.sessionID) {
                        Text("Choose a session").tag("")
                        ForEach(model.continuableSessions) { s in
                            Text("\(s.displayName) — \(JobTime.tilde(s.cwd))").tag(s.id)
                        }
                    }
                    .fixedSize()
                } else {
                    HStack {
                        TextField("", text: draft.cwd, prompt: hint(draft.wrappedValue.place == .worktree ? "The repo's folder" : "The folder"))
                            .textFieldStyle(.plain)
                            .padding(6).overlay { outline(model.problem("cwd") != nil) }
                        Button("Choose…") { model.chooseFolder() }
                    }
                }
            }
        }
    }

    private func groupField(_ draft: Binding<JobDraft>) -> some View {
        field("Group", hint: "Where each run's session goes in the sidebar.", error: model.problem("group")) {
            Picker("", selection: draft.groupID) {
                ForEach(model.store.groups) { g in Text(g.name).tag(g.isDefault ? "" : g.id) }
            }
            .labelsHidden().fixedSize()
        }
    }

    private func settingsField(_ draft: Binding<JobDraft>) -> some View {
        field("Claude Code settings", hint: "Empty ones use Claude's own settings.", error: model.problem("settings")) {
            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 8) {
                ForEach(model.settings) { setting in
                    GridRow(alignment: .top) {
                        Text(setting.label).foregroundStyle(theme.dim).padding(.top, 6)
                        VStack(alignment: .leading, spacing: 2) {
                            settingInput(setting, Binding(get: { draft.wrappedValue.settings[setting.key] ?? "" }, set: { draft.wrappedValue.settings[setting.key] = $0 }), draft: draft)
                            if !setting.help.isEmpty {
                                Text(setting.help).font(AppFont.ui(11)).foregroundStyle(theme.dim)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func settingInput(_ setting: AgentSetting, _ value: Binding<String>, draft: Binding<JobDraft>) -> some View {
        switch setting.kind {
        case "choice":
            Picker("", selection: value) {
                Text("Claude's default").tag("")
                ForEach(setting.choices, id: \.self) { Text($0).tag($0) }
            }
            .labelsHidden().fixedSize()
        case "long", "list", "paths":
            HStack(alignment: .top) {
                TextEditor(text: value)
                    .font(.system(size: 12, design: .monospaced))
                    .scrollContentBackground(.hidden)
                    .frame(height: setting.kind == "long" ? 70 : 52)
                    .overlay { outline(false) }
                if setting.kind == "paths" {
                    Button("Add…") { model.addPath(to: setting.key, directory: setting.key == "add-dir") }
                }
            }
            if setting.isList {
                Text("One a line.").font(AppFont.ui(11)).foregroundStyle(theme.dim)
            }
        default:
            HStack {
                TextField("", text: value, prompt: hint(setting.choices.isEmpty ? "Claude's default" : "Claude's default, or " + setting.choices.joined(separator: ", ")))
                    .textFieldStyle(.plain)
                    .padding(6).overlay { outline(false) }
                if setting.kind == "path" {
                    Button("Choose…") {
                        if let path = model.choosePath(false, draft.wrappedValue.cwd) { value.wrappedValue = path }
                    }
                }
            }
        }
    }

    private func field(_ title: String, hint: String? = nil, error: String? = nil, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).fontWeight(.bold)
            content()
            if let error {
                Text(error).font(AppFont.ui(11)).foregroundStyle(theme.red).fixedSize(horizontal: false, vertical: true)
            } else if let hint {
                Text(hint).font(AppFont.ui(11)).foregroundStyle(theme.dim).fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private func outline(_ bad: Bool) -> some View {
        Rectangle().strokeBorder(bad ? theme.red : Color(nsColor: theme.line), lineWidth: 1)
    }

    /// A placeholder, faint so it isn't taken for something typed.
    private func hint(_ text: String) -> Text {
        Text(text).foregroundStyle(theme.dim)
    }
}
