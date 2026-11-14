import AppKit
import ServiceManagement
import SwiftUI

/// The parts of the Settings window, in its sidebar.
enum SettingsSection: String, CaseIterable, Identifiable {
    case general, claude, notifications, popup, speech, scheduled, appearance, keyboard, editor, advanced

    var id: String { rawValue }

    var title: String {
        switch self {
        case .general: "General"
        case .claude: "Claude"
        case .notifications: "Notifications"
        case .popup: "Reply Popup"
        case .speech: "Read Aloud"
        case .scheduled: "Scheduled Jobs"
        case .appearance: "Appearance"
        case .keyboard: "Keyboard"
        case .editor: "Editor"
        case .advanced: "Advanced"
        }
    }

    var icon: String {
        switch self {
        case .general: "gearshape"
        case .claude: "sparkles"
        case .notifications: "bell.badge"
        case .popup: "rectangle.on.rectangle"
        case .speech: "speaker.wave.2"
        case .scheduled: "calendar.badge.clock"
        case .appearance: "paintpalette"
        case .keyboard: "keyboard"
        case .editor: "chevron.left.forwardslash.chevron.right"
        case .advanced: "wrench.and.screwdriver"
        }
    }
}

/// Everything that can be set, in one window.
struct SettingsView: View {
    @Bindable var model: SettingsModel
    let store: SessionStore
    let notifier: AppNotifier
    let shortcuts: ShortcutsModel
    let chooseTheme: () -> Void
    @State var section: SettingsSection = .general

    var body: some View {
        NavigationSplitView {
            List(SettingsSection.allCases, selection: $section) { section in
                Label(section.title, systemImage: section.icon).tag(section)
            }
            .navigationSplitViewColumnWidth(190)
        } detail: {
            VStack(spacing: 0) {
                if let error = model.error {
                    Text(error)
                        .foregroundStyle(.red)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(10)
                }
                page
            }
            .navigationTitle(section.title)
        }
        .onAppear { model.load() }
    }

    @ViewBuilder private var page: some View {
        switch section {
        case .keyboard:
            ShortcutsView(model: shortcuts)
        default:
            Form { form }
                .formStyle(.grouped)
                .disabled(!model.loaded && needsAcmd)
        }
    }

    private var needsAcmd: Bool {
        [.claude, .notifications, .speech, .editor].contains(section)
    }

    @ViewBuilder private var form: some View {
        switch section {
        case .general: GeneralSettings(model: model, notifier: notifier)
        case .claude: ClaudeSettings(model: model)
        case .notifications: NotificationSettings(model: model, notifier: notifier)
        case .popup: PopupSettingsForm()
        case .speech: SpeechSettings(model: model)
        case .scheduled: ScheduledSettings(model: model, store: store)
        case .appearance: AppearanceSettings(model: model, chooseTheme: chooseTheme)
        case .editor: EditorSettings(model: model)
        case .advanced: AdvancedSettings(model: model)
        case .keyboard: EmptyView()
        }
    }
}

/// A toggle for one of acmd's settings: it shows the saved value, and saves a change.
private struct AcmdToggle: View {
    let title: String
    var note = ""
    let key: String
    let model: SettingsModel
    let value: Bool

    var body: some View {
        Toggle(isOn: Binding(get: { value }, set: { model.set(key, $0) })) {
            Text(title)
            if !note.isEmpty { Text(note) }
        }
        .disabled(model.busy == key)
    }
}

private struct GeneralSettings: View {
    let model: SettingsModel
    let notifier: AppNotifier
    @State private var atLogin = SMAppService.mainApp.status == .enabled
    @State private var loginError: String?

    var body: some View {
        Section {
            Toggle(isOn: Binding(get: { atLogin }, set: setAtLogin)) {
                Text("Open at login")
                Text("So the reply popup, notifications and the Dock badge are there from the start.")
            }
            if let loginError { Text(loginError).foregroundStyle(.red) }
        }
        Section {
            AcmdToggle(
                title: "Remote Control for new sessions",
                note: "Use sessions from claude.ai and the Claude app. Applies to sessions started or resumed after the change.",
                key: "remote_control", model: model, value: model.values.remoteControl)
        }
        Section("Warnings") {
            LabeledContent("Warnings you've dismissed") {
                Button("Show Them Again") { notifier.restoreDismissedWarnings() }
                    .disabled(notifier.dismissedWarnings.isEmpty)
            }
        }
    }

