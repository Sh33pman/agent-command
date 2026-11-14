import AppKit
import Carbon.HIToolbox
import SwiftUI

/// The keyboard shortcuts list, where they can also be changed. It has the keyboard
/// while it's open: while it records, the next key pressed is the new shortcut.
@Observable final class ShortcutsModel {
    let keys: Keymap
    /// The action whose next key is being recorded.
    private(set) var recording: KeyAction?
    /// Why the last key pressed couldn't be used, and for what.
    private(set) var problem: (action: KeyAction, text: String)?
    @ObservationIgnored var close: () -> Void = {}
    @ObservationIgnored private var monitor: Any?

    init(keys: Keymap = .shared) {
        self.keys = keys
    }

    func start() {
        monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard let self else { return event }
            return self.handle(KeyCombo(event: event)) ? nil : event
        }
    }

    func stop() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        stopRecording()
    }

    func record(_ action: KeyAction) {
        recording = action
        problem = nil
        keys.recording = true
    }

    func stopRecording() {
        recording = nil
        keys.recording = false
    }

    /// Takes a key: the new shortcut while recording, otherwise closing. Reports whether
    /// it was used.
    func handle(_ combo: KeyCombo) -> Bool {
        if let action = recording {
            if combo == KeyCombo(key: UInt16(kVK_Escape)) {
                stopRecording()
                return true
            }
            if let text = action.problem(with: combo) {
                problem = (action, text)
                return true
            }
            keys.add(combo, to: action)
            problem = nil
            stopRecording()
            return true
        }
        if keys.action(for: combo, among: [.back, .closePane, .shortcuts]) != nil {
            close()
            return true
        }
        // Nothing behind the list to split, move to or focus.
        return keys.action(for: combo, among: KeyAction.paneActions + [.focusSidebar]) != nil
    }
}

struct ShortcutsView: View {
    @Bindable var model: ShortcutsModel

    private var theme: AppTheme { .shared }
    private var keys: Keymap { model.keys }

    var body: some View {
        VStack(spacing: 0) {
            header
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(KeySection.allCases, id: \.self) { section in
                        sectionTitle(section.title)
                        ForEach(KeyAction.allCases.filter { $0.section == section }) { action in
                            row(action)
                        }
                    }
                    fixed
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 24)
                .frame(maxWidth: 860, alignment: .leading)
                .frame(maxWidth: .infinity)
            }
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
    }

    private var header: some View {
        HStack(spacing: 12) {
            Text("Keyboard Shortcuts").fontWeight(.bold).foregroundStyle(theme.yellow)
            Text(Keymap.hint([
                ("+", "records a key"), ("esc", "cancels it"), (keys.label(.shortcuts), "or \(keys.label(.back)) closes"),
            ]))
            .font(AppFont.ui(11))
            .foregroundStyle(theme.dim)
            .lineLimit(1)
            Spacer()
            Button("Reset All") {
                model.stopRecording()
                keys.resetAll()
            }
            .buttonStyle(.borderless)
            .disabled(KeyAction.allCases.allSatisfy { !keys.isChanged($0) })
            .help("Put every shortcut back as it came")
            Button {
                model.close()
            } label: {
                Image(systemName: "xmark")
            }
            .buttonStyle(.plain)
            .help(keys.tip("Close", .shortcuts))
        }
        .padding(.horizontal, 16)
        .frame(height: 34)
    }

    private func sectionTitle(_ title: String) -> some View {
        Text(title)
            .fontWeight(.bold)
            .foregroundStyle(theme.magenta)
            .padding(.top, 18)
            .padding(.bottom, 4)
    }

    private func row(_ action: KeyAction) -> some View {
        let combos = keys.combos(action)
        let clashes = combos.flatMap { keys.clashes($0, for: action) }
        return HStack(alignment: .firstTextBaseline, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(action.title)
                if !action.note.isEmpty {
                    Text(action.note).font(AppFont.ui(11)).foregroundStyle(theme.dim)
                }
                if !clashes.isEmpty {
                    Text("Also used by: " + clashes.map(\.title).joined(separator: ", "))
                        .font(AppFont.ui(11)).foregroundStyle(theme.red)
                }
                if let problem = model.problem, problem.action == action {
                    Text(problem.text).font(AppFont.ui(11)).foregroundStyle(theme.red)
                }
            }
            Spacer(minLength: 16)
            if combos.isEmpty && model.recording != action {
                Text("none").foregroundStyle(theme.dim)
            }
            ForEach(combos, id: \.self) { combo in
                chip(combo, of: action)
            }
            if model.recording == action {
                Text("Press a key…")
                    .padding(.horizontal, 6)
                    .padding(.vertical, 1)
                    .overlay(Rectangle().strokeBorder(theme.yellow, lineWidth: 1))
                    .foregroundStyle(theme.yellow)
            }
            Button {
                if model.recording == action { model.stopRecording() } else { model.record(action) }
            } label: {
                Image(systemName: model.recording == action ? "xmark.circle" : "plus.circle")
            }
            .buttonStyle(.plain)
            .help(model.recording == action ? "Stop recording" : action.takesSeveral ? "Add a shortcut" : "Change the shortcut")
            Button {
                keys.reset(action)
            } label: {
                Image(systemName: "arrow.uturn.backward")
            }
            .buttonStyle(.plain)
            .opacity(keys.isChanged(action) ? 1 : 0)
            .disabled(!keys.isChanged(action))
            .help("Put back " + action.defaults.compactMap(KeyCombo.init).map(\.display).joined(separator: " "))
        }
        .padding(.vertical, 5)
        .overlay(alignment: .bottom) {
            Rectangle().fill(Color(nsColor: theme.line).opacity(0.5)).frame(height: 1)
        }
    }

    private func chip(_ combo: KeyCombo, of action: KeyAction) -> some View {
        HStack(spacing: 4) {
            Text(combo.display)
            Button {
                keys.remove(combo, from: action)
            } label: {
                Image(systemName: "xmark").font(.system(size: 8, weight: .bold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(theme.dim)
            .help("Remove \(combo.display)")
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 1)
        .overlay(Rectangle().strokeBorder(Color(nsColor: theme.line), lineWidth: 1))
    }

    private var fixed: some View {
        VStack(alignment: .leading, spacing: 0) {
            sectionTitle("Can't be changed here")
            ForEach(FixedKeys.all) { entry in
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(entry.title)
                        Text(entry.section).font(AppFont.ui(11)).foregroundStyle(theme.dim)
                    }
                    Spacer(minLength: 16)
                    Text(entry.keys).foregroundStyle(theme.dim)
                }
                .padding(.vertical, 5)
            }
        }
    }
}
