import AppKit
import Carbon.HIToolbox
import Observation

/// A key and the modifiers held with it. The key is a key code, so it doesn't
/// depend on the keyboard layout's characters.
struct KeyCombo: Hashable {
    struct Mods: OptionSet, Hashable {
        let rawValue: Int
        static let control = Mods(rawValue: 1)
        static let option = Mods(rawValue: 2)
        static let shift = Mods(rawValue: 4)
        static let command = Mods(rawValue: 8)
    }

    let key: UInt16
    let mods: Mods

    init(key: UInt16, mods: Mods = []) {
        // Enter on the keypad is return.
        self.key = key == UInt16(kVK_ANSI_KeypadEnter) ? UInt16(kVK_Return) : key
        self.mods = mods
    }

    init(event: NSEvent) {
        self.init(key: event.keyCode, mods: Mods(event.modifierFlags))
    }

    /// Reads "ctrl+cmd+j", as the settings keep it.
    init?(_ text: String) {
        var parts = text.lowercased().split(separator: "+").map(String.init)
        guard let last = parts.popLast(), let key = KeyName.named(last) else { return nil }
        var mods: Mods = []
        for part in parts {
            switch part {
            case "ctrl", "control": mods.insert(.control)
            case "opt", "option", "alt": mods.insert(.option)
            case "shift": mods.insert(.shift)
            case "cmd", "command": mods.insert(.command)
            default: return nil
            }
        }
        self.init(key: key.code, mods: mods)
    }

    var name: KeyName? { KeyName.code(key) }

    /// "ctrl+cmd+j": what the settings keep.
    var text: String {
        var parts: [String] = []
        if mods.contains(.control) { parts.append("ctrl") }
        if mods.contains(.option) { parts.append("opt") }
        if mods.contains(.shift) { parts.append("shift") }
        if mods.contains(.command) { parts.append("cmd") }
        parts.append(name?.name ?? "key\(key)")
        return parts.joined(separator: "+")
    }

    /// "⌃⌘J", in the order macOS's menus use. A plain letter stays small: "x".
    var display: String {
        var out = ""
        if mods.contains(.control) { out += "⌃" }
        if mods.contains(.option) { out += "⌥" }
        if mods.contains(.shift) { out += "⇧" }
        if mods.contains(.command) { out += "⌘" }
        guard let name else { return out + "key \(key)" }
        return out + (mods.isEmpty ? name.plain : name.symbol)
    }

    /// Whether pressing it in a text field would type: a character, with shift or
    /// option at most.
    var types: Bool {
        (name?.isCharacter ?? false) && mods.isDisjoint(with: [.command, .control])
    }

    /// What a menu item needs to show and answer it, if a menu can.
    var menuKey: (key: String, mask: NSEvent.ModifierFlags)? {
        guard let menu = name?.menu else { return nil }
        var mask: NSEvent.ModifierFlags = []
        if mods.contains(.control) { mask.insert(.control) }
        if mods.contains(.option) { mask.insert(.option) }
        if mods.contains(.shift) { mask.insert(.shift) }
        if mods.contains(.command) { mask.insert(.command) }
        return (menu, mask)
    }

    /// Carbon's modifiers, for a shortcut that works from any app.
    var carbonMods: Int {
        var out = 0
        if mods.contains(.control) { out |= controlKey }
        if mods.contains(.option) { out |= optionKey }
        if mods.contains(.shift) { out |= shiftKey }
        if mods.contains(.command) { out |= cmdKey }
        return out
    }
}

extension KeyCombo.Mods {
    init(_ flags: NSEvent.ModifierFlags) {
        var mods: KeyCombo.Mods = []
        if flags.contains(.control) { mods.insert(.control) }
        if flags.contains(.option) { mods.insert(.option) }
        if flags.contains(.shift) { mods.insert(.shift) }
        if flags.contains(.command) { mods.insert(.command) }
        self = mods
    }
}

