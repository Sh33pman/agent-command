import AppKit
import SwiftUI

/// Settings → Remote: Hosting, this Mac's identity, its clients and their
/// requests. What it shows and does is in RemoteSettingsModel and
/// SettingsModel; this lays it out.
struct RemoteSettings: View {
    let model: SettingsModel
    let remote: RemoteSettingsModel
    let store: SessionStore
    /// The hosts this Mac connects to, as a client.
    let knownHosts: KnownHostsModel
    /// Opens the Remote Hosts browser, at a host when one is given.
    let browseHosts: (String?) -> Void
    @State private var port = ""
    @State private var addingHost = false
    @State private var editingHost: RemoteHost?
    @State private var removingHost: RemoteHost?
    @State private var confirmRegenerate = false
    @State private var adding = false
    @State private var renaming: RemoteClient?
    @State private var revoking: RemoteClient?
    @State private var approving: RemoteRequest?

    var body: some View {
        let v = model.values
        if let error = remote.error {
            Section {
                Text(error).foregroundStyle(.red).textSelection(.enabled)
            }
        }
        hosting(v)
        Section {
            AcmdToggle(
                title: "Start new sessions as Remote",
                note: "What ⌘N and acmd new do, and where the Remote checkbox of ⇧⌘N and ⇧⌘T starts. Clients of Hosting can see and use Remote sessions; change one from its menu in the sidebar.",
                key: "remote.new_sessions_remote", model: model, value: v.newSessionsRemote)
        }
        requests
        clients
        connected
        identity(v)
        hosts
        Section {
            Text("Agent Remote Control, for using sessions from claude.ai and the Claude app, is a separate setting, in General.")
                .font(.caption).foregroundStyle(.secondary)
        }
        .onAppear {
            port = String(v.hostPort)
            remote.refresh()
        }
        .onChange(of: v.hostPort) { port = String(v.hostPort) }
        .task {
            // Who's attached to what changes without a change of status.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                remote.loadLists()
            }
        }
        .confirmationDialog("Make a new identity for this Mac?", isPresented: $confirmRegenerate) {
            Button("Make a New Identity", role: .destructive) { remote.regenerate() }
        } message: {
            Text("Every host and client that trusts this Mac has to trust its new fingerprint before it can connect again. A cert of your own isn't changed.")
        }
        .confirmationDialog(
            "Revoke \(revoking?.name ?? "")?", isPresented: Binding(get: { revoking != nil }, set: { if !$0 { revoking = nil } })
        ) {
            Button("Revoke", role: .destructive) {
                if let revoking { remote.revoke(revoking) }
            }
        } message: {
            Text("It's disconnected now, and can't connect again until it's authorized again.")
        }
        .confirmationDialog(
            removingHost.map { KnownHostsModel.removePrompt($0).title } ?? "",
            isPresented: Binding(get: { removingHost != nil }, set: { if !$0 { removingHost = nil } })
        ) {
            Button("Remove", role: .destructive) {
                if let removingHost { knownHosts.remove(removingHost) }
            }
        } message: {
            Text(removingHost.map { KnownHostsModel.removePrompt($0).note } ?? "")
        }
        .sheet(isPresented: $addingHost) {
            HostSheet(model: knownHosts, host: nil)
        }
        .sheet(item: $editingHost) { host in
            HostSheet(model: knownHosts, host: host)
        }
        .sheet(isPresented: $adding) {
            AddClientSheet { what, name in remote.authorize(what, name: name) }
        }
        .sheet(item: $renaming) { client in
            NameSheet(title: "Rename \(client.name)", action: "Rename", name: client.name) { remote.rename(client, to: $0) }
        }
        .sheet(item: $approving) { request in
            NameSheet(
                title: "Approve \(request.displayName)?", action: "Approve", name: request.commonName,
                note: "It can use this Mac's Remote sessions from now on. Fingerprint \(request.fingerprint).") { remote.approve(request, name: $0) }
        }
    }

    @ViewBuilder private func hosting(_ v: AcmdSettings.Values) -> some View {
        let status = remote.status
        Section("Hosting") {
            Toggle(isOn: Binding(get: { status?.enabled ?? v.hostEnabled }, set: { remote.setHosting($0) })) {
                Text("Hosting")
                Text("Let other Agent Commands use this Mac's Remote sessions. Only clients you authorize get in.")
            }
            .disabled(remote.busy == "hosting")
            LabeledContent("Status") {
                Text(status?.line ?? "Off")
                    .foregroundStyle(status?.isFailing == true ? .red : .secondary)
                    .textSelection(.enabled)
            }
            Picker(selection: Binding(get: { v.hostListen }, set: { model.set("remote.host.listen", $0) })) {
                Text("Localhost").tag("localhost")
                Text("All interfaces").tag("all")
            } label: {
                Text("Listen on")
                Text(v.hostListen == "all"
                     ? "Other machines on your network can reach this Mac. Only authorized clients get in."
                     : "Only this Mac can reach it, and other machines through an SSH tunnel.")
            }
            .disabled(model.busy == "remote.host.listen")
            LabeledContent {
                HStack {
                    TextField("7433", text: $port).labelsHidden().frame(width: 80)
                    Button("Save") {
                        if let n = RemoteSettingsModel.port(port) { model.setAll(RemoteSettingsModel.portChanges(n)) }
                    }
                    .disabled(RemoteSettingsModel.port(port).map { $0 == v.hostPort } ?? true)
                }
            } label: {
                Text("Port")
                Text("Clients connect to it. A port of your own isn't swapped for another: if it's taken, Hosting stops with an error that names it.")
            }
            AcmdToggle(
                title: "Use another port if this one is taken",
                note: "The status above shows the port in use.",
                key: "remote.host.port_fallback", model: model, value: v.hostPortFallback)
        }
    }

    /// The client side: the hosts this Mac uses Remote sessions on.
    @ViewBuilder private var hosts: some View {
        Section {
            if let error = knownHosts.error {
                Text(error).foregroundStyle(.red).textSelection(.enabled)
            }
            if store.remoteHosts.isEmpty {
                Text("None yet. Add a host by its address and fingerprint: acmd remote identity on the host shows it.")
                    .foregroundStyle(.secondary)
            }
            ForEach(store.remoteHosts) { host in
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(host.name).bold()
                        Text(host.address).font(.caption).foregroundStyle(.secondary)
                        FingerprintLine(fingerprint: host.fingerprint)
                        if host.isFailing || host.state == .pending {
                            Text(host.message().text).font(.caption).foregroundStyle(host.isFailing ? .red : .secondary)
                                .textSelection(.enabled)
                        }
                    }
                    Spacer()
                    Label(host.stateTitle, systemImage: host.isConnected ? "circle.fill" : "circle")
                        .foregroundStyle(host.isConnected ? .green : host.isFailing ? .red : .secondary)
                        .font(.caption)
                    Menu {
                        Button("Browse Sessions…") { browseHosts(host.name) }
                        Button("Retry") { knownHosts.retry(host) }.disabled(!host.canRetry)
                        Button("Edit…") { editingHost = host }.disabled(host.inConfig)
                        Divider()
                        Button("Remove…", role: .destructive) { removingHost = host }.disabled(host.inConfig)
                        if host.inConfig { Text("Set in config.toml: edit the file to change it") }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                }
                .disabled(knownHosts.busy == host.name)
            }
            HStack {
                Button("Add Host…") {
                    knownHosts.clearError()
                    addingHost = true
                }
                .disabled(knownHosts.busy == "")
                Button("Browse Remote Hosts…") { browseHosts(nil) }
                    .disabled(store.remoteHosts.isEmpty)
            }
        } header: {
            Text("Known hosts")
        } footer: {
            Text("Other Agent Commands whose Remote sessions this Mac uses. Each one must approve this Mac first.")
                .font(.caption).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var requests: some View {
        Section("Pending requests") {
            if remote.requests.isEmpty {
                Text("No clients are waiting for approval.").foregroundStyle(.secondary)
            }
            ForEach(remote.requests) { request in
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(request.displayName).bold()
                        FingerprintLine(fingerprint: request.fingerprint)
                        Text(request.detail()).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Approve…") { approving = request }
                    Button("Deny") { remote.deny(request) }
                }
                .disabled(remote.busy == "request:\(request.id)")
                .listRowBackground(remote.highlighted == request.id ? Color.accentColor.opacity(0.15) : nil)
            }
        }
    }

    @ViewBuilder private var clients: some View {
        Section("Authorized clients") {
            if remote.clients.isEmpty {
                Text("None yet. Add one, or approve one when it asks to connect.").foregroundStyle(.secondary)
            }
            ForEach(remote.clients) { client in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(client.name).bold()
                        FingerprintLine(fingerprint: client.fingerprint)
                    }
                    Spacer()
                    Label(client.isConnected ? "Connected" : "Not connected", systemImage: client.isConnected ? "circle.fill" : "circle")
                        .foregroundStyle(client.isConnected ? .green : .secondary)
                        .font(.caption)
                    Menu {
                        Button("Rename…") { renaming = client }.disabled(client.inConfig)
                        Button("Disconnect") { remote.disconnect(client) }.disabled(!client.isConnected)
                        Divider()
                        Button("Revoke…", role: .destructive) { revoking = client }.disabled(client.inConfig)
                        if client.inConfig { Text("Set in config.toml: edit the file to change it") }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .menuStyle(.borderlessButton)
                    .fixedSize()
                }
                .disabled(remote.busy == "client:\(client.fingerprint)")
            }
            Button("Add Client…") { adding = true }
                .disabled(remote.busy == "authorize")
        }
    }

    @ViewBuilder private var connected: some View {
        let rows = remote.connectionRows { id in store.sessions.first { $0.id == id }?.displayName }
        Section("Connected clients") {
            if rows.isEmpty {
                Text("No one is connected.").foregroundStyle(.secondary)
            }
            ForEach(rows) { row in
                VStack(alignment: .leading, spacing: 2) {
                    Text(row.client).bold()
                    Text(row.sessions)
                    Text(row.from).font(.caption).foregroundStyle(.secondary)
                }
            }
        }
    }

    @ViewBuilder private func identity(_ v: AcmdSettings.Values) -> some View {
        Section("Identity") {
            let id = remote.identity
            LabeledContent {
                FingerprintLine(fingerprint: id?.host.fingerprint ?? "", short: false)
            } label: {
                Text(id?.client == nil ? "This Mac's fingerprint" : "Hosting fingerprint")
                Text("Clients pin it, and hosts authorize it. Give it to the other machine to check.")
            }
            if let client = id?.client {
                LabeledContent("Client fingerprint") {
                    FingerprintLine(fingerprint: client.fingerprint, short: false)
                }
            }
            ForEach(RemoteRole.allCases) { role in
                let cert = role.cert(in: v)
                LabeledContent {
                    Menu(cert.isEmpty ? "acmd's identity" : (cert as NSString).lastPathComponent) {
                        Button("Use acmd's identity") { remote.useAcmdIdentity(role) }.disabled(cert.isEmpty)
                        Button("Choose your own…") { chooseCert(role) }
                    }
                    .fixedSize()
                    .disabled(remote.busy == "cert:\(role.rawValue)")
                } label: {
                    Text(role.title)
                    Text(cert.isEmpty
                         ? (role == .host ? "What this Mac shows clients." : "What this Mac shows the hosts it connects to.")
                         : NSString(string: cert).abbreviatingWithTildeInPath)
                }
            }
            LabeledContent("acmd's identity") {
                Button("Regenerate…") { confirmRegenerate = true }
                    .disabled(remote.busy == "regenerate")
            }
        }
    }

    /// A cert, then its key: acmd checks they belong together before using them.
    private func chooseCert(_ role: RemoteRole) {
        guard let cert = Self.choosePEM("Choose the \(role == .host ? "Hosting" : "client") cert (a PEM file)"),
              let key = Self.choosePEM("Now choose its private key (a PEM file)")
        else { return }
        remote.useOwnCert(role, cert: cert, key: key)
    }

    static func choosePEM(_ message: String) -> String? {
        let panel = NSOpenPanel()
        panel.message = message
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.showsHiddenFiles = true
        return panel.runModal() == .OK ? panel.url?.path : nil
    }
}

