import AppKit
import CoreText
import SwiftUI

/// The font of the app's own interface. It defaults to JetBrains Mono, as the
/// terminal does, and follows `font-family` in the app's Ghostty config, so the
/// interface and the terminal match.
enum AppFont {
    static let defaultFamily = "JetBrains Mono"

    static let family: String = {
        if let chosen = configuredFamily(), NSFontManager.shared.availableFontFamilies.contains(chosen) {
            return chosen
        }
        return defaultFamily
    }()

    /// The app's Ghostty config.
    static var configPath: String {
        let env = ProcessInfo.processInfo.environment
        let dir = env["ACMD_CONFIG_DIR"].flatMap { $0.isEmpty ? nil : $0 }
            ?? NSString(string: "~/.config/agent-command").expandingTildeInPath
        return (dir as NSString).appendingPathComponent("ghostty.conf")
    }

    /// Makes the bundled font available to this process. Call before using it.
    static func register() {
        // Inside the app first: Bundle.module traps if its bundle isn't next to
        // the executable, which is only so in a development build.
        let name = "JetBrainsMono[wght]"
        guard let url = Bundle.main.url(forResource: name, withExtension: "ttf", subdirectory: "Fonts")
            ?? Bundle.module.url(forResource: name, withExtension: "ttf", subdirectory: "Fonts")
        else { return }
        CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
    }

    static func ui(_ size: CGFloat, weight: Font.Weight = .regular) -> Font {
        Font.custom(family, size: size).weight(weight)
    }

    static func appKit(_ size: CGFloat) -> NSFont {
        NSFontManager.shared.font(withFamily: family, traits: [], weight: 5, size: size)
            ?? .monospacedSystemFont(ofSize: size, weight: .regular)
    }

    private static func configuredFamily() -> String? {
        guard let text = try? String(contentsOfFile: configPath, encoding: .utf8) else { return nil }
        return firstFontFamily(inConfig: text)
    }

    /// The primary family: Ghostty treats later `font-family` lines as fallbacks.
    static func firstFontFamily(inConfig text: String) -> String? {
        for line in text.split(whereSeparator: \.isNewline) {
            let parts = line.split(separator: "=", maxSplits: 1).map {
                $0.trimmingCharacters(in: .whitespaces)
            }
            guard parts.count == 2, parts[0] == "font-family" else { continue }
            let value = parts[1].trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
            if !value.isEmpty { return value }
        }
        return nil
    }
}
