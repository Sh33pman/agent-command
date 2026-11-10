import AppKit
import SwiftUI

/// A form for a bug report or feature request. It shows what will be included, then
/// opens GitHub's page filled in, for the person to submit: the app posts nothing.
/// `acmd report` does the cleaning.
struct ReportView: View {
    let kind: ReportKind
    let close: () -> Void

    private enum Field { case title, description }
    private enum Outcome {
        case opened(cut: Bool)
        case failed(String)
    }

    @State private var draft: ReportDraft
    @State private var opening = false
    @State private var outcome: Outcome?
    @FocusState private var focus: Field?

    init(kind: ReportKind, close: @escaping () -> Void) {
        self.kind = kind
        self.close = close
        _draft = State(initialValue: ReportDraft(kind: kind))
    }

    private var theme: AppTheme { .shared }

    var body: some View {
        ZStack {
            theme.backdrop.ignoresSafeArea().onTapGesture(perform: close)
            VStack(alignment: .leading, spacing: 10) {
                Text(kind.title).fontWeight(.bold).foregroundStyle(theme.yellow)
                form
            }
            .padding(20)
            .frame(width: 640)
            .background(theme.bg)
            .overlay { Rectangle().strokeBorder(Color(nsColor: theme.line), lineWidth: 1) }
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .onExitCommand(perform: close)
        .onAppear {
            focus = .title
            loadDetails()
        }
    }

    private var form: some View {
        VStack(alignment: .leading, spacing: 10) {
            TextField("Title", text: $draft.title)
                .textFieldStyle(.plain)
                .focused($focus, equals: .title)
                .padding(6)
                .overlay { outline(focus == .title) }
            Text(kind.question).foregroundStyle(theme.dim).font(AppFont.ui(11))
            TextEditor(text: $draft.description)
                .scrollContentBackground(.hidden)
                .focused($focus, equals: .description)
                .frame(height: 110)
                .overlay { outline(focus == .description) }
            if kind == .bug {
                Toggle("Include the last 200 lines of the daemon log", isOn: $draft.includesLog)
                    .font(AppFont.ui(11))
            }
            Text(kind == .bug
                ? "Also included, and public once you submit it. Secrets, your account name and your home folder are taken out, but check it."
                : "Also included, and public once you submit it:")
                .foregroundStyle(theme.dim)
                .font(AppFont.ui(11))
            ScrollView {
                Text(draft.shownDetails)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.dim)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(6)
            }
            .frame(height: kind == .bug && draft.includesLog ? 170 : 44)
            .overlay { outline(false) }
            switch outcome {
            case .opened(let cut)?:
                Label(
                    "GitHub opened in your browser. Check it and press Submit there."
                        + (cut ? " It was too long for the page, so paste the full report from the clipboard." : ""),
                    systemImage: "info.circle"
                )
                .font(AppFont.ui(11))
                .foregroundStyle(theme.yellow)
                .fixedSize(horizontal: false, vertical: true)
            case .failed(let message)?:
                Label(message, systemImage: "exclamationmark.triangle.fill")
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.red)
                    .fixedSize(horizontal: false, vertical: true)
            default:
                EmptyView()
            }
            HStack {
                Spacer()
                Button("Cancel", action: close).keyboardShortcut(.cancelAction)
                Button("Continue on GitHub", action: openPage)
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(!draft.canSend(opening: opening))
            }
        }
    }

    private func outline(_ focused: Bool) -> some View {
        Rectangle().strokeBorder(focused ? theme.yellow : Color(nsColor: theme.line), lineWidth: 1)
    }

    private func loadDetails() {
        let arguments = draft.detailsArguments
        DispatchQueue.global(qos: .userInitiated).async {
            let result = Self.run(arguments, input: nil)
            DispatchQueue.main.async {
                if result.status == 0 {
                    draft.details = String(decoding: result.output, as: UTF8.self)
                } else {
                    outcome = .failed("Couldn't read this install's details.")
                }
            }
        }
    }

    private func openPage() {
        let body = draft.body
        let arguments = draft.pageArguments
        opening = true
        outcome = nil
        DispatchQueue.global(qos: .userInitiated).async {
            let result = Self.run(arguments, input: Data(body.utf8))
            let page = ReportPage.from(status: result.status, output: result.output)
            DispatchQueue.main.async {
                opening = false
                finish(page)
            }
        }
    }

    private func finish(_ page: ReportPage?) {
        guard let page, let url = page.url else {
            outcome = .failed("Couldn't prepare the report.")
            return
        }
        if page.wasCut {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(page.body, forType: .string)
        }
        NSWorkspace.shared.open(url)
        outcome = .opened(cut: page.wasCut)
    }

    /// Runs acmd, which can take a while. Call it off the main thread.
    private static func run(_ arguments: [String], input: Data?) -> (status: Int32, output: Data) {
        ReportProcess.run("/usr/bin/env", [DaemonPaths.acmd] + arguments, input: input)
    }
}
