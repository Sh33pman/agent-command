// swift-tools-version: 6.2
// acmd-speech: acmd's Swift speech engine. Kokoro-82M
// (https://huggingface.co/hexgrad/Kokoro-82M) on MLX through mlx-audio-swift,
// answering acmd on a unix socket (internal/speech). Build it with xcodebuild
// (make speech-helper): only Xcode compiles MLX's Metal shaders.
import PackageDescription

let package = Package(
    name: "acmd-speech",
    platforms: [.macOS(.v26)],
    products: [
        .executable(name: "acmd-speech", targets: ["acmd-speech"])
    ],
    dependencies: [
        .package(url: "https://github.com/Neywa-Labs/mlx-audio-swift.git", exact: "0.1.8"),
        .package(url: "https://github.com/ml-explore/mlx-swift.git", from: "0.33.0"),
        .package(url: "https://github.com/huggingface/swift-huggingface.git", from: "0.8.1"),
    ],
    targets: [
        // Everything that doesn't need the model: voice blends, text
        // splitting, framing, requests. Unit-tested.
        .target(name: "SpeechCore"),
        .executableTarget(
            name: "acmd-speech",
            dependencies: [
                "SpeechCore",
                .product(name: "MLXAudioTTS", package: "mlx-audio-swift"),
                .product(name: "MLX", package: "mlx-swift"),
                .product(name: "HuggingFace", package: "swift-huggingface"),
            ]
        ),
        // Unit checks (make speech-test): an executable, because Swift
        // Testing and XCTest need Xcode.
        .executableTarget(name: "speech-core-checks", dependencies: ["SpeechCore"]),
    ]
)