/// A fingerprint, short or whole, with a Copy button.
private struct FingerprintLine: View {
    let fingerprint: String
    var short = true

    var body: some View {
        HStack(spacing: 4) {
            Text(fingerprint.isEmpty ? "—" : (short ? RemoteFingerprint.short(fingerprint) : fingerprint))
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .help(fingerprint)
            Button {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(fingerprint, forType: .string)
            } label: {
                Image(systemName: "doc.on.doc")
            }
            .buttonStyle(.borderless)
            .help("Copy the fingerprint")
            .disabled(fingerprint.isEmpty)
        }
    }
}

/// Asks for a name: to approve a request, or rename a client.
private struct NameSheet: View {
    let title: String
    let action: String
    @State var name: String
    var note = ""
    let done: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title).font(.headline)
            if !note.isEmpty { Text(note).font(.caption).foregroundStyle(.secondary).textSelection(.enabled) }
            TextField("Name", text: $name)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(action) {
                    done(name)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 420)
    }
}

/// Adds a known host, or edits one: its name, address, and the fingerprint
/// to pin, pasted in or read from its cert file.
private struct HostSheet: View {
    let model: KnownHostsModel
    /// nil adds one.
    let host: RemoteHost?
    @State private var form: KnownHostForm
    @State private var tried = false
    @Environment(\.dismiss) private var dismiss