    private func setAtLogin(_ on: Bool) {
        do {
            if on { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }
            loginError = nil
        } catch {
            loginError = "Couldn't change it: \(error.localizedDescription)"
        }
        atLogin = SMAppService.mainApp.status == .enabled
    }
}

private struct ClaudeSettings: View {
    let model: SettingsModel
    @State private var command = ""
    @State private var args = ""

    var body: some View {
        Section {
            LabeledContent("Claude Code") {
                HStack {
                    TextField("claude", text: $command).labelsHidden()
                    Button("Save") { model.set("claude_command", command) }
                        .disabled(command == model.values.claudeCommand || command.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            Text("The claude command acmd runs. `acmd init` sets it to the full path it finds.")
                .font(.caption).foregroundStyle(.secondary)
            LabeledContent("Extra arguments") {
                HStack {
                    TextField("--model opus", text: $args).labelsHidden()
                    Button("Save") { model.setArgs(args) }
                        .disabled(ShellWords.split(args) == model.values.claudeArgs)
                }
            }
            Text("Added to every session acmd starts or resumes, after the change. Quote an argument with spaces.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .onAppear(perform: fill)
        .onChange(of: model.values) { fill() }
    }

    private func fill() {
        command = model.values.claudeCommand
        args = ShellWords.join(model.values.claudeArgs)
    }
}

private struct NotificationSettings: View {
    let model: SettingsModel
    let notifier: AppNotifier

    var body: some View {
        let v = model.values
        if notifier.isTurnedOff {
            Section {
                LabeledContent("macOS has notifications turned off for Agent Command.") {
                    Button("Open System Settings") { notifier.openSettings() }
                }
            }
        }
        Section {
            AcmdToggle(title: "Notifications", note: "When the app isn't showing the session.", key: "notify.enabled",
                       model: model, value: v.notify)
        }
        Section("Tell me when a session") {
            AcmdToggle(title: "Needs me", note: "A question or a permission.", key: "notify.on_waiting", model: model, value: v.onWaiting)
            AcmdToggle(title: "Fails", key: "notify.on_error", model: model, value: v.onError)
            AcmdToggle(title: "Finishes a turn", key: "notify.on_done", model: model, value: v.onDone)
            AcmdToggle(title: "Stops", key: "notify.on_paused", model: model, value: v.onPaused)
        }
        .disabled(!v.notify)
        Section {
            wait("Only for turns longer than", key: "notify.done_min_duration", current: v.doneMin, none: "Every turn")
                .disabled(!v.onDone)
            wait("At most one per session every", key: "notify.cooldown", current: v.cooldown, none: "No limit")
            Toggle("Play a sound", isOn: Binding(get: { !v.sound.isEmpty }, set: { model.set("notify.sound", $0 ? "default" : "") }))
                .disabled(model.busy == "notify.sound")
        }
        .disabled(!v.notify)
    }

    private func wait(_ title: String, key: String, current: String, none: String) -> some View {
        Picker(title, selection: Binding(
            get: { GoDuration.seconds(current) ?? 0 },
            set: { model.set(key, "\(Int($0))s") }
        )) {
            ForEach(model.waitChoices(current: current), id: \.self) { seconds in
                Text(seconds == 0 ? none : GoDuration.label(seconds)).tag(seconds)
            }
        }
        .disabled(model.busy == key)
    }
}

private struct PopupSettingsForm: View {
    @Bindable private var settings = PopupSettings.shared

    var body: some View {
        Section {
            Toggle(isOn: $settings.enabled) {
                Text("Reply popup")
                Text("When you're in another app, what your agents send opens in a window over everything. While it's off, you get notifications instead.")
            }
        }
        Section {
            Picker("Opens for", selection: $settings.opens) {
                ForEach(PopupOpens.allCases) { Text($0.label).tag($0) }
            }
            Picker("Style", selection: $settings.style) {
                ForEach(ReplyPopupStyle.allCases) { Text($0.label).tag($0) }
            }
            Toggle("Read each message aloud as it's shown", isOn: $settings.autoPlay)
                .disabled(!SpeechController.shared.isEnabled)
            LabeledContent("Open it from any app", value: Keymap.shared.label(.popupAnywhere))
        }
        .disabled(!settings.enabled)
    }
}

private struct SpeechSettings: View {
    let model: SettingsModel
    private var speech: SpeechController { .shared }

    var body: some View {
        let v = model.values
        Section {
            Toggle(isOn: Binding(get: { speech.isEnabled }, set: { speech.setEnabled($0) })) {
                Text("Read aloud")
                Text("A local voice (Kokoro-82M on MLX) reads messages. Turning it on downloads the model, about 360 MB.")
            }
            if speech.isSettingUp {
                if let progress = speech.setupProgress {
                    ProgressView("Downloading…", value: progress)
                } else {
                    ProgressView("Setting up…")
                }
            } else if let failure = speech.setupFailure {
                LabeledContent {
                    Button("Try Again") { speech.retrySetup() }
                } label: {
                    Text(failure).foregroundStyle(.red)
                }
            }
        }
        Section {
            Picker("Voice", selection: Binding(get: { v.voice }, set: { model.setVoice($0) })) {
                if !model.acmd.voices.contains(v.voice) {
                    Text(v.voice.contains(",") ? "A blend: \(v.voice)" : v.voice).tag(v.voice)
                }
                ForEach(model.acmd.voices, id: \.self) { Text(Self.voiceLabel($0)).tag($0) }
            }
            .disabled(model.busy == "speech.voice")
            SpeedSlider(model: model, saved: v.speed)
            LabeledContent("Hear it") {
                Button("Play a Sample") { model.playSample() }
                    .disabled(!speech.isEnabled || speech.isSettingUp)
            }
            Text("American English voices only for now. A new voice is downloaded when it's chosen.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .disabled(!speech.isEnabled)
    }

    /// "Heart (female)" from "af_heart".
    static func voiceLabel(_ voice: String) -> String {
        let parts = voice.split(separator: "_", maxSplits: 1)
        guard parts.count == 2 else { return voice }
        let kind = parts[0].hasSuffix("f") ? "female" : "male"
        return "\(parts[1].capitalized) (\(kind))"
    }
}

private struct ScheduledSettings: View {
    let model: SettingsModel
    let store: SessionStore

    var body: some View {
        if let wake = store.wake {
            Section {
                Toggle(isOn: Binding(get: { wake.allowed }, set: { model.setWake($0) })) {
                    Text("Wake the Mac for jobs")
                    Text("Wakes it two minutes before a run. Turning it on or off asks for your password once, to add or remove a sudoers rule that only lets acmd set and cancel its own wakes.")
                }
                .disabled(model.busy == "wake")
                Picker("Wake for", selection: Binding(get: { wake.all }, set: { model.setWakeAll($0) })) {
                    Text("Every job").tag(true)
                    Text("Only jobs set to wake it").tag(false)
                }
                .disabled(!wake.allowed || model.busy == "wake")
                if !wake.next.isEmpty { LabeledContent("Next wake", value: wake.next) }
                if !wake.error.isEmpty { Text(wake.error).foregroundStyle(.red) }
            }
        } else {
            Section {
                Text("acmd's background service can't wake this Mac.").foregroundStyle(.secondary)
            }
        }
    }
}

private struct AppearanceSettings: View {
    let model: SettingsModel
    let chooseTheme: () -> Void
    @State private var family = ""
    @State private var size = 13.0

    var body: some View {
        Section {
            LabeledContent("Theme") {
                Button("Choose…", action: chooseTheme)
            }
            Text("Ghostty's themes, previewed as you move through them.").font(.caption).foregroundStyle(.secondary)
        }
        Section("Font") {
            Picker("Family", selection: Binding(get: { family }, set: { family = $0; model.setFontFamily($0) })) {
                ForEach(SettingsModel.monospacedFamilies, id: \.self) { Text($0).tag($0) }
            }
            Stepper(value: Binding(get: { size }, set: { size = $0; model.setFontSize($0) }), in: 8...32, step: 1) {
                LabeledContent("Size", value: "\(Int(size)) pt")
            }
            Button("Use the Defaults") {
                model.setFontFamily(nil)
                model.setFontSize(nil)
                fill()
            }
            Text("Terminals change straight away; the app's own text follows the family after a restart.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .onAppear(perform: fill)
    }

    private func fill() {
        family = model.fontFamily
        size = model.fontSize
    }
}

private struct EditorSettings: View {
    let model: SettingsModel
    @State private var custom = ""

    static let presets: [(String, String)] = [
        ("", "Ask the first time"), ("vscode", "Visual Studio Code"), ("cursor", "Cursor"), ("zed", "Zed"),
        ("idea", "IntelliJ IDEA"), ("custom", "Another command"),
    ]

    var body: some View {
        let v = model.values
        Section {
            Picker("Open folders in", selection: Binding(
                get: { v.editorPreset },
                set: { preset in
                    if preset == "custom" {
                        custom = custom.isEmpty ? "nvim {dir}" : custom
                    } else if !preset.isEmpty {
                        model.setEditor(preset)
                    }
                })) {
                    ForEach(Self.presets, id: \.0) { Text($0.1).tag($0.0) }
                }
                .disabled(model.busy == "editor.preset")
            if v.editorPreset == "custom" || !custom.isEmpty {
                LabeledContent("Command") {
                    HStack {
                        TextField("nvim {dir}", text: $custom).labelsHidden()
                        Button("Save") { model.setEditor("custom", command: ShellWords.split(custom)) }
                            .disabled(ShellWords.split(custom).isEmpty)
                    }
                }
                Text("{dir} is the folder; it's added at the end if left out.").font(.caption).foregroundStyle(.secondary)
            }
        }
        Section {
            Picker("Runs", selection: Binding(get: { v.editorRunIn }, set: { model.set("editor.run_in", $0) })) {
                Text("In the background (an app)").tag("background")
                Text("In a terminal tab").tag("tab")
            }
            Picker("Opens", selection: Binding(get: { v.editorTarget }, set: { model.set("editor.target", $0) })) {
                Text("The session's folder").tag("cwd")
                Text("Its git repository's top folder").tag("git_root")
            }
        }
        .onAppear { if v.editorPreset == "custom" { custom = ShellWords.join(v.editorCommand) } }
    }
}

private struct AdvancedSettings: View {
    let model: SettingsModel

    var body: some View {
        Section("Setup") {
            LabeledContent("Claude Code hooks and the background service") {
                HStack {
                    Button("Check") { model.checkSetup() }
                    Button("Repair") { model.repairSetup() }
                }
                .disabled(model.busy == "doctor")
            }
            if model.busy == "doctor" { ProgressView().controlSize(.small) }
            if let report = model.report {
                Text(report)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        Section("Files") {
            file("acmd's settings", path: model.acmd.path)
            file("Ghostty settings for the app's terminals", path: AppFont.configPath)
        }
    }

    private func file(_ title: String, path: String) -> some View {
        LabeledContent {
            HStack {
                Button("Open") { NSWorkspace.shared.open(URL(fileURLWithPath: path)) }
                Button("Show in Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)]) }
            }
            .disabled(path.isEmpty || !FileManager.default.fileExists(atPath: path))
        } label: {
            Text(title)
            Text(NSString(string: path).abbreviatingWithTildeInPath)
        }
    }
}

/// Saves the speed once you let go, not at every step on the way.
private struct SpeedSlider: View {
    let model: SettingsModel
    let saved: Double
    @State private var speed = 1.0

    var body: some View {
        LabeledContent("Speed") {
            HStack {
                Slider(value: $speed, in: 0.5...2, step: 0.05) { editing in
                    if !editing, speed != saved { model.set("speech.speed", String(format: "%.2f", speed)) }
                }
                .frame(maxWidth: 220)
                Text(String(format: "%.2f×", speed)).monospacedDigit().frame(width: 48, alignment: .trailing)
            }
        }
        .onAppear { speed = saved }
        .onChange(of: saved) { speed = saved }
    }
}
