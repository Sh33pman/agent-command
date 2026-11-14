import Foundation

/// What a command palette entry does. The app carries it out; the list itself is
/// worked out here, from what's showing, so it can be checked without a window.
enum PaletteAction: Equatable {
    case rename([Session])
    case archive([Session])
    case moveOut(sessionID: String)
    case copy(String)
    case select(sessionID: String)
    case nextAttention
    case newSession(harness: String)
    case resume
    case split(SplitDirection)
    case closePane
    case focus(FocusDirection)
    case equalizePanes
    /// A Ghostty binding run on the pane that's showing.
    case screen(binding: String)
    case inbox
    case inboxFor(Session)
    case scheduled
    case toggleSpeech
    case toggleReading
    case toggleRemoteControl
    case togglePopupReading
    case togglePopup
    case popupStyle(ReplyPopupStyle)
    case popupOpens(PopupOpens)
    case themes
    case focusSidebar
    case toggleSidebar
    case report(ReportKind)
    case shortcuts
    case quit
}

struct PaletteEntry: Equatable {
    let section: String
    let title: String
    var shortcut = ""
    /// Tells apart entries whose section and title are the same.
    var key = ""
    let action: PaletteAction
}

/// What the palette's list depends on.
struct PaletteState {
    /// The sessions the commands act on: the selection, or the session showing.
    var targets: [Session] = []
    var sessions: [Session] = []
    /// Whether a session needs you, for the attention command.
    var needsYou = false
    var setMembers: [[String]] = []
    var paneCount = 0
    /// Whether a terminal is showing, for the Screen commands.
    var showsScreen = false
    var speechOn = false
    var speaking = false
    var remoteControl = false
    var popupReads = false
    var popupOn = true
    var popupStyle = ReplyPopupStyle.window
    var popupOpens = PopupOpens.needsYou
    /// Where the shortcuts shown come from.
    var keys = Keymap.shared
}

enum Palette {
    static let screenCommands: [(title: String, shortcut: String, binding: String)] = [
        ("Clear screen", "⌘K", "clear_screen"),
        ("Scroll to top", "⌘↖", "scroll_to_top"),
        ("Scroll to bottom", "⌘↘", "scroll_to_bottom"),
        ("Select all", "⌘A", "select_all"),
        ("Paste", "⌘V", "paste_from_clipboard"),
        ("Increase font size", "⌘=", "increase_font_size:1"),
        ("Decrease font size", "⌘-", "decrease_font_size:1"),
        ("Reset font size", "⌘0", "reset_font_size"),
    ]