/// A key the shortcuts can use, by its US layout name.
struct KeyName {
    let code: UInt16
    /// What the settings call it: "j", "return".
    let name: String
    /// How it's shown with modifiers: "J", "↩".
    let symbol: String
    /// How it's shown on its own: "j", "↩".
    let plain: String
    /// A menu item's key equivalent for it, if a menu can use it.
    let menu: String?
    let isCharacter: Bool

    static func named(_ name: String) -> KeyName? { byName[name] }
    static func code(_ code: UInt16) -> KeyName? { byCode[code] }

    private static func function(_ key: Int) -> String { String(Character(UnicodeScalar(key)!)) }

    static let all: [KeyName] = {
        var keys: [KeyName] = []
        let letters: [(String, Int)] = [
            ("a", kVK_ANSI_A), ("b", kVK_ANSI_B), ("c", kVK_ANSI_C), ("d", kVK_ANSI_D), ("e", kVK_ANSI_E),
            ("f", kVK_ANSI_F), ("g", kVK_ANSI_G), ("h", kVK_ANSI_H), ("i", kVK_ANSI_I), ("j", kVK_ANSI_J),
            ("k", kVK_ANSI_K), ("l", kVK_ANSI_L), ("m", kVK_ANSI_M), ("n", kVK_ANSI_N), ("o", kVK_ANSI_O),
            ("p", kVK_ANSI_P), ("q", kVK_ANSI_Q), ("r", kVK_ANSI_R), ("s", kVK_ANSI_S), ("t", kVK_ANSI_T),
            ("u", kVK_ANSI_U), ("v", kVK_ANSI_V), ("w", kVK_ANSI_W), ("x", kVK_ANSI_X), ("y", kVK_ANSI_Y),
            ("z", kVK_ANSI_Z),
        ]
        for (name, code) in letters {
            keys.append(KeyName(code: UInt16(code), name: name, symbol: name.uppercased(), plain: name, menu: name, isCharacter: true))
        }
        let characters: [(String, Int)] = [
            ("1", kVK_ANSI_1), ("2", kVK_ANSI_2), ("3", kVK_ANSI_3), ("4", kVK_ANSI_4), ("5", kVK_ANSI_5),
            ("6", kVK_ANSI_6), ("7", kVK_ANSI_7), ("8", kVK_ANSI_8), ("9", kVK_ANSI_9), ("0", kVK_ANSI_0),
            ("-", kVK_ANSI_Minus), ("=", kVK_ANSI_Equal), ("[", kVK_ANSI_LeftBracket), ("]", kVK_ANSI_RightBracket),
            (";", kVK_ANSI_Semicolon), ("'", kVK_ANSI_Quote), (",", kVK_ANSI_Comma), (".", kVK_ANSI_Period),
            ("/", kVK_ANSI_Slash), ("\\", kVK_ANSI_Backslash), ("`", kVK_ANSI_Grave),
        ]
        for (name, code) in characters {
            keys.append(KeyName(code: UInt16(code), name: name, symbol: name, plain: name, menu: name, isCharacter: true))
        }
        let named: [(String, Int, String, String?, Bool)] = [
            ("return", kVK_Return, "↩", "\r", false),
            ("tab", kVK_Tab, "⇥", "\t", false),
            ("space", kVK_Space, "Space", " ", true),
            ("delete", kVK_Delete, "⌫", "\u{8}", false),
            ("forwarddelete", kVK_ForwardDelete, "⌦", function(NSDeleteFunctionKey), false),
            ("escape", kVK_Escape, "esc", "\u{1b}", false),
            ("left", kVK_LeftArrow, "←", function(NSLeftArrowFunctionKey), false),
            ("right", kVK_RightArrow, "→", function(NSRightArrowFunctionKey), false),
            ("down", kVK_DownArrow, "↓", function(NSDownArrowFunctionKey), false),
            ("up", kVK_UpArrow, "↑", function(NSUpArrowFunctionKey), false),
            ("home", kVK_Home, "↖", function(NSHomeFunctionKey), false),
            ("end", kVK_End, "↘", function(NSEndFunctionKey), false),
            ("pageup", kVK_PageUp, "⇞", function(NSPageUpFunctionKey), false),
            ("pagedown", kVK_PageDown, "⇟", function(NSPageDownFunctionKey), false),
        ]
        for (name, code, symbol, menu, isCharacter) in named {
            keys.append(KeyName(code: UInt16(code), name: name, symbol: symbol, plain: symbol, menu: menu, isCharacter: isCharacter))
        }
        let functionKeys = [kVK_F1, kVK_F2, kVK_F3, kVK_F4, kVK_F5, kVK_F6, kVK_F7, kVK_F8, kVK_F9, kVK_F10, kVK_F11, kVK_F12]
        for (index, code) in functionKeys.enumerated() {
            let name = "F\(index + 1)"
            keys.append(KeyName(
                code: UInt16(code), name: name.lowercased(), symbol: name, plain: name,
                menu: function(NSF1FunctionKey + index), isCharacter: false))
        }
        return keys
    }()

