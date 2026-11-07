import SwiftUI

/// The bar along the bottom of the window.
struct StatusBarView: View {
    static let height: CGFloat = 22

    let notifier: AppNotifier
    let store: SessionStore
    let openInbox: () -> Void

    @AppStorage(InboxLayout.key) private var inboxLayout = InboxLayout.columns

    private var theme: AppTheme { .shared }

    /// The version from the app's bundle, which a development build doesn't have.
    private var version: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String).map { "v\($0)" } ?? "dev"
    }

    var body: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            HStack {
                if notifier.isTurnedOff {
                    Button {
                        notifier.openSettings()
                    } label: {
                        Label("Notifications off", systemImage: "exclamationmark.triangle.fill")
                    }
                    .buttonStyle(.plain)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.yellow)
                    .lineLimit(1)
                    .help("Open Agent Command's page in System Settings")
                }
                Spacer()
                popupMenu
                if store.inboxOpen {
                    layoutButton(.columns, icon: "rectangle.split.3x1", help: "Message box in a column")
                    layoutButton(.stacked, icon: "rectangle.split.1x2", help: "Message box at the bottom")
                }
                Button(action: openInbox) {
                    let unread = store.unreadCount()
                    Text(unread > 0 ? "Inbox \(unread)" : "Inbox")
                        .fontWeight(unread > 0 ? .bold : .regular)
                        .foregroundStyle(unread > 0 ? theme.blue : theme.dim)
                }
                .buttonStyle(.plain)
                .font(AppFont.ui(11))
                .lineLimit(1)
                .help("Open the Inbox (⇧⌘I)")
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

    /// Turns the popup on or off, and chooses what opens it.
    private var popupMenu: some View {
        @Bindable var settings = PopupSettings.shared
        return Menu {
            Toggle("Popup on", isOn: $settings.enabled)
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
        .help(settings.enabled ? "Popup on (⌥⌘I)" : "Popup off (⌥⌘I)")
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
