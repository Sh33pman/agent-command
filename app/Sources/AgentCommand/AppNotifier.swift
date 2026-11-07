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
    /// Whether notifications are turned off for the app in System Settings.
    private(set) var isTurnedOff = false

    @ObservationIgnored private var lastSeq: UInt64?

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
            if status == .notDetermined {
                center.requestAuthorization(options: [.alert, .sound]) { _, error in
                    if let error { NSLog("notification permission: %@", error.localizedDescription) }
                    center.getNotificationSettings { settings in
                        self?.setTurnedOff(settings.authorizationStatus == .denied)
                    }
                }
            } else {
                self?.setTurnedOff(status == .denied)
            }
        }
    }

    private func setTurnedOff(_ off: Bool) {
        DispatchQueue.main.async { self.isTurnedOff = off }
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
        guard var last = lastSeq else {
            lastSeq = notices.last?.seq ?? 0
            return
        }
        // A restarted daemon counts from 1 again.
        if let newest = notices.last, newest.seq < last { last = 0 }
        lastSeq = last
        let fresh = notices.filter { $0.seq > last }
        if let newest = fresh.last { lastSeq = newest.seq }
        guard let center else { return }
        for notice in fresh {
            if notice.isWithdrawal {
                center.removeDeliveredNotifications(withIdentifiers: [notice.sessionID])
            } else if notice.isForApp && !isLooking(notice.sessionID) && !popupShows(notice.kind) {
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
