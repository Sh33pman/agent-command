import AppKit
import GhosttyKit

func ghostty_init_ok() -> Bool {
    ghostty_init(UInt(CommandLine.argc), CommandLine.unsafeArgv) == GHOSTTY_SUCCESS
}

/// The one libghostty app in this process, and its runtime callbacks. All C
/// calls to the app live here and in SurfaceView, because the embedding API
/// is unstable (docs/v2/architecture.md §9).
final class GhosttyRuntime {
    static let shared = GhosttyRuntime()

    let app: ghostty_app_t
    /// The resolved config, kept for reading colors from.
    private(set) var config: ghostty_config_t?

    private init() {
        let config = GhosttyRuntime.makeConfig(themeOverride: nil)

        var runtime = ghostty_runtime_config_s(
            userdata: nil,
            supports_selection_clipboard: false,
            wakeup_cb: { _ in
                DispatchQueue.main.async { GhosttyRuntime.shared.tick() }
            },
            action_cb: { _, target, action in
                GhosttyRuntime.handle(action, target: target)
            },
            read_clipboard_cb: { userdata, location, state, mimes, mimesLen, _ in
                GhosttyRuntime.readClipboard(userdata, location, state, mimes, mimesLen)
            },
            confirm_read_clipboard_cb: { userdata, confirm, state, request in
                GhosttyRuntime.confirmReadClipboard(userdata, confirm, state, request)
            },
            write_clipboard_cb: { _, location, content, len, confirm in
                GhosttyRuntime.writeClipboard(location, content, len, confirm)
            },
            close_surface_cb: { userdata, _ in
                guard let view = SurfaceView.from(userdata) else { return }
                trace("ghostty close surface")
                DispatchQueue.main.async { view.onClose?() }
            }
        )
        guard let app = ghostty_app_new(&runtime, config) else {
            fatalError("ghostty_app_new failed")
        }
        self.app = app
        self.config = config
    }

    /// Ghostty's defaults, then the app's own config. The user's Ghostty config
    /// stays with Ghostty. An override sets the theme whatever the file says.
    private static func makeConfig(themeOverride: ThemeOverride?) -> ghostty_config_t? {
        let config = ghostty_config_new()
        loadDefaults(config)
        let path = AppFont.configPath
        if FileManager.default.fileExists(atPath: path) {
            ghostty_config_load_file(config, path)
        }
        if let themeOverride {
            load("theme = \(themeOverride.name ?? "")", into: config)
        }
        ghostty_config_finalize(config)
        return config
    }

    /// A theme to use for now, without saving it. A nil name means no theme.
    struct ThemeOverride {
        let name: String?
    }

    /// Applies the config to every terminal and the interface, while running.
    func reloadConfig(themeOverride: ThemeOverride? = nil) {
        let next = GhosttyRuntime.makeConfig(themeOverride: themeOverride)
        guard let next else { return }
        ghostty_app_update_config(app, next)
        let previous = config
        config = next
        ThemeStore.instance.update(AppTheme(config: next))
        ghostty_config_free(previous)
    }

    /// The app's own defaults, loaded first so the user's config can override
    /// them. libghostty only reads config from files.
    private static func loadDefaults(_ config: ghostty_config_t?) {
        let defaults = """
            # A little more room above the terminal: Ghostty's default is 2.
            window-padding-y = 4,2
            """
        load(defaults, into: config)
    }

    /// libghostty only reads config from files.
    private static func load(_ text: String, into config: ghostty_config_t?) {
        let path = NSTemporaryDirectory() + "agent-command-ghostty-\(getpid()).conf"
        guard FileManager.default.createFile(atPath: path, contents: Data(text.utf8)) else { return }
        defer { try? FileManager.default.removeItem(atPath: path) }
        ghostty_config_load_file(config, path)
    }

    func tick() { ghostty_app_tick(app) }

    func setFocus(_ focused: Bool) { ghostty_app_set_focus(app, focused) }

