import Foundation
import Testing

@testable import AgentCommand

@Test func themeIsTheLastOneSet() {
    #expect(ThemeConfig.theme(inConfig: "") == nil)
    #expect(ThemeConfig.theme(inConfig: "font-size = 13\n") == nil)
    #expect(ThemeConfig.theme(inConfig: "theme = Nord\n") == "Nord")
    #expect(ThemeConfig.theme(inConfig: "theme=Nord\ntheme =  Dracula+ \n") == "Dracula+", "the last line wins")
    #expect(ThemeConfig.theme(inConfig: "theme = Nord\ntheme =\n") == nil, "an empty value clears it")
    #expect(ThemeConfig.theme(inConfig: "window-theme = dark\n") == nil, "only the theme key")
}

@Test func settingAThemeKeepsTheRestOfTheConfig() {
    #expect(ThemeConfig.setting(theme: "Nord", inConfig: "") == "theme = Nord\n")
    #expect(ThemeConfig.setting(theme: nil, inConfig: "") == "")
    #expect(ThemeConfig.setting(theme: "Nord", inConfig: "font-size = 13") == "font-size = 13\ntheme = Nord\n")
    #expect(
        ThemeConfig.setting(theme: "Nord", inConfig: "a = 1\ntheme = Old\nb = 2\ntheme = Older\n")
            == "a = 1\ntheme = Nord\nb = 2\n",
        "the first theme line is replaced in place, the rest removed")
    #expect(ThemeConfig.setting(theme: nil, inConfig: "a = 1\ntheme = Old\n") == "a = 1\n")
    #expect(ThemeConfig.setting(theme: nil, inConfig: "theme = Old\n") == "")
}

@Test func themeNamesAreTheFilesInTheFolders() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("themes-\(UUID().uuidString)")
    defer { try? FileManager.default.removeItem(at: root) }
    let one = root.appendingPathComponent("one"), two = root.appendingPathComponent("two")
    try FileManager.default.createDirectory(at: one.appendingPathComponent("subfolder"), withIntermediateDirectories: true)
    try FileManager.default.createDirectory(at: two, withIntermediateDirectories: true)
    for (folder, name) in [(one, "nord"), (one, "Dracula"), (one, ".hidden"), (two, "nord"), (two, "Zenburn")] {
        try Data().write(to: folder.appendingPathComponent(name))
    }
    let names = ThemeConfig.themeNames(in: [one.path, two.path, root.appendingPathComponent("missing").path])
    #expect(names == ["Dracula", "nord", "Zenburn"], "sorted ignoring case, no duplicates, folders or hidden files")
}

@Test func anyKeyCanBeSetLikeTheTheme() {
    #expect(ThemeConfig.value("font-size", inConfig: "font-size = 14\nfont-size = 15\n") == "15")
    #expect(ThemeConfig.value("font-family", inConfig: "font-family = \"Fira Code\"\n") == "Fira Code", "quotes aren't part of it")
    let fonts = "font-family = A\nx = 1\nfont-family = B\n"
    #expect(ThemeConfig.setting("font-family", to: "C", inConfig: fonts, keepingOthers: true) == "font-family = C\nx = 1\nfont-family = B\n")
    #expect(ThemeConfig.setting("font-family", to: "C", inConfig: fonts) == "font-family = C\nx = 1\n")
    #expect(ThemeConfig.setting("font-family", to: nil, inConfig: fonts, keepingOthers: true) == "x = 1\nfont-family = B\n")
}