    private static let byName = Dictionary(uniqueKeysWithValues: all.map { ($0.name, $0) })
    private static let byCode = Dictionary(uniqueKeysWithValues: all.map { ($0.code, $0) })

    /// The number row's 1 to 9, in order.
    static let digitCodes: [UInt16] = (1...9).map { named("\($0)")!.code }

    var isFunctionKey: Bool { name.hasPrefix("f") && name.count > 1 }
}

/// The parts of the keyboard's map, as the shortcuts list groups them.
enum KeySection: String, CaseIterable {
    case anyApp, app, panes, goTo, lists, inbox, scheduled, editing

    var title: String {
        switch self {
        case .anyApp: "From any app"
        case .app: "App"
        case .panes: "Panes"
        case .goTo: "Go to"
        case .lists: "Lists: the sidebar, Inbox, popup and Scheduled"
        case .inbox: "Inbox and popup"
        case .scheduled: "Scheduled"
        case .editing: "Writing a reply or a job"
        }
    }

    /// The keys active at the same time as each other, so they mustn't share a shortcut.
    fileprivate var scope: Int {
        switch self {
        case .anyApp: 0
        case .app, .panes, .goTo: 1
        case .lists: 2
        case .inbox: 3
        case .scheduled: 4
        case .editing: 5
        }
    }

    /// A shortcut from any app is active everywhere. The menus are active with the
    /// lists, the Inbox and Scheduled; the list keys with the Inbox's and Scheduled's
    /// own. A reply or job being written takes its own keys first: ⌘↩ sends there and
    /// splits elsewhere.
    func overlaps(_ other: KeySection) -> Bool {
        let (a, b) = (min(scope, other.scope), max(scope, other.scope))
        switch (a, b) {
        case (0, _): return true
        case (1, 5): return false
        case (1, _), (2, 2...4): return true
        default: return a == b
        }
    }
}

/// Everything a shortcut can be set for.
enum KeyAction: String, CaseIterable, Identifiable {
    case popupAnywhere = "global.popup"
    case appAnywhere = "global.app"

    case newClaude = "app.new-claude"
    case newTerminal = "app.new-terminal"
    case resume = "app.resume"
    case commands = "app.commands"
    case themes = "app.themes"
    case focusSidebar = "app.focus-sidebar"
    case inbox = "app.inbox"
    case scheduled = "app.scheduled"
    case readAloud = "app.read-aloud"
    case rename = "app.rename"
    case archive = "app.archive"
    case togglePopup = "app.toggle-popup"
    case shortcuts = "app.shortcuts"
    case quit = "app.quit"

    case splitRight = "panes.split-right"
    case splitDown = "panes.split-down"
    case closePane = "panes.close"
    case focusLeft = "panes.focus-left"
    case focusDown = "panes.focus-down"
    case focusUp = "panes.focus-up"
    case focusRight = "panes.focus-right"
    case equalize = "panes.equalize"

