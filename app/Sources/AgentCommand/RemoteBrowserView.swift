import SwiftUI

/// The full-window Remote Hosts browser: your known hosts on the left; the
/// chosen one's state and Remote sessions on the right. What it shows and does
/// is in RemoteBrowserModel.
struct RemoteBrowserView: View {
    let model: RemoteBrowserModel
    /// Opens Settings at Remote, to add a host.
    let settings: () -> Void

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 0) {
                hostList.frame(width: 280)
                Rectangle().fill(Color(nsColor: theme.line)).frame(width: 1)
                detail.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            Text(RemoteBrowserModel.hint)
                .font(AppFont.ui(11))
                .foregroundStyle(theme.dim)
                .lineLimit(1)
                .padding(.horizontal, 16)
                .frame(maxWidth: .infinity, alignment: .leading)
                .frame(height: 22)
        }
        .font(AppFont.ui(13))
        .foregroundStyle(theme.fg)
        .background(theme.bg)
        // A host that connects lists its sessions; while open, they stay current.
        .onChange(of: model.host?.stateName) { model.load(quiet: true) }
        .task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                model.load(quiet: true)
            }
        }
    }

    private var hostList: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Remote Hosts").fontWeight(.bold).foregroundStyle(theme.yellow)
                Spacer()
                Button("Add…", action: settings)
                    .buttonStyle(.borderless)
                    .help("Add a host in Settings → Remote")
                Button("Close", action: model.close).buttonStyle(.borderless)
            }
            .padding(.horizontal, 12)
            .frame(height: 34)
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            if model.hosts.isEmpty {
                Text("No hosts yet. Add one in Settings → Remote: its name, address and fingerprint.")
                    .foregroundStyle(theme.dim)
                    .padding(12)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(model.hosts) { host in
                        hostRow(host, isSelected: host.name == model.host?.name)
                            .contentShape(Rectangle())
                            .onTapGesture { model.choose(host: host.name) }
                    }
                }
            }
        }
    }

    private func hostRow(_ host: RemoteHost, isSelected: Bool) -> some View {
        let focused = isSelected && model.panel == .hosts
        return VStack(alignment: .leading, spacing: 1) {
            HStack {
                Text(host.name).fontWeight(isSelected ? .bold : .regular).lineLimit(1)
                Spacer()
                Text(host.stateTitle)
                    .font(AppFont.ui(11))
                    .foregroundStyle(focused ? theme.bg : (host.isConnected ? theme.green : host.isFailing ? theme.red : theme.dim))
            }
            Text(host.address).font(AppFont.ui(11)).foregroundStyle(focused ? theme.bg : theme.dim).lineLimit(1)
        }
        .foregroundStyle(focused ? theme.bg : theme.fg)
        .padding(.horizontal, 12)
        .frame(height: 38)
        .background(focused ? theme.yellow : (isSelected ? Color(nsColor: theme.line) : Color.clear))
    }

    @ViewBuilder private var detail: some View {
        if let host = model.host {
            VStack(alignment: .leading, spacing: 0) {
                hostHeader(host)
                Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
                sessionList(host)
            }
        } else {
            Text("Choose a host on the left.").foregroundStyle(theme.dim).padding(16)
        }
    }

    private func hostHeader(_ host: RemoteHost) -> some View {
        let message = host.message()
        return VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(host.name).fontWeight(.bold).foregroundStyle(theme.magenta)
                Text(host.stateTitle).foregroundStyle(host.isConnected ? theme.green : host.isFailing ? theme.red : theme.dim)
                Spacer()
                Button("Retry") { model.retry(host) }
                    .buttonStyle(.borderless)
                    .disabled(!host.canRetry || model.retrying.contains(host.name))
                    .help("Connect now, without waiting (r)")
                Button("New Session on \(host.name)…") { model.newSession(host.name) }
                    .buttonStyle(.borderless)
                    .help("⇧⌘N with this host chosen (n)")
            }
            Text(message.text).foregroundStyle(theme.dim).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
            if let copy = message.copy, !copy.isEmpty {
                Button("Copy Your Fingerprint") { model.copy(copy) }.buttonStyle(.borderless).help("c")
            }
            if let expected = message.expected {
                fingerprint("Expected", expected)
                fingerprint("It showed", message.seen ?? "")
            }
        }
        .padding(16)
    }

    private func fingerprint(_ label: String, _ value: String) -> some View {
        HStack(spacing: 8) {
            Text(label).foregroundStyle(theme.dim).frame(width: 80, alignment: .leading)
            Text(value.isEmpty ? "—" : value).font(.system(size: 11, design: .monospaced)).textSelection(.enabled)
        }
    }

    @ViewBuilder private func sessionList(_ host: RemoteHost) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            if let error = model.error {
                Text(error).foregroundStyle(theme.red).textSelection(.enabled).padding(.horizontal, 16).padding(.top, 8)
            }
            if model.loading {
                Text("Asking \(host.name) for its sessions…").foregroundStyle(theme.dim).padding(16)
            } else if model.sessions.isEmpty, model.error == nil, model.listed == host.name {
                Text("\(host.name) has no Remote sessions. Start one with New Session, or make one Remote on \(host.name).")
                    .foregroundStyle(theme.dim).padding(16)
            }
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(model.sessions.enumerated()), id: \.element.id) { index, session in
                        sessionRow(session, isCursor: model.panel == .sessions && index == model.sessionCursor)
                            .contentShape(Rectangle())
                            .onTapGesture { model.setCursor(session) }
                            .simultaneousGesture(TapGesture(count: 2).onEnded { model.open(session) })
                    }
                }
            }
        }
    }

    private func sessionRow(_ session: RemoteSession, isCursor: Bool) -> some View {
        let fg = isCursor ? theme.bg : theme.fg
        let dim = isCursor ? theme.bg : theme.dim
        let ago = RemoteTime.ago(session.lastActivity)
        return HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 8) {
                    Text(session.displayName).fontWeight(.bold).foregroundStyle(fg).lineLimit(1)
                    Text(session.harnessTitle).font(AppFont.ui(11)).foregroundStyle(dim)
                    if session.needsYou > 0 {
                        Text("needs you").font(AppFont.ui(11)).foregroundStyle(isCursor ? theme.bg : theme.yellow)
                    }
                }
                Text(session.dir).font(AppFont.ui(11)).foregroundStyle(dim).lineLimit(1).truncationMode(.head)
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 1) {
                Text(session.statusLine).font(AppFont.ui(11)).foregroundStyle(dim).lineLimit(1)
                Text(ago.isEmpty ? "" : "active \(ago)").font(AppFont.ui(11)).foregroundStyle(dim)
            }
            Button("Open") { model.open(session) }
                .buttonStyle(.borderless)
                .help("Add it if it isn't yet, and show it (return)")
            Button(session.added ? "Added" : "Add") { model.add(session) }
                .buttonStyle(.borderless)
                .disabled(session.added || model.adding.contains(session.id))
                .help(session.added ? "It's in the sidebar" : "Put it in the sidebar (a)")
        }
        .padding(.horizontal, 16)
        .frame(height: 42)
        .background(isCursor ? theme.yellow : Color.clear)
    }
}
