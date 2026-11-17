import SwiftUI

/// Picks an earlier Claude or Antigravity conversation to continue, or takes its id.
struct ResumeView: View {
    let store: SessionStore
    let close: () -> Void
    let choose: (ResumeTarget) -> Void

    @State private var choice = ResumeChoice()
    @State private var loading = true
    @State private var problem: String?

    private var theme: AppTheme { .shared }

    var body: some View {
        let list = choice.rows
        PopupBox(
            title: "Resume a session",
            placeholder: "Search, or paste a session id",
            search: Binding(get: { choice.search }, set: { choice.setSearch($0) }),
            close: close,
            onMove: { choice.move($0) },
            onSubmit: {
                if let row = choice.chosen { choose(row.target) }
            }
        ) {
            rows(list)
        }
        .onAppear {
            store.loadConversations { found, error in
                choice.conversations = found
                problem = error
                loading = false
            }
        }
    }

    private func rows(_ list: [ResumeChoice.Row]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if let message = message(list) {
                        Text(message)
                            .foregroundStyle(problem == nil ? theme.dim : theme.red)
                            .padding(.horizontal, 24)
                    }
                    ForEach(list) { row in
                        let isSelected = row == choice.chosen
                        Group {
                            switch row {
                            case .conversation(let conversation):
                                ConversationRow(conversation: conversation, isSelected: isSelected)
                            case .typed(_, let harness):
                                TypedIDRow(harness: harness, isSelected: isSelected)
                            }
                        }
                        .id(row.id)
                        .contentShape(Rectangle())
                        .onTapGesture { choose(row.target) }
                    }
                }
            }
            .frame(maxHeight: 520)
            .fixedSize(horizontal: false, vertical: true)
            .onChange(of: choice.chosen) {
                if let row = choice.chosen { proxy.scrollTo(row.id) }
            }
        }
    }

    private func message(_ list: [ResumeChoice.Row]) -> String? {
        if let problem { return problem }
        if loading { return "Loading…" }
        if choice.typedID != nil { return "Whose session is this id? Press return to look it up" }
        return list.isEmpty ? "No matching sessions" : nil
    }
}

/// The agent's mark, in the row's colors.
private struct HarnessMark: View {
    let harness: Harness
    let isSelected: Bool
    private var theme: AppTheme { .shared }

    var body: some View {
        let color = isSelected ? theme.bg : theme.fg
        if harness == .antigravity {
            AgentMark(mark: AntigravityMarkShape(), label: harness.title, color: color, busy: false)
        } else {
            AgentMark(mark: ClaudeMarkShape(), label: harness.title, color: color, busy: false)
        }
    }
}

/// Looks the typed id up as one agent's.
private struct TypedIDRow: View {
    let harness: Harness
    let isSelected: Bool
    private var theme: AppTheme { .shared }

    var body: some View {
        HStack(spacing: 8) {
            HarnessMark(harness: harness, isSelected: isSelected)
            Text("As \(harness == .antigravity ? "an" : "a") \(harness.title) session")
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
            Spacer(minLength: 12)
        }
        .padding(.horizontal, 8)
        .frame(height: 19)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }
}

private struct ConversationRow: View {
    let conversation: Conversation
    let isSelected: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                HarnessMark(harness: conversation.harnessKind, isSelected: isSelected)
                    .help(conversation.harnessKind.title)
                Text(conversation.displayTitle)
                    .fontWeight(isSelected ? .bold : .regular)
                    .foregroundStyle(isSelected ? theme.bg : theme.fg)
                    .lineLimit(1)
                Spacer(minLength: 12)
                if conversation.isRunning {
                    Text("running").foregroundStyle(isSelected ? theme.bg : theme.yellow)
                } else if conversation.session_id != nil {
                    Text("in sidebar").foregroundStyle(isSelected ? theme.bg : theme.dim)
                }
                if let updated = conversation.updated {
                    Text(updated, format: .relative(presentation: .named))
                        .foregroundStyle(isSelected ? theme.bg : theme.dim)
                        .lineLimit(1)
                }
            }
            Text(detail)
                .foregroundStyle(isSelected ? theme.bg : theme.dim)
                .lineLimit(1)
                .truncationMode(.middle)
                // Under the title, past the mark.
                .padding(.leading, 21)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }

    private var detail: String {
        let folder = (conversation.cwd as NSString).abbreviatingWithTildeInPath
        return conversation.branch.map { "\(folder) · \($0)" } ?? folder
    }
}