    init(model: KnownHostsModel, host: RemoteHost?) {
        self.model = model
        self.host = host
        _form = State(initialValue: host.map(KnownHostForm.init(host:)) ?? KnownHostForm())
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(host.map { "Edit \($0.name)" } ?? "Add a host").font(.headline)
            Text("On the host, acmd remote identity shows its fingerprint, and Settings → Remote its port. Then approve this Mac there.")
                .font(.caption).foregroundStyle(.secondary)
            TextField("Name, as studio-mac", text: $form.name)
            TextField("Address, as studio-mac.local:7433", text: $form.address)
            Picker("", selection: $form.byFile) {
                Text("Fingerprint").tag(false)
                Text("Cert file").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            if form.byFile {
                HStack {
                    Text(form.cert.isEmpty ? "No file chosen" : NSString(string: form.cert).abbreviatingWithTildeInPath)
                        .foregroundStyle(form.cert.isEmpty ? .secondary : .primary)
                        .lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Choose…") { form.cert = RemoteSettings.choosePEM("Choose the host's cert (a PEM file)") ?? form.cert }
                }
            } else {
                TextField("SHA256:…", text: $form.fingerprint).font(.system(.body, design: .monospaced))
            }
            if let problem = tried ? (model.error ?? form.problem) : model.error {
                Text(problem).font(.caption).foregroundStyle(.red).textSelection(.enabled)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button(host == nil ? "Add" : "Save") {
                    tried = true
                    let done: (Bool) -> Void = { ok in if ok { dismiss() } }
                    if let host { model.edit(host, form, done: done) } else { model.add(form, done: done) }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(model.busy != nil)
            }
        }
        .padding(20)
        .frame(width: 460)
        .onAppear { model.clearError() }
    }
}

/// Authorizes a client by its cert file or the fingerprint it shows.
private struct AddClientSheet: View {
    let done: (String, String) -> Void
    @State private var byFile = false
    @State private var fingerprint = ""
    @State private var file = ""
    @State private var name = ""
    @Environment(\.dismiss) private var dismiss

    private var what: String { byFile ? file : fingerprint }
    private var ready: Bool { byFile ? !file.isEmpty : RemoteFingerprint.looksValid(fingerprint) }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Add a client").font(.headline)
            Text("Run acmd remote identity on the client for its fingerprint, or choose its cert file.")
                .font(.caption).foregroundStyle(.secondary)
            Picker("", selection: $byFile) {
                Text("Fingerprint").tag(false)
                Text("Cert file").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            if byFile {
                HStack {
                    Text(file.isEmpty ? "No file chosen" : NSString(string: file).abbreviatingWithTildeInPath)
                        .foregroundStyle(file.isEmpty ? .secondary : .primary)
                        .lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("Choose…") { file = RemoteSettings.choosePEM("Choose the client's cert (a PEM file)") ?? file }
                }
            } else {
                TextField("SHA256:…", text: $fingerprint).font(.system(.body, design: .monospaced))
            }
            TextField("Name (optional: its cert's name)", text: $name)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Add") {
                    done(what, name)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!ready)
            }
        }
        .padding(20)
        .frame(width: 460)
    }
}