    case nextAttention = "go.next-attention"
    case session1 = "go.session-1", session2 = "go.session-2", session3 = "go.session-3"
    case session4 = "go.session-4", session5 = "go.session-5", session6 = "go.session-6"
    case session7 = "go.session-7", session8 = "go.session-8", session9 = "go.session-9"

    case up = "list.up"
    case down = "list.down"
    case left = "list.left"
    case right = "list.right"
    case open = "list.open"
    case back = "list.back"
    case mark = "list.mark"
    case nextNeedingYou = "list.next"
    case go = "list.go"
    case remove = "list.remove"

    case reply = "inbox.reply"
    case dismiss = "inbox.dismiss"
    case unread = "inbox.unread"

    case newJob = "jobs.new"
    case editJob = "jobs.edit"
    case runJob = "jobs.run"
    case pauseJob = "jobs.pause"
    case duplicateJob = "jobs.duplicate"

    case save = "editing.save"
    case leave = "editing.leave"

    var id: String { rawValue }

    static let sessions: [KeyAction] = [.session1, .session2, .session3, .session4, .session5, .session6, .session7, .session8, .session9]

    /// What acts on the panes, which mean nothing while the Inbox or Scheduled covers them.
    static let paneActions: [KeyAction] = [.splitRight, .splitDown, .focusLeft, .focusDown, .focusUp, .focusRight, .equalize]

    var section: KeySection {
        switch rawValue.split(separator: ".").first {
        case "global": .anyApp
        case "app": .app
        case "panes": .panes
        case "go": .goTo
        case "list": .lists
        case "inbox": .inbox
        case "jobs": .scheduled
        default: .editing
        }
    }

    var title: String {
        switch self {
        case .popupAnywhere: "Open the popup, or the Inbox in the app"
        case .appAnywhere: "Bring the app forward, or hide it"
        case .newClaude: "New Claude session"
        case .newTerminal: "New terminal"
        case .resume: "Resume a session"
        case .commands: "Commands"
        case .themes: "Change theme"
        case .focusSidebar: "Focus the sidebar"
        case .inbox: "Inbox"
        case .scheduled: "Scheduled jobs"
        case .readAloud: "Read aloud, or stop reading"
        case .rename: "Rename"
        case .archive: "Archive"
        case .togglePopup: "Turn the popup on or off"
        case .shortcuts: "Keyboard shortcuts"
        case .quit: "Quit"
        case .splitRight: "Split right"
        case .splitDown: "Split down"
        case .closePane: "Close pane"
        case .focusLeft: "Focus the pane on the left"
        case .focusDown: "Focus the pane below"
        case .focusUp: "Focus the pane above"
        case .focusRight: "Focus the pane on the right"
        case .equalize: "Equalize panes"
        case .nextAttention: "Next session needing attention"
        case .up: "Move up"
        case .down: "Move down"
        case .left: "Previous option"
        case .right: "Next option"
        case .open: "Open, or answer"
        case .back: "Back, or close"
        case .mark: "Mark"
        case .nextNeedingYou: "Next that needs you"
        case .go: "Go to the session"
        case .remove: "Dismiss or delete"
        case .reply: "Reply"
        case .dismiss: "Dismiss"
        case .unread: "Mark read or unread"
        case .newJob: "New job"
        case .editJob: "Edit the job"
        case .runJob: "Run the job now"
        case .pauseJob: "Pause or resume the job"
        case .duplicateJob: "Duplicate the job"
        case .save: "Send the reply, or save the job"
        case .leave: "Leave without sending or saving"
        default:
            "Show session \((KeyAction.sessions.firstIndex(of: self) ?? 0) + 1)"
        }
    }

    /// What it does where it means something else, said in the shortcuts list.
    var note: String {
        switch self {
        case .popupAnywhere, .appAnywhere: "Works in every app."
        case .focusSidebar: "In the Inbox: switches between sessions and messages."
        case .rename: "In Scheduled: edits the job."
        case .archive: "In Scheduled: deletes the job."
        case .closePane: "Closes the Inbox, the popup or Scheduled when one is open."
        case .open: "In the Inbox, on the open message: answer or reply. In Scheduled: edit."
        case .nextNeedingYou: "In the sidebar: the next session. In the Inbox: the next unread message."
        case .go: "From a message, or a job's last run."
        case .remove: "Dismisses a message, or deletes a job."
        case .left, .right: "While answering with an option."
        default: ""
        }
    }

