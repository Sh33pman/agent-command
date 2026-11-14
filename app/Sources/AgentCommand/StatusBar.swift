import SwiftUI

/// The bar along the bottom of the window.
struct StatusBarView: View {
    static let height: CGFloat = 22

    let notifier: AppNotifier
    let store: SessionStore
    let openInbox: () -> Void
    var openScheduled: () -> Void = {}

    @AppStorage(InboxLayout.key) private var inboxLayout = InboxLayout.columns
    @State private var showsWarnings = false

    private var theme: AppTheme { .shared }
    private var speech: SpeechController { .shared }

    /// The version from the app's bundle, which a development build doesn't have.
    private var version: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String).map { "v\($0)" } ?? "dev"
    }

    /// "1 needs you · 2 busy": what needs you in color, the rest dim.
    @ViewBuilder private var summary: some View {
        let parts = StatusSummary.parts(store.sessions)
        if !parts.isEmpty {
            HStack(spacing: 0) {
                ForEach(Array(parts.enumerated()), id: \.offset) { index, part in
                    if index > 0 { Text(" · ").foregroundStyle(theme.dim) }
                    Text(part.text)
                        .fontWeight(part.needsYou ? .bold : .regular)
                        .foregroundStyle(part.needsYou ? theme.blue : theme.dim)
                }
            }
            .font(AppFont.ui(11))
            .lineLimit(1)
            .fixedSize()
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            HStack {
                if !notifier.warnings.isEmpty {
                    warningsButton
                }
                summary
                if !store.inboxOpen, let selected = store.selected, selected.statusDetail?.isEmpty == false {
                    Text(selected.statusLine)
                        .font(AppFont.ui(11))
                        .foregroundStyle(theme.dim)
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .help("\(selected.displayName): \(selected.statusLine)")
                }
                if speech.isSettingUp {
                    speechSetup
                }
                if !store.inboxOpen, let failure = speech.failure {
                    Label(failure, systemImage: "exclamationmark.triangle.fill")
                        .font(AppFont.ui(11))
                        .foregroundStyle(theme.yellow)
                        .lineLimit(1)
                        .help(failure)
                        .onTapGesture { speech.stop() }
                }
                Spacer()
                if speech.isEnabled, !store.inboxOpen, store.selected?.isAgent == true {
                    speakButton
                }
                popupMenu
                if store.inboxOpen {
                    layoutButton(.columns, icon: "rectangle.split.3x1", help: "Message box in a column")
                    layoutButton(.stacked, icon: "rectangle.split.1x2", help: "Message box at the bottom")
                }
                Button(action: openScheduled) {
                    let failing = store.jobs.filter { $0.state == .failing }.count
                    Text(failing > 0 ? "Scheduled \(failing) failing" : "Scheduled")
                        .fontWeight(failing > 0 ? .bold : .regular)
                        .foregroundStyle(failing > 0 ? theme.red : theme.dim)
                }
                .buttonStyle(.plain)
                .font(AppFont.ui(11))
                .lineLimit(1)
                .help(Keymap.shared.tip("Scheduled jobs", .scheduled))
                Button(action: openInbox) {
                    let unread = store.unreadCount()
                    Text(unread > 0 ? "Inbox \(unread)" : "Inbox")
                        .fontWeight(unread > 0 ? .bold : .regular)
                        .foregroundStyle(unread > 0 ? theme.blue : theme.dim)
                }
                .buttonStyle(.plain)
                .font(AppFont.ui(11))
                .lineLimit(1)
                .help(Keymap.shared.tip("Open the Inbox", .inbox))
                Text(version)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.dim)
                    .lineLimit(1)
            }
            .padding(.horizontal, 12)
            .frame(maxHeight: .infinity)
        }
        .frame(height: Self.height)
        .background(theme.bg)
    }

    /// Downloading the model has a percentage. Before and after that, a spinner.
    private var speechSetup: some View {
        HStack(spacing: 6) {
            if let progress = speech.setupProgress, progress < 1 {
                Text(setupLabel).fixedSize()
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .frame(width: 100)
                Text("\(Int(progress * 100))%").monospacedDigit().fixedSize()
            } else {
                ProgressView().controlSize(.mini)
                Text(speech.setupProgress == nil ? "\(setupLabel)…" : "Finishing speech setup…").fixedSize()
            }
        }
        .font(AppFont.ui(11))
        .foregroundStyle(theme.dim)
        .lineLimit(1)
        .help("Downloading the speech model, about 360 MB. You can keep working.")
    }

    private var setupLabel: String {
        speech.isUpgrading ? "Upgrading speech" : "Setting up speech"
    }

    /// Yellow while any warning is waiting on you, grey once you've dismissed them all.
    private var warningsButton: some View {
        let waiting = notifier.warnings.contains { !notifier.isDismissed($0) }
        return Button {
            showsWarnings.toggle()
        } label: {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(waiting ? theme.yellow : theme.dim)
        }
        .buttonStyle(.plain)
        .font(AppFont.ui(11))
        .help(waiting ? "Something needs your attention" : "Warnings you've dismissed")
        .popover(isPresented: $showsWarnings, arrowEdge: .top) {
            WarningsView(notifier: notifier)
        }
    }

    /// Reads the latest message from the session on show, or stops.
    private var speakButton: some View {
        Button {
            store.toggleReadingSelected()
        } label: {
            Image(systemName: speech.isSpeaking ? "stop.fill" : "speaker.wave.2").foregroundStyle(theme.fg)
        }
        .buttonStyle(.plain)
        .font(AppFont.ui(11))
        .help(Keymap.shared.tip(speech.isSpeaking ? "Stop reading" : "Read the latest message aloud", .readAloud))
    }

    /// Turns the popup on or off, and chooses what opens it.
    private var popupMenu: some View {
        @Bindable var settings = PopupSettings.shared
        return Menu {
            Toggle("Popup on", isOn: $settings.enabled)
            Toggle("Speech on", isOn: Binding(get: { speech.isEnabled }, set: { speech.setEnabled($0) }))
            Toggle("Read messages aloud in the popup", isOn: $settings.autoPlay)
                .disabled(!speech.isEnabled)
            Picker("Popup style", selection: $settings.style) {
                ForEach(ReplyPopupStyle.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.inline)
            Picker("Opens for", selection: $settings.opens) {
                ForEach(PopupOpens.allCases) { Text($0.label).tag($0) }
            }
            .pickerStyle(.inline)
        } label: {
            Image(systemName: settings.enabled ? "bell" : "bell.slash")
                .foregroundStyle(settings.enabled ? theme.fg : theme.dim)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .font(AppFont.ui(11))
        .help(Keymap.shared.tip(settings.enabled ? "Popup on" : "Popup off", .togglePopup))
    }

    private func layoutButton(_ layout: InboxLayout, icon: String, help: String) -> some View {
        Button {
            inboxLayout = layout
        } label: {
            Image(systemName: icon).foregroundStyle(inboxLayout == layout ? theme.fg : theme.dim)
        }
        .buttonStyle(.plain)
        .font(AppFont.ui(11))
        .help(help)
    }
}

/// The warnings, with what to do about each. Dismissed ones stay, greyed out.
private struct WarningsView: View {
    let notifier: AppNotifier

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ForEach(notifier.warnings) { warning in
                row(warning)
            }
        }
        .font(AppFont.ui(12))
        .padding(14)
        .frame(width: 340, alignment: .leading)
    }

    private func row(_ warning: AppWarning) -> some View {
        let dismissed = notifier.isDismissed(warning)
        return VStack(alignment: .leading, spacing: 6) {
            Label(warning.title, systemImage: "exclamationmark.triangle.fill")
                .fontWeight(.bold)
                .foregroundStyle(dismissed ? theme.dim : theme.yellow)
            Text(warning.detail).foregroundStyle(dismissed ? theme.dim : theme.fg)
            Text(warning.fix).foregroundStyle(dismissed ? theme.dim : theme.fg)
            HStack(spacing: 12) {
                Button(warning.actionTitle, action: warning.action)
                Button(dismissed ? "Show again" : "Dismiss") { notifier.setDismissed(warning, !dismissed) }
            }
            .buttonStyle(.link)
        }
        .fixedSize(horizontal: false, vertical: true)
    }
}
