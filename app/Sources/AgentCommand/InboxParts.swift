import AppKit
import SwiftUI

/// How to show an item's type and what happened to it.
extension InboxItem {
    var kindIcon: String {
        switch kind {
        case "question": "questionmark.circle"
        case "permission": "hand.raised"
        case "error": "exclamationmark.triangle"
        default: "checkmark.circle"
        }
    }

    var stateIcon: String? {
        switch state {
        case "responded": "arrowshape.turn.up.left"
        case "dismissed": "archivebox"
        case "stale": "clock.arrow.circlepath"
        default: nil
        }
    }
}

/// A divider you can drag to resize what's beside it.
struct InboxDivider: View {
    let vertical: Bool
    @Binding var value: Double
    /// Dragging toward the start makes it bigger: what it resizes comes after it.
    var inverted = false
    let range: ClosedRange<Double>

    @State private var start: Double?

    private var theme: AppTheme { .shared }

    var body: some View {
        Rectangle()
            .fill(Color(nsColor: theme.line))
            .frame(width: vertical ? 1 : nil, height: vertical ? nil : 1)
            .overlay {
                // A wider area to grab than the line you see.
                Color.clear
                    .frame(width: vertical ? 9 : nil, height: vertical ? nil : 9)
                    .contentShape(Rectangle())
                    .onHover { inside in
                        if inside {
                            (vertical ? NSCursor.resizeLeftRight : NSCursor.resizeUpDown).push()
                        } else {
                            NSCursor.pop()
                        }
                    }
                    .gesture(
                        DragGesture(coordinateSpace: .global)
                            .onChanged { drag in
                                let from = start ?? value
                                start = from
                                let moved = Double(vertical ? drag.translation.width : drag.translation.height)
                                value = min(range.upperBound, max(range.lowerBound, from + (inverted ? -moved : moved)))
                            }
                            .onEnded { _ in start = nil })
            }
            .zIndex(1)
    }
}

/// Says that a new message has come in.
struct InboxNotice: View {
    let model: InboxModel

    private var theme: AppTheme { .shared }

    var body: some View {
        if let notice = model.notice {
            Label(notice, systemImage: "bell.badge")
                .font(AppFont.ui(11))
                .foregroundStyle(theme.yellow)
                .lineLimit(1)
                .padding(.horizontal, 16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .frame(height: 22)
        }
    }
}

/// Reads a message aloud, or stops.
struct SpeakButton: View {
    let item: InboxItem

    private var theme: AppTheme { .shared }

    var body: some View {
        let speech = SpeechController.shared
        let speaking = speech.speakingItem == item.id
        if speech.isEnabled {
            Button {
                speech.toggle(item)
            } label: {
                Image(systemName: speaking ? "stop.fill" : "speaker.wave.2").foregroundStyle(theme.fg)
            }
            .buttonStyle(.plain)
            .help(Keymap.shared.tip(speaking ? "Stop reading" : "Read aloud", .readAloud))
        }
    }
}

/// How to answer an item: its options, or a reply box.
struct InboxAnswerView: View {
    @Bindable var model: InboxModel
    let item: InboxItem

    @FocusState private var boxFocused: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        let blocked = model.answerable(item)
        VStack(alignment: .leading, spacing: 8) {
            if let error = model.error ?? SpeechController.shared.failure {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(theme.red)
            }
            if item.isOpen && !item.multipart {
                if item.needsOption {
                    options(enabled: blocked == nil)
                    Spacer(minLength: 0)
                } else {
                    replyBox(enabled: blocked == nil)
                }
            } else {
                Spacer(minLength: 0)
            }
            if let blocked {
                Text(blocked).font(AppFont.ui(11)).foregroundStyle(theme.dim).lineLimit(1)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private func options(enabled: Bool) -> some View {
        let highlighted = model.highlightedOption(of: item)
        return VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 12) {
                ForEach(Array(item.options.enumerated()), id: \.offset) { index, option in
                    Button("\(index + 1). \(option)") { model.answer(item, option: index + 1) }
                        .buttonStyle(.plain)
                        .padding(.horizontal, 10)
                        .frame(height: 24)
                        .overlay(
                            Rectangle().strokeBorder(
                                index == highlighted ? theme.yellow : Color(nsColor: theme.line), lineWidth: 1))
                        .foregroundStyle(enabled ? theme.fg : theme.dim)
                        .disabled(!enabled || model.sending)
                }
            }
            if highlighted != nil {
                Text(Keymap.shared.choiceHint).font(AppFont.ui(11)).foregroundStyle(theme.dim)
            }
        }
    }

    private func replyBox(enabled: Bool) -> some View {
        let draft = Binding(
            get: { model.drafts[item.sessionID] ?? "" },
            set: { model.drafts[item.sessionID] = $0 })
        return VStack(alignment: .trailing, spacing: 4) {
            TextEditor(text: draft)
                .font(AppFont.ui(13))
                .scrollContentBackground(.hidden)
                .disabled(!enabled)
                .focused($boxFocused)
                .overlay(
                    Rectangle().strokeBorder(
                        boxFocused ? theme.yellow : Color(nsColor: theme.line), lineWidth: 1))
                .onChange(of: model.typing) { _, typing in boxFocused = typing }
                .onChange(of: boxFocused) { _, focused in model.typing = focused }
                .onAppear { boxFocused = model.typing }
            Text(Keymap.shared.replyHint).font(AppFont.ui(11)).foregroundStyle(theme.dim)
        }
    }
}