    var defaults: [String] {
        switch self {
        case .popupAnywhere: ["ctrl+cmd+o"]
        case .appAnywhere: ["ctrl+cmd+a"]
        case .newClaude: ["cmd+n"]
        case .newTerminal: ["cmd+t"]
        case .resume: ["shift+cmd+o"]
        case .commands: ["shift+cmd+p"]
        case .themes: ["shift+cmd+t"]
        case .focusSidebar: ["shift+cmd+b"]
        case .inbox: ["shift+cmd+i"]
        case .scheduled: ["shift+cmd+j"]
        case .readAloud: ["shift+cmd+s"]
        case .rename: ["cmd+r"]
        case .archive: ["shift+cmd+delete"]
        case .togglePopup: ["opt+cmd+i"]
        case .shortcuts: ["cmd+/"]
        case .quit: ["cmd+q"]
        case .splitRight: ["cmd+return"]
        case .splitDown: ["shift+cmd+return"]
        case .closePane: ["cmd+w"]
        case .focusLeft: ["ctrl+cmd+h"]
        case .focusDown: ["ctrl+cmd+j"]
        case .focusUp: ["ctrl+cmd+k"]
        case .focusRight: ["ctrl+cmd+l"]
        case .equalize: ["ctrl+cmd+="]
        case .nextAttention: ["shift+cmd+a"]
        case .up: ["up", "ctrl+k", "k"]
        case .down: ["down", "ctrl+j", "j"]
        case .left: ["left", "ctrl+h", "h"]
        case .right: ["right", "ctrl+l", "l"]
        case .open: ["return"]
        case .back: ["escape"]
        case .mark: ["x"]
        case .nextNeedingYou: ["tab"]
        case .go: ["g"]
        case .remove: ["delete", "forwarddelete"]
        case .reply: ["r"]
        case .dismiss: ["d"]
        case .unread: ["u"]
        case .newJob: ["n"]
        case .editJob: ["e"]
        case .runJob: ["r"]
        case .pauseJob: ["p"]
        case .duplicateJob: ["cmd+d"]
        case .save: ["cmd+return", "cmd+s"]
        case .leave: ["escape"]
        default: ["cmd+\((KeyAction.sessions.firstIndex(of: self) ?? 0) + 1)"]
        }
    }

    /// Whether it takes several shortcuts. A menu item answers only one.
    var takesSeveral: Bool {
        switch section {
        case .lists, .inbox, .scheduled, .editing: true
        default: false
        }
    }

    /// Why a shortcut can't be used for it, if it can't.
    func problem(with combo: KeyCombo) -> String? {
        guard let name = combo.name else { return "That key can't be used for a shortcut." }
        let mods = combo.mods
        switch section {
        case .anyApp:
            // Every app would lose it: it needs more than one modifier, or a function key.
            let held = [KeyCombo.Mods.control, .option, .shift, .command].filter(mods.contains).count
            if held < 2 && !name.isFunctionKey { return "From any app, it needs two of ⌃ ⌥ ⇧ ⌘, or a function key." }
        case .app, .panes, .goTo:
            // Otherwise the terminal would never get it.
            if !mods.contains(.command) && !name.isFunctionKey { return "It needs ⌘, or a function key, so terminals keep the key." }
            if combo.menuKey == nil { return "A menu can't use that key." }
        case .editing:
            if combo.types || [UInt16(kVK_Return), UInt16(kVK_Tab), UInt16(kVK_Delete), UInt16(kVK_ForwardDelete)].contains(combo.key) && mods.isEmpty {
                return "You'd be typing it: it needs ⌘ or ⌃, or a key like esc."
            }
        case .lists, .inbox, .scheduled:
            if mods.isEmpty, KeyName.digitCodes.contains(combo.key) { return "1 to 9 already show a session or answer an option." }
        }
        if mods == .command, ["x", "c", "v", "a", "z"].contains(name.name) { return "\(combo.display) is for editing text." }
        return nil
    }
}

