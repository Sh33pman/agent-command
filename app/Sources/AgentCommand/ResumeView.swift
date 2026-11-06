import SwiftUI

/// Picks an earlier Claude session to continue, or takes its id.
struct ResumeView: View {
    let store: SessionStore
    let close: () -> Void
    let choose: (_ id: String, _ isRunning: Bool) -> Void

    @State private var conversations: [Conversation] = []
    @State private var search = ""
    @State private var selected = 0
    @State private var loading = true
    @State private var problem: String?

    private var theme: AppTheme { .shared }

    private var matches: [Conversation] { conversations.filter { $0.matches(search) } }

    /// What search holds, if it could be an id with nothing listed for it.
    private var typedID: String? {
        let text = search.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let isHex = !text.isEmpty && text.allSatisfy { $0.isHexDigit || $0 == "-" }
        return matches.isEmpty && text.count >= 8 && isHex ? text : nil
    }

    var body: some View {
        let list = matches
        PopupBox(
            title: "Resume a session",
            placeholder: "Search, or paste a session id",
            search: $search,
            close: close,
            onMove: { delta in
                if !list.isEmpty { selected = (selected + delta + list.count) % list.count }
            },
            onSubmit: { submit(list) }
        ) {
            rows(list)
        }
        .onChange(of: search) { selected = 0 }
        .onAppear {
            store.loadConversations { found, error in
                conversations = found
                problem = error
                loading = false
            }
        }
    }

    private func rows(_ list: [Conversation]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    if let message = message(list) {
                        Text(message)
                            .foregroundStyle(problem == nil ? theme.dim : theme.red)
                            .padding(.horizontal, 24)
                    }
                    ForEach(list) { conversation in
                        let index = list.firstIndex { $0.id == conversation.id } ?? 0
                        ConversationRow(conversation: conversation, isSelected: index == selected)
                            .id(conversation.id)
                            .contentShape(Rectangle())
                            .onTapGesture { choose(conversation.id, conversation.isRunning) }
                    }
                }
            }
            .frame(maxHeight: 520)
            .fixedSize(horizontal: false, vertical: true)
            .onChange(of: selected) {
                if list.indices.contains(selected) { proxy.scrollTo(list[selected].id) }
            }
        }
    }

    private func message(_ list: [Conversation]) -> String? {
        if let problem { return problem }
        if loading { return "Loading…" }
        guard list.isEmpty else { return nil }
        return typedID == nil ? "No matching sessions" : "Press return to look up this id"
    }

    private func submit(_ list: [Conversation]) {
        if list.indices.contains(selected) {
            choose(list[selected].id, list[selected].isRunning)
        } else if let id = typedID {
            choose(id, false)
        }
    }
}

private struct ConversationRow: View {
    let conversation: Conversation
    let isSelected: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
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
