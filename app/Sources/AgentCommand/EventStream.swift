import Foundation

/// The daemon's state, kept from its event stream (internal/ipc.Event): a
/// snapshot, then each entity added, updated (whole) or removed. The app still
/// reads it as a Snapshot.
struct SnapshotAssembler {
    /// The last revision applied: sent back to resume after a dropped connection.
    private(set) var rev: UInt64 = 0
    private var root: [String: Any] = [:]
    private(set) var hasSnapshot = false

    /// Where each list entity lives in the snapshot, and its id's field.
    private static let lists: [String: (key: String, id: String)] = [
        "session": ("sessions", "id"), "group": ("groups", "id"), "set": ("sets", "id"), "inbox": ("inbox", "id"),
        "job": ("jobs", "id"), "notice": ("notices", "seq"), "remote_host": ("remote_hosts", "name"),
    ]
    /// The single values, and their field in the snapshot.
    private static let singles: [String: String] = [
        "view": "view_state", "settings": "settings", "health": "health", "dismissed": "dismissed", "features": "features",
        "update": "update", "speech": "speech", "remote": "remote", "wake": "wake",
    ]

    /// Applies one event; true when the state changed. An entity type the app
    /// doesn't know is ignored.
    mutating func apply(_ event: [String: Any]) -> Bool {
        if let r = (event["rev"] as? NSNumber)?.uint64Value { rev = r }
        let type = event["type"] as? String ?? ""
        if type == "snapshot" {
            root = event["snapshot"] as? [String: Any] ?? [:]
            hasSnapshot = true
            return true
        }
        guard hasSnapshot, let entity = event["entity"] as? String else { return false }
        let id = event["id"] as? String ?? ""
        let data = event["data"]
        if let field = Self.singles[entity] {
            root[field] = type == "removed" ? nil : data
            return true
        }
        guard let (key, idField) = Self.lists[entity] else { return false }
        var list = root[key] as? [[String: Any]] ?? []
        let at = list.firstIndex { Self.idOf($0[idField]) == id }
        switch type {
        case "added", "updated":
            guard let item = data as? [String: Any] else { return false }
            if let at { list[at] = item } else { list.append(item) }
        case "removed":
            guard let at else { return false }
            list.remove(at: at)
        default:
            return false
        }
        root[key] = list
        return true
    }

    private static func idOf(_ value: Any?) -> String {
        switch value {
        case let s as String: s
        case let n as NSNumber: n.stringValue
        default: ""
        }
    }

    func snapshot() throws -> Snapshot {
        try JSONDecoder().decode(Snapshot.self, from: JSONSerialization.data(withJSONObject: root))
    }
}