/// Keys that can't be changed here, listed with the rest.
struct FixedKeys: Identifiable {
    let section: String
    let keys: String
    let title: String
    var id: String { section + keys }

    static let all: [FixedKeys] = [
        FixedKeys(section: "Sidebar", keys: "1-9", title: "Show that session"),
        FixedKeys(section: "Inbox and popup", keys: "1-9", title: "Answer with that option"),
        FixedKeys(section: "Text", keys: "⌘X ⌘C ⌘V ⌘A", title: "Cut, copy, paste and select all"),
        FixedKeys(section: "Text", keys: "⌘B ⌘I ⌘E ⌘K", title: "In a job's prompt: bold, italic, code and link"),
        FixedKeys(section: "Text", keys: "⌥⌘1-3  ⇧⌘7-9", title: "In a job's prompt: headings and lists"),
    ] + Palette.screenCommands.map {
        FixedKeys(section: "Terminal (Ghostty's keys, set in its config)", keys: $0.shortcut, title: $0.title)
    }
}

/// The app's shortcuts: the defaults, and what you've changed. Every key handler
/// reads it, so a change applies everywhere straight away.
@Observable final class Keymap {
    static let shared = Keymap(
        saved: UserDefaults.standard.dictionary(forKey: Keymap.defaultsKey) as? [String: [String]] ?? [:],
        save: { UserDefaults.standard.set($0, forKey: Keymap.defaultsKey) })

    static let defaultsKey = "keyBindings"

    /// The actions you've changed, by their settings name.
    private(set) var changed: [KeyAction: [KeyCombo]] = [:]
    /// While the shortcuts list records a key, the shortcuts that work from any app are
    /// off, so they can be recorded too.
    var recording = false {
        didSet { if recording != oldValue { onChange() } }
    }
    /// Told after every change: the menus and the shortcuts from any app follow.
    @ObservationIgnored var onChange: () -> Void = {}
    @ObservationIgnored private let save: ([String: [String]]) -> Void

    init(saved: [String: [String]] = [:], save: @escaping ([String: [String]]) -> Void = { _ in }) {
        self.save = save
        for (name, texts) in saved {
            guard let action = KeyAction(rawValue: name) else { continue }
            changed[action] = texts.compactMap(KeyCombo.init)
        }
    }

    func combos(_ action: KeyAction) -> [KeyCombo] {
        changed[action] ?? action.defaults.compactMap(KeyCombo.init)
    }

    func isChanged(_ action: KeyAction) -> Bool { changed[action] != nil }

    func matches(_ action: KeyAction, _ combo: KeyCombo) -> Bool {
        combos(action).contains(combo)
    }

    /// The first of these actions the shortcut is for.
    func action(for combo: KeyCombo, among actions: [KeyAction]) -> KeyAction? {
        actions.first { matches($0, combo) }
    }

    func set(_ action: KeyAction, _ combos: [KeyCombo]) {
        var unique: [KeyCombo] = []
        for combo in combos where !unique.contains(combo) { unique.append(combo) }
        if unique == action.defaults.compactMap(KeyCombo.init) {
            changed[action] = nil
        } else {
            changed[action] = unique
        }
        persist()
    }

    /// Adds a shortcut, or for an action a menu answers, replaces it.
    func add(_ combo: KeyCombo, to action: KeyAction) {
        set(action, action.takesSeveral ? combos(action) + [combo] : [combo])
    }

    func remove(_ combo: KeyCombo, from action: KeyAction) {
        set(action, combos(action).filter { $0 != combo })
    }

    func reset(_ action: KeyAction) {
        changed[action] = nil
        persist()
    }

    func resetAll() {
        changed = [:]
        persist()
    }