    private static func handle(_ action: ghostty_action_s, target: ghostty_target_s) -> Bool {
        switch action.tag {
        case GHOSTTY_ACTION_QUIT:
            trace("ghostty quit action")
            DispatchQueue.main.async { NSApp.terminate(nil) }
            return true
        case GHOSTTY_ACTION_OPEN_URL:
            let link = action.action.open_url
            let text = TerminalLinks.string(link.url, Int(link.len))
            guard let url = TerminalLinks.url(text) else {
                trace("ghostty link not opened: not a web or mail link")
                return true
            }
            DispatchQueue.main.async { NSWorkspace.shared.open(url) }
            return true
        case GHOSTTY_ACTION_MOUSE_OVER_LINK:
            let link = action.action.mouse_over_link
            let over = !TerminalLinks.string(link.url, Int(link.len)).isEmpty
            let view = target.tag == GHOSTTY_TARGET_SURFACE
                ? SurfaceView.from(ghostty_surface_userdata(target.target.surface)) : nil
            DispatchQueue.main.async { view?.overLink = over }
            return true
        default:
            return false
        }
    }

    // MARK: Clipboard (plain text for now)

    private static func readClipboard(
        _ userdata: UnsafeMutableRawPointer?,
        _ location: ghostty_clipboard_e,
        _ state: UnsafeMutableRawPointer?,
        _ mimes: UnsafePointer<UnsafePointer<CChar>?>?,
        _ mimesLen: Int
    ) -> ghostty_clipboard_read_result_e {
        guard location == GHOSTTY_CLIPBOARD_STANDARD,
              let surface = SurfaceView.from(userdata)?.surface,
              let text = NSPasteboard.general.string(forType: .string)
        else { return GHOSTTY_CLIPBOARD_READ_UNAVAILABLE }
        complete(surface, text: text, state: state, confirmed: false)
        return GHOSTTY_CLIPBOARD_READ_STARTED
    }

    private static func confirmReadClipboard(
        _ userdata: UnsafeMutableRawPointer?,
        _ confirm: UnsafePointer<ghostty_clipboard_confirm_s>?,
        _ state: UnsafeMutableRawPointer?,
        _ request: ghostty_clipboard_request_e
    ) {
        guard let surface = SurfaceView.from(userdata)?.surface else { return }
        // A paste the user asked for goes through. A program reading the
        // clipboard is refused until the app has a permission prompt.
        guard request == GHOSTTY_CLIPBOARD_REQUEST_PASTE,
              let text = NSPasteboard.general.string(forType: .string)
        else {
            ghostty_surface_deny_clipboard_request(surface, state)
            return
        }
        complete(surface, text: text, state: state, confirmed: true)
    }

    private static func complete(
        _ surface: ghostty_surface_t,
        text: String,
        state: UnsafeMutableRawPointer?,
        confirmed: Bool
    ) {
        "text/plain".withCString { mime in
            text.withCString { data in
                var content = ghostty_clipboard_content_s(
                    mime: mime, data: data, len: text.utf8.count)
                withUnsafePointer(to: &content) { contents in
                    var done = ghostty_clipboard_complete_s(
                        contents: contents, contents_len: 1, available: nil, available_len: 0,
                        confirmed: confirmed, remember: false)
                    ghostty_surface_complete_clipboard_request(surface, &done, state)
                }
            }
        }
    }

    private static func writeClipboard(
        _ location: ghostty_clipboard_e,
        _ content: UnsafePointer<ghostty_clipboard_content_s>?,
        _ len: Int,
        _ confirm: Bool
    ) {
        // `confirm` marks a write from a program, refused until the app has a
        // permission prompt.
        guard location == GHOSTTY_CLIPBOARD_STANDARD, !confirm, let content else { return }
        for i in 0..<len {
            let entry = content[i]
            guard let mime = entry.mime, String(cString: mime) == "text/plain",
                  let data = entry.data
            else { continue }
            let bytes = UnsafeRawBufferPointer(start: data, count: entry.len)
            guard let text = String(bytes: bytes, encoding: .utf8) else { continue }
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            return
        }
    }
}
