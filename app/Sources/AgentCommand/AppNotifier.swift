import AppKit
import Observation
import UserNotifications

/// Shows the daemon's notices as macOS notifications, and opens the session when
/// one is clicked. The daemon decides when to notify; this only decides whether
/// you're already looking.
@Observable
final class AppNotifier: NSObject, UNUserNotificationCenterDelegate {
    /// Whether you're looking at this session right now.
    @ObservationIgnored var isLooking: (String) -> Bool = { _ in false }
    /// Whether the popup shows this kind of notice, so a banner would repeat it.
    @ObservationIgnored var popupShows: (String) -> Bool = { _ in false }
    /// Called with the session of a clicked notification.
    @ObservationIgnored var onOpen: ((String) -> Void)?
    /// Warnings from elsewhere in the app (scheduled jobs that can't wake the Mac).
    @ObservationIgnored var moreWarnings: () -> [AppWarning] = { [] }
    /// Whether notifications are turned off for the app in System Settings.
    private(set) var isTurnedOff = false
    /// Whether the Dock icon can't show its count. Only you can change that, in System
    /// Settings: macOS doesn't let an app turn it on.
    private(set) var isBadgeOff = false
    /// The warnings you've dismissed, which stay listed, greyed out.
    private(set) var dismissedWarnings = Set(UserDefaults.standard.stringArray(forKey: AppNotifier.dismissedKey) ?? [])

    @ObservationIgnored private var lastSeq: UInt64?
    private static let dismissedKey = "warnings.dismissed"

    /// Only an app bundle can post, so a development build shows nothing.
    private var center: UNUserNotificationCenter? {
        Bundle.main.bundleIdentifier == nil ? nil : .current()
    }

    func start() {
        guard let center else { return }
        center.delegate = self
        refreshPermission()
    }

    /// Asks if macOS hasn't asked yet, which it does only once, and otherwise notes
    /// the answer. You can change it in System Settings at any time, so the app
    /// calls this whenever it comes to the front.
    func refreshPermission() {
        guard let center else { return }
        center.getNotificationSettings { [weak self] settings in
            let status = settings.authorizationStatus
            // Earlier versions didn't ask for badges, so those installs are asked again.
            if status == .notDetermined || (status == .authorized && settings.badgeSetting == .notSupported) {
                center.requestAuthorization(options: [.alert, .sound, .badge]) { _, error in
                    if let error { NSLog("notification permission: %@", error.localizedDescription) }
                    center.getNotificationSettings { settings in
                        self?.setTurnedOff(settings)
                    }
                }
            } else {
                self?.setTurnedOff(settings)
            }
        }
    }

    private func setTurnedOff(_ settings: UNNotificationSettings) {
        let off = settings.authorizationStatus == .denied
        let badgeOff = settings.authorizationStatus == .authorized && settings.badgeSetting != .enabled
        DispatchQueue.main.async { self.setPermission(turnedOff: off, badgeOff: badgeOff) }
    }

    func setPermission(turnedOff: Bool, badgeOff: Bool) {
        isTurnedOff = turnedOff
        isBadgeOff = badgeOff
    }

    /// What needs your attention, in the app's settings.
    var warnings: [AppWarning] {
        var list: [AppWarning] = []
        if isTurnedOff {
            list.append(AppWarning(
                id: "notifications", title: "Notifications are off",
                detail: "Banners can't appear when a session needs you and the popup is off or closed.",
                fix: "Open System Settings, then turn on Allow Notifications.",
                actionTitle: "Open System Settings", action: { [weak self] in self?.openSettings() }))
        }
        if isBadgeOff {
            list.append(AppWarning(
                id: "dock-badge", title: "Dock badge is off",
                detail: "The Dock icon can't show how many messages are unread.",
                fix: "Open System Settings, then turn on Badge application icon.",
                actionTitle: "Open System Settings", action: { [weak self] in self?.openSettings() }))
        }
        if let failure = SpeechController.shared.setupFailure {
            list.append(AppWarning(
                id: "speech-setup", title: "Speech couldn't be set up",
                detail: "Messages can't be read aloud until it's done. \(failure)",
                fix: "Check your connection, then try again.",
                actionTitle: "Try again", action: { SpeechController.shared.retrySetup() }))
        }
        return list + moreWarnings()
    }

    func isDismissed(_ warning: AppWarning) -> Bool {
        dismissedWarnings.contains(warning.id)
    }

    func setDismissed(_ warning: AppWarning, _ dismissed: Bool) {
        if dismissed { dismissedWarnings.insert(warning.id) } else { dismissedWarnings.remove(warning.id) }
        UserDefaults.standard.set(Array(dismissedWarnings), forKey: Self.dismissedKey)
    }

    /// Opens the app's page in System Settings.
    func openSettings() {
        let id = Bundle.main.bundleIdentifier ?? ""
        if let url = URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=\(id)") {
            NSWorkspace.shared.open(url)
        }
    }

    /// Takes the daemon's recent notices, oldest first. Those from before this
    /// launch aren't news, so they aren't shown.
    func receive(_ notices: [Notice]) {
        let fresh = newNotices(in: notices)
        guard let center else { return }
        for notice in fresh {
            if notice.isWithdrawal {
                center.removeDeliveredNotifications(withIdentifiers: [notice.sessionID])
            } else if shouldShow(notice) {
                let content = UNMutableNotificationContent()
                content.title = notice.title
                content.body = notice.body
                content.sound = notice.sound.isEmpty ? nil : .default
                content.userInfo = ["session": notice.sessionID]
                // By session, so a newer one replaces the older.
                center.add(UNNotificationRequest(identifier: notice.sessionID, content: content, trigger: nil)) { error in
                    if let error { NSLog("notification: %@", error.localizedDescription) }
                }
            }
        }
    }

    /// The notices not seen yet. On the first call that's none.
    func newNotices(in notices: [Notice]) -> [Notice] {
        guard var last = lastSeq else {
            lastSeq = notices.last?.seq ?? 0
            return []
        }
        // A restarted daemon counts from 1 again.
        if let newest = notices.last, newest.seq < last { last = 0 }
        lastSeq = last
        let fresh = notices.filter { $0.seq > last }
        if let newest = fresh.last { lastSeq = newest.seq }
        return fresh
    }

    /// Whether a notice becomes a banner: not if you're looking, or the popup shows it.
    func shouldShow(_ notice: Notice) -> Bool {
        notice.isForApp && !isLooking(notice.sessionID) && !popupShows(notice.kind)
    }

    /// Shows it even with the app in front: you may be looking at another session.
    func userNotificationCenter(
        _ center: UNUserNotificationCenter, willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        if let session = response.notification.request.content.userInfo["session"] as? String {
            DispatchQueue.main.async { self.onOpen?(session) }
        }
        completionHandler()
    }
}

/// Something in the app's settings that stops a feature working.
struct AppWarning: Identifiable {
    let id: String
    let title: String
    /// What doesn't work because of it.
    let detail: String
    /// What to do about it.
    let fix: String
    /// What the warning's button says and does.
    let actionTitle: String
    let action: () -> Void
}
