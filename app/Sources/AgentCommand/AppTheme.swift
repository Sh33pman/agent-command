import AppKit
import GhosttyKit
import Observation
import SwiftUI

/// The colors of the app's own interface, read from libghostty's resolved
/// config, so the interface and the terminal always match.
struct AppTheme {
    /// The current theme. Views that read it follow changes as they happen.
    /// Available once libghostty is initialized.
    static var shared: AppTheme { ThemeStore.instance.current }

    let background: NSColor
    let foreground: NSColor
    private let palette: [NSColor]

    var isDark: Bool {
        let c = background.usingColorSpace(.sRGB) ?? background
        return 0.2126 * c.redComponent + 0.7152 * c.greenComponent + 0.0722 * c.blueComponent < 0.5
    }

    var bg: Color { Color(nsColor: background) }
    var fg: Color { Color(nsColor: foreground) }
    /// Secondary text: the foreground faded towards the background.
    var dim: Color { Color(nsColor: mix(0.45)) }
    /// A line between areas.
    var line: NSColor { mix(0.8) }
    var backdrop: Color { Color.black.opacity(0.5) }

    var red: Color { ansi(1) }
    var green: Color { ansi(2) }
    var yellow: Color { ansi(3) }
    var blue: Color { ansi(4) }
    var magenta: Color { ansi(5) }

    private func ansi(_ index: Int) -> Color { Color(nsColor: palette[index]) }

    private func mix(_ amount: CGFloat) -> NSColor {
        foreground.blended(withFraction: amount, of: background) ?? foreground
    }

    init(config: ghostty_config_t?) {
        background = Self.color("background", config) ?? NSColor(srgbRed: 0.16, green: 0.17, blue: 0.2, alpha: 1)
        foreground = Self.color("foreground", config) ?? .white
        palette = Self.palette(config)
    }

    private static func color(_ key: String, _ config: ghostty_config_t?) -> NSColor? {
        var value = ghostty_config_color_s()
        guard key.withCString({ ghostty_config_get(config, &value, $0, UInt(key.utf8.count)) }) else {
            return nil
        }
        return nsColor(value)
    }

    private static func palette(_ config: ghostty_config_t?) -> [NSColor] {
        var value = ghostty_config_palette_s()
        let key = "palette"
        let found = key.withCString { ghostty_config_get(config, &value, $0, UInt(key.utf8.count)) }
        guard found else { return Array(repeating: .gray, count: 8) }
        return withUnsafeBytes(of: &value.colors) { raw in
            raw.bindMemory(to: ghostty_config_color_s.self).prefix(16).map(nsColor)
        }
    }

    private static func nsColor(_ c: ghostty_config_color_s) -> NSColor {
        NSColor(srgbRed: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: 1)
    }
}

/// Holds the current theme, so SwiftUI views and AppKit code hear of a change.
@Observable
final class ThemeStore {
    static let instance = ThemeStore()

    private(set) var current: AppTheme
    /// For AppKit views, which don't observe.
    @ObservationIgnored var onChange: (() -> Void)?

    private init() {
        current = AppTheme(config: GhosttyRuntime.shared.config)
    }

    func update(_ theme: AppTheme) {
        current = theme
        onChange?()
    }
}

/// Where libghostty finds its themes.
enum GhosttyAppearance {
    /// Where themes can be, as Ghostty searches for them.
    static var themeFolders: [String] {
        var folders = ["\(NSHomeDirectory())/.config/ghostty/themes"]
        if let dir = ProcessInfo.processInfo.environment["GHOSTTY_RESOURCES_DIR"] {
            folders.append(dir + "/themes")
        }
        return folders
    }

    /// Tells libghostty where themes are. Call before ghostty_init.
    static func locateResources(sourceFile: String = #filePath) {
        guard ProcessInfo.processInfo.environment["GHOSTTY_RESOURCES_DIR"] == nil else { return }
        let source = URL(fileURLWithPath: sourceFile)
        let candidates: [String?] = [
            Bundle.main.resourceURL?.appendingPathComponent("ghostty").path,
            // A development build, next to this package.
            source.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
                .appendingPathComponent("vendor/ghostty-resources").path,
            "/Applications/Ghostty.app/Contents/Resources/ghostty",
        ]
        for case let path? in candidates where FileManager.default.fileExists(atPath: path + "/themes") {
            setenv("GHOSTTY_RESOURCES_DIR", path, 1)
            return
        }
    }
}
