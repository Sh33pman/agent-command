// swift-tools-version: 6.2
// acmd-speech: acmd's Swift speech engine (SPEC §8.9). Kokoro-82M
// (https://huggingface.co/hexgrad/Kokoro-82M) on Core ML through
// FluidAudio's Kokoro ANE v3, behind the same unix-socket protocol as the
// Python engine (internal/speech/assets/engine.py).
import PackageDescription

let package = Package(
    name: "acmd-speech",
    platforms: [.macOS(.v15)],  // Kokoro ANE v3 needs macOS 15
    products: [
        .executable(name: "acmd-speech", targets: ["acmd-speech"])
    ],
    dependencies: [
        // Only text to speech (+ NeMo text normalization: "$5" → "five
        // dollars"); no ASR or diarization.
        .package(
            url: "https://github.com/FluidInference/FluidAudio.git", exact: "0.18.2",
            traits: ["TTS", "NemoTextProcessing"])
    ],
    targets: [
        // Everything that doesn't need the model: voice blends, text
        // splitting, framing, requests. Unit-tested.
        .target(name: "SpeechCore"),
        .executableTarget(
            name: "acmd-speech",
            dependencies: ["SpeechCore", .product(name: "FluidAudio", package: "FluidAudio")]
        ),
        // Unit checks (make speech-test): an executable, because Swift
        // Testing and XCTest need Xcode.
        .executableTarget(name: "speech-core-checks", dependencies: ["SpeechCore"]),
    ]
)