    /// What can be done right now: the list depends on what's showing.
    static func entries(_ state: PaletteState) -> [PaletteEntry] {
        let keys = state.keys
        var list: [PaletteEntry] = []
        let targets = state.targets
        let current = targets.count == 1 ? targets[0] : nil
        if targets.count > 1 {
            let section = "\(targets.count) sessions"
            list.append(PaletteEntry(section: section, title: "Rename…", shortcut: keys.label(.rename), action: .rename(targets)))
            list.append(PaletteEntry(section: section, title: "Archive…", shortcut: keys.label(.archive), action: .archive(targets)))
        }
        if let session = current {
            let section = session.isTerminal ? "Terminal session" : "Claude session"
            list.append(PaletteEntry(section: section, title: "Rename…", shortcut: keys.label(.rename), action: .rename([session])))
            list.append(PaletteEntry(section: section, title: "Archive…", shortcut: keys.label(.archive), action: .archive([session])))
            if state.setMembers.contains(where: { $0.contains(session.id) }) {
                list.append(PaletteEntry(section: section, title: "Move out of set", action: .moveOut(sessionID: session.id)))
            }
            list.append(PaletteEntry(section: section, title: "Copy folder path", action: .copy(session.cwd)))
            if !session.isTerminal {
                list.append(PaletteEntry(section: section, title: "Copy Claude session ID", action: .copy(session.claudeSessionID)))
            }
        }
        if state.needsYou {
            list.append(PaletteEntry(section: "Go to", title: "Next needing attention", shortcut: keys.label(.nextAttention), action: .nextAttention))
        }
        for session in state.sessions where session.id != current?.id {
            list.append(PaletteEntry(
                section: "Go to", title: session.displayName, shortcut: session.status, key: session.id,
                action: .select(sessionID: session.id)))
        }
        list.append(PaletteEntry(section: "New", title: "Claude session", shortcut: keys.label(.newClaude), action: .newSession(harness: "claude")))
        list.append(PaletteEntry(section: "New", title: "Terminal", shortcut: keys.label(.newTerminal), action: .newSession(harness: "terminal")))
        list.append(PaletteEntry(section: "New", title: "Resume a session…", shortcut: keys.label(.resume), action: .resume))
        list.append(PaletteEntry(section: "Panes", title: "Split right", shortcut: keys.label(.splitRight), action: .split(.right)))
        list.append(PaletteEntry(section: "Panes", title: "Split down", shortcut: keys.label(.splitDown), action: .split(.down)))
        list.append(PaletteEntry(section: "Panes", title: "Close pane", shortcut: keys.label(.closePane), action: .closePane))
        if state.paneCount > 1 {
            for (title, shortcut, direction) in [
                ("Focus left", keys.label(.focusLeft), FocusDirection.left), ("Focus down", keys.label(.focusDown), .down),
                ("Focus up", keys.label(.focusUp), .up), ("Focus right", keys.label(.focusRight), .right),
            ] {
                list.append(PaletteEntry(section: "Panes", title: title, shortcut: shortcut, action: .focus(direction)))
            }
            list.append(PaletteEntry(section: "Panes", title: "Equalize panes", shortcut: keys.label(.equalize), action: .equalizePanes))
        }
        if state.showsScreen {
            for command in screenCommands {
                list.append(PaletteEntry(
                    section: "Screen", title: command.title, shortcut: command.shortcut, action: .screen(binding: command.binding)))
            }
        }
        list.append(PaletteEntry(section: "App", title: "Inbox", shortcut: keys.label(.inbox), action: .inbox))
        list.append(PaletteEntry(section: "App", title: "Scheduled jobs", shortcut: keys.label(.scheduled), action: .scheduled))
        list.append(PaletteEntry(section: "App", title: state.speechOn ? "Turn speech off" : "Turn speech on", action: .toggleSpeech))
        if state.speechOn, current?.isAgent == true || state.speaking {
            let title = state.speaking ? "Stop reading" : "Read the latest message aloud"
            list.append(PaletteEntry(section: "App", title: title, shortcut: keys.label(.readAloud), action: .toggleReading))
        }
        list.append(PaletteEntry(
            section: "Claude", title: RemoteControlSetting.paletteTitle(isOn: state.remoteControl), action: .toggleRemoteControl))
        list.append(PaletteEntry(
            section: "App", title: state.popupReads ? "Stop reading aloud in the popup" : "Read aloud in the popup",
            action: .togglePopupReading))
        list.append(PaletteEntry(
            section: "App", title: state.popupOn ? "Turn popup off" : "Turn popup on", shortcut: keys.label(.togglePopup), action: .togglePopup))
        for style in ReplyPopupStyle.allCases where style != state.popupStyle {
            list.append(PaletteEntry(section: "App", title: "Popup style: \(style.label)", action: .popupStyle(style)))
        }
        for opens in PopupOpens.allCases where opens != state.popupOpens {
            list.append(PaletteEntry(section: "App", title: "Popup opens for: \(opens.label)", action: .popupOpens(opens)))
        }
        if let current {
            list.append(PaletteEntry(section: "App", title: "Inbox for \(current.displayName)", action: .inboxFor(current)))
        }
        list.append(PaletteEntry(section: "App", title: "Change theme…", shortcut: keys.label(.themes), action: .themes))
        list.append(PaletteEntry(section: "App", title: "Keyboard shortcuts…", shortcut: keys.label(.shortcuts), action: .shortcuts))
        list.append(PaletteEntry(section: "App", title: "Focus sidebar", shortcut: keys.label(.focusSidebar), action: .focusSidebar))
        list.append(PaletteEntry(section: "App", title: "Toggle sidebar", action: .toggleSidebar))
        list.append(PaletteEntry(section: "App", title: "Report a bug…", action: .report(.bug)))
        list.append(PaletteEntry(section: "App", title: "Request a feature…", action: .report(.feature)))
        list.append(PaletteEntry(section: "App", title: "Quit", shortcut: keys.label(.quit), action: .quit))
        return list
    }

    /// The question and note of the alert that confirms archiving these sessions.
    static func archivePrompt(_ sessions: [Session]) -> (title: String, note: String)? {
        guard let first = sessions.first else { return nil }
        let title = sessions.count == 1 ? "Archive \(first.displayName)?" : "Archive \(sessions.count) sessions?"
        var notes: [String] = []
        if sessions.contains(where: \.isTerminal) {
            notes.append("Terminals end, along with everything running in them, and can't be brought back.")
        }
        if sessions.contains(where: { !$0.isTerminal }) {
            notes.append("Agents stop. You can unarchive them later and continue the conversation.")
        }
        return (title, notes.joined(separator: " "))
    }
}
