// swift-tools-version: 6.2
// The acmd v2 app. GhosttyKit comes from scripts/build-ghostty.sh, which
// copies it to vendor/ (git-ignored).
import PackageDescription

let package = Package(
    name: "AgentCommand",
    platforms: [.macOS(.v26)],
    targets: [
        .binaryTarget(name: "GhosttyKit", path: "vendor/GhosttyKit.xcframework"),
        .executableTarget(
            name: "AgentCommand",
            dependencies: ["GhosttyKit"],
            resources: [.copy("Resources/Fonts")],
            // Swift 6 checks data races. An app's types run on the main thread
            // unless they say otherwise (nonisolated).
            swiftSettings: [.swiftLanguageMode(.v6), .defaultIsolation(MainActor.self)],
            linkerSettings: [
                .linkedFramework("AppKit"),
                .linkedFramework("Carbon"),
                .linkedFramework("CoreGraphics"),
                .linkedFramework("CoreText"),
                .linkedFramework("CoreVideo"),
                .linkedFramework("IOSurface"),
                .linkedFramework("Metal"),
                .linkedFramework("QuartzCore"),
                .linkedLibrary("c++"),
            ]
        ),
        .testTarget(
            name: "AgentCommandTests",
            dependencies: ["AgentCommand"],
            swiftSettings: [.swiftLanguageMode(.v6), .defaultIsolation(MainActor.self)]
        ),
    ]
)