    /// The other actions that answer the same shortcut at the same time.
    func clashes(_ combo: KeyCombo, for action: KeyAction) -> [KeyAction] {
        KeyAction.allCases.filter { other in
            other != action && other.section.overlaps(action.section) && matches(other, combo)
        }
    }

    /// Every action with a shortcut another uses at the same time.
    var clashing: [KeyAction] {
        KeyAction.allCases.filter { action in combos(action).contains { !clashes($0, for: action).isEmpty } }
    }

    // MARK: What the views show

    /// The first shortcut, as hints and tooltips show it.
    func label(_ action: KeyAction) -> String {
        combos(action).first?.display ?? ""
    }

    /// Every shortcut, as the shortcuts list shows it.
    func labels(_ action: KeyAction) -> String {
        combos(action).map(\.display).joined(separator: " ")
    }

    /// A pair like up and down: "↑↓ or ⌃K ⌃J or k j".
    func pair(_ a: KeyAction, _ b: KeyAction, limit: Int = .max) -> String {
        let arrows: Set<String> = ["↑", "↓", "←", "→"]
        return zip(combos(a), combos(b)).prefix(limit).map { first, second in
            arrows.contains(first.display) && arrows.contains(second.display)
                ? first.display + second.display : first.display + " " + second.display
        }.joined(separator: " or ")
    }

    /// "Inbox (⇧⌘I)", or just "Inbox" when it has no shortcut.
    func tip(_ text: String, _ action: KeyAction) -> String {
        let key = label(action)
        return key.isEmpty ? text : "\(text) (\(key))"
    }

    /// A hint line: "↩ open   x mark", leaving out what has no shortcut.
    static func hint(_ parts: [(key: String, text: String)]) -> String {
        parts.filter { !$0.key.isEmpty }.map { "\($0.key) \($0.text)" }.joined(separator: "   ")
    }

    private func persist() {
        save(Dictionary(uniqueKeysWithValues: changed.map { ($0.key.rawValue, $0.value.map(\.text)) }))
        onChange()
    }
}

/// The hint lines along the bottom of each view, from the keymap.
extension Keymap {
    private var move: String { pair(.up, .down, limit: 2) }

    var inboxHint: String {
        Self.hint([
            (move, "move"), (label(.open), "open or answer"), (label(.mark), "mark"), (label(.nextNeedingYou), "next unread"),
            (label(.focusSidebar), "sessions"), ("1-9", "answer"), (label(.reply), "reply"), (label(.dismiss), "dismiss"),
            (label(.unread), "unread"), (label(.go), "go"), (label(.readAloud), "read aloud"), (label(.back), "back"),
            (label(.shortcuts), "all keys"),
        ])
    }

    var popupHint: String {
        Self.hint([
            (move, "move"), (label(.open), "reply or choose"), ("1-9", "answer"), (label(.reply), "reply"),
            (label(.nextNeedingYou), "next unread"), (label(.mark), "mark"), (label(.dismiss), "dismiss"),
            (label(.unread), "unread"), (label(.go), "go to session"), (label(.readAloud), "read aloud"), (label(.back), "close"),
        ])
    }

    var choiceHint: String {
        Self.hint([(pair(.left, .right, limit: 2), "move"), (label(.open), "choose"), (label(.back), "leave")])
    }

    var replyHint: String {
        Self.hint([(label(.save), "send"), (label(.leave), "leave")])
    }

    var scheduledHint: String {
        Self.hint([
            (move, "move"), (label(.newJob), "new"), (label(.open), "edit"), (label(.runJob), "run now"),
            (label(.pauseJob), "pause"), (label(.duplicateJob), "duplicate"), (label(.go), "go to last run"),
            (label(.remove), "delete"), (label(.back), "back"), (label(.shortcuts), "all keys"),
        ])
    }

    var jobFormHint: String {
        Self.hint([
            (label(.save), "save"), (label(.leave), "cancel"), ("⌘B ⌘I ⌘E ⌘K", "format"), ("⌥⌘1-3", "headings"),
            ("⇧⌘7 ⇧⌘8 ⇧⌘9", "lists"),
        ])
    }
}
