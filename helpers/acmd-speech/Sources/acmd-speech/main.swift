// acmd-speech: the speech service (SPEC §8.9). It turns text into audio and plays
// it, on its own: nothing about it depends on acmd's daemon or app, which are
// clients like any other.
//
//   acmd-speech --prefetch --dir DIR --voice SPEC
//       check that the model in DIR loads and prepare the voice, then exit
//   acmd-speech --serve SOCKET --dir DIR --voice SPEC [--idle SECONDS]
//       load once and answer requests on a unix socket until idle
//
// A request is one JSON line, and the reply is one JSON line {"ok": true, ...} or
// {"ok": false, "error": ...}:
//
//   {"op": "speak", "text": ..., "speed": 1.0}
//       plays the text, replacing whatever is playing. The ok has the read's "id".
//       After it, one more line
//       comes when it ends: {"done": true}, with "error" if it failed. Closing the
//       connection doesn't stop it.
//   {"op": "stop", "id": 7}         stops that read, if it's still the one playing;
//                                   without an id, whatever is playing
//   {"op": "status"}                {"ok": true, "speaking": true}
//   {"op": "ping"}
//   {"op": "synth", "text": ..., "out": WAV path, "speed": 1.0}
//       writes the audio to a file instead of playing it
//   {"op": "stream", "text": ..., "speed": 1.0}
//       sends the audio instead of playing it: after the ok come frames of 16-bit
//       little-endian mono PCM at "rate" Hz (4-byte big-endian length + data), ended
//       by a 0 length (or 0xFFFFFFFF and an error line). Closing the connection
//       stops it.
//
// The model is Kokoro-82M (https://huggingface.co/hexgrad/Kokoro-82M) on MLX
// through mlx-audio-swift.
import AVFoundation
import Darwin
import Foundation
import HuggingFace
import MLX
import MLXAudioCore
import MLXAudioTTS
import SpeechCore

let sampleRate = 24_000
let modelRepo = "mlx-community/Kokoro-82M-bf16"

struct Options {
    var prefetch = false
    var serve: String?
    var dir: String?
    var voice = "af_heart:0.6,af_bella:0.4"
    var idle: Double = 600
}

func parseArgs(_ args: [String]) throws -> Options {
    var o = Options()
    var it = args.makeIterator()
    func value(_ flag: String) throws -> String {
        guard let v = it.next() else { throw SpeechError.badRequest("\(flag) needs a value") }
        return v
    }
    while let a = it.next() {
        switch a {
        case "--prefetch": o.prefetch = true
        case "--serve": o.serve = try value(a)
        case "--dir": o.dir = try value(a)
        case "--voice": o.voice = try value(a)
        case "--model": _ = try value(a)  // accepted and ignored (the old engine took one); the model is fixed
        case "--idle":
            guard let v = Double(try value(a)), v > 0 else { throw SpeechError.badRequest("bad --idle") }
            o.idle = v
        case "--version": print("acmd-speech (mlx-audio-swift 0.1.8, Kokoro-82M on MLX)"); exit(0)
        default: throw SpeechError.badRequest("unknown argument \(a)")
        }
    }
    if o.dir == nil { throw SpeechError.badRequest("--dir is required") }
    if !o.prefetch && o.serve == nil {
        throw SpeechError.badRequest("--prefetch or --serve")
    }
    return o
}

func log(_ s: String) {
    FileHandle.standardError.write(Data("\(ISO8601DateFormatter().string(from: Date())) \(s)\n".utf8))
}

/// The model, loaded once, with the voice (or blend) in use. An actor, so requests
/// take turns with it.
actor Engine {
    let model: KokoroModel
    let voice: String

    init(dir: URL, spec: String) async throws {
        let voices = try parseVoice(spec)
        try checkAvailable(voices)
        // acmd installs the model. Without it the library would download its own
        // copy from wherever it was built to look.
        let modelFolder = dir.appendingPathComponent("mlx-audio/\(modelRepo.replacingOccurrences(of: "/", with: "_"))")
        for folder in [modelFolder, dir.appendingPathComponent("mlx-audio/beshkenadze_kitten-tts-g2p")]
        where !FileManager.default.fileExists(atPath: folder.path) {
            throw SpeechError.badRequest("the model isn't installed: run `acmd speech install`")
        }
        // Without a limit, MLX keeps gigabytes of buffers from each piece of speech
        // for as long as the engine stays up.
        Memory.cacheLimit = 128 << 20
        let loaded = try await TTS.loadModel(modelRepo: modelRepo, cache: HubCache(cacheDirectory: dir))
        guard let kokoro = loaded as? KokoroModel else { throw SpeechError.badRequest("\(modelRepo) isn't Kokoro") }
        model = kokoro
        voice = voiceName(for: voices)
        if voices.count > 1 {
            try writeBlend(voices, to: modelFolder.appendingPathComponent("voices/\(voice).safetensors"))
        }
    }

    /// Stores the weighted sum of the voices as a voice of its own, next to the
    /// downloaded ones, where the model looks for voices.
    func writeBlend(_ voices: [VoiceWeight], to file: URL) throws {
        var packs: [[Float]] = []
        var shape: [Int] = []
        for v in voices {
            let pack = try model.loadVoice(named: v.name)
            shape = pack.shape
            packs.append(pack.asArray(Float.self))
        }
        try save(arrays: ["voice": MLXArray(try blend(packs, voices), shape)], url: file)
    }

    func samples(_ text: String, speed: Float) async throws -> [Float] {
        model.speed = speed
        let audio = try await model.generate(
            text: text, voice: voice, refAudio: nil, refText: nil, language: nil,
            generationParameters: model.defaultGenerationParameters)
        return audio.asArray(Float.self)
    }

    /// One piece of a request. A piece that fails is said in two halves instead;
    /// a half that still fails is skipped and logged rather than ending the whole
    /// reply.
    func speak(_ text: String, speed: Float) async throws -> [Float] {
        do {
            return try await samples(text, speed: speed)
        } catch is CancellationError {
            throw CancellationError()
        } catch {
            log("piece failed, saying it in two halves: \(text.debugDescription): \(error)")
        }
        var out: [Float] = []
        for half in halves(text) {
            do {
                out += try await samples(half, speed: speed)
            } catch is CancellationError {
                throw CancellationError()
            } catch {
                log("half failed, skipped: \(half.debugDescription): \(error)")
            }
        }
        return out
    }
}

// MARK: - playing

/// Plays samples through the default output as they're ready.
final class Player: @unchecked Sendable {
    private let audio = AVAudioEngine()
    private let node = AVAudioPlayerNode()
    private let format: AVAudioFormat
    private let lock = NSLock()
    private var stopped = false
    private var observer: NSObjectProtocol?

    init() throws {
        guard let format = AVAudioFormat(standardFormatWithSampleRate: Double(sampleRate), channels: 1) else {
            throw SpeechError.badRequest("no audio format")
        }
        self.format = format
        audio.attach(node)
        audio.connect(node, to: audio.mainMixerNode, format: format)
        try audio.start()
        node.play()
        // A change of output device stops the audio engine, and with it the callbacks
        // that say what has played. This read ends there; the next uses the new device.
        observer = NotificationCenter.default.addObserver(
            forName: .AVAudioEngineConfigurationChange, object: audio, queue: nil
        ) { [weak self] _ in self?.stop() }
    }

    private func buffer(_ samples: [Float]) -> AVAudioPCMBuffer? {
        guard let b = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(samples.count)),
            let channel = b.floatChannelData?[0]
        else { return nil }
        b.frameLength = AVAudioFrameCount(samples.count)
        samples.withUnsafeBufferPointer { channel.update(from: $0.baseAddress!, count: samples.count) }
        return b
    }

    func play(_ samples: [Float]) {
        lock.lock()
        defer { lock.unlock() }
        guard !stopped, let b = buffer(samples) else { return }
        node.scheduleBuffer(b, completionHandler: nil)
    }

    /// Waits until everything has played, or playing stops.
    func drain() async {
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            lock.lock()
            defer { lock.unlock() }
            guard !stopped, let end = buffer([0]) else {
                done.resume()
                return
            }
            node.scheduleBuffer(end, completionCallbackType: .dataPlayedBack) { _ in done.resume() }
        }
    }

    func stop() {
        lock.lock()
        let already = stopped
        stopped = true
        lock.unlock()
        if already { return }
        if let observer { NotificationCenter.default.removeObserver(observer) }
        node.stop()
        audio.stop()
    }
}

/// What's being read aloud: at most one thing, whoever asked for it.
actor Speaker {
    private let engine: Engine
    private var current: (id: Int, task: Task<String?, Never>, player: Player)?
    private var nextID = 0

    init(engine: Engine) {
        self.engine = engine
    }

    var isSpeaking: Bool { current != nil }

    /// Starts reading, replacing whatever is playing. The task's value is what went
    /// wrong, if anything.
    func speak(_ text: String, speed: Float) throws -> (id: Int, task: Task<String?, Never>) {
        stop()
        let player = try Player()
        nextID += 1
        let id = nextID
        let engine = self.engine
        let task = Task { () -> String? in
            var failure: String?
            do {
                for piece in pieces(text) {
                    try Task.checkCancellation()
                    let samples = try await engine.speak(piece, speed: speed)
                    try Task.checkCancellation()
                    if !samples.isEmpty { player.play(samples) }
                }
                await player.drain()
            } catch is CancellationError {
            } catch {
                failure = "\(error)"
            }
            await self.finished(id)
            return failure
        }
        current = (id, task, player)
        return (id, task)
    }

    /// A client leaving stops only its own read: one that has been replaced may be
    /// asking after the new one started.
    func stop(id: Int? = nil) {
        if let id, current?.id != id { return }
        current?.task.cancel()
        current?.player.stop()
        current = nil
    }

    private func finished(_ id: Int) {
        if current?.id == id { stop() }
    }
}

// MARK: - socket server

func writeAll(_ fd: Int32, _ d: Data) -> Bool {
    d.withUnsafeBytes { raw -> Bool in
        var off = 0
        while off < raw.count {
            let n = Darwin.write(fd, raw.baseAddress! + off, raw.count - off)
            if n < 0 && errno == EINTR { continue }
            if n <= 0 { return false }
            off += n
        }
        return true
    }
}

func readLine(_ fd: Int32) -> Data? {
    var out = Data()
    var b: UInt8 = 0
    while out.count < 16 << 20 {
        let n = Darwin.read(fd, &b, 1)
        if n < 0 && errno == EINTR { continue }
        if n <= 0 { return out.isEmpty ? nil : out }
        if b == 0x0a { return out }
        out.append(b)
    }
    return nil
}

/// Whether the other end closed the connection (stop playback).
func peerClosed(_ fd: Int32) -> Bool {
    var b: UInt8 = 0
    let n = recv(fd, &b, 1, MSG_PEEK | MSG_DONTWAIT)
    return n == 0 || (n < 0 && errno != EAGAIN && errno != EWOULDBLOCK)
}

func handle(_ c: Int32, _ engine: Engine, _ speaker: Speaker) async {
    guard let line = readLine(c) else { return }
    let req: Request
    do { req = try Request.parse(line) } catch {
        _ = writeAll(c, replyLine(["ok": false, "error": "\(error)"]))
        return
    }
    let speed = req.speed ?? 1
    switch req.op {
    case "ping":
        _ = writeAll(c, replyLine(["ok": true, "engine": "swift"]))
    case "speak":
        do {
            let read = try await speaker.speak(req.text ?? "", speed: speed)
            guard writeAll(c, replyLine(["ok": true, "id": read.id])) else { return }
            if let failure = await read.task.value {
                _ = writeAll(c, replyLine(["done": true, "error": failure]))
            } else {
                _ = writeAll(c, replyLine(["done": true]))
            }
        } catch {
            _ = writeAll(c, replyLine(["ok": false, "error": "can't play audio: \(error)"]))
        }
    case "stop":
        await speaker.stop(id: req.id)
        _ = writeAll(c, replyLine(["ok": true]))
    case "status":
        _ = writeAll(c, replyLine(["ok": true, "speaking": await speaker.isSpeaking]))
    case "stream":
        guard writeAll(c, replyLine(["ok": true, "rate": sampleRate])) else { return }
        do {
            for piece in pieces(req.text ?? "") {
                if peerClosed(c) { return }
                let s = try await engine.speak(piece, speed: speed)
                if s.isEmpty { continue }
                if !writeAll(c, frame(pcm16(s))) { return }  // the listener stopped playback
            }
            _ = writeAll(c, endFrame)
        } catch {
            _ = writeAll(c, errorFrame("\(error)"))
        }
    case "synth":
        do {
            guard let out = req.out else { throw SpeechError.badRequest("synth needs out") }
            var all: [Float] = []
            for piece in pieces(req.text ?? "") { all += try await engine.speak(piece, speed: speed) }
            if all.isEmpty { throw SpeechError.badRequest("nothing to say") }
            let tmp = out + ".tmp"
            try wav(pcm16(all), rate: sampleRate).write(to: URL(fileURLWithPath: tmp))
            if rename(tmp, out) != 0 { throw SpeechError.badRequest("can't write \(out)") }
            _ = writeAll(c, replyLine(["ok": true, "seconds": Double(all.count) / Double(sampleRate)]))
        } catch {
            _ = writeAll(c, replyLine(["ok": false, "error": "\(error)"]))
        }
    default:
        _ = writeAll(c, replyLine(["ok": false, "error": "unknown op \(req.op)"]))
    }
}

func serve(_ engine: Engine, path: String, idle: Double) async throws {
    let speaker = Speaker(engine: engine)
    signal(SIGPIPE, SIG_IGN)
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw SpeechError.badRequest("socket: \(errno)") }
    defer { close(fd) }
    unlink(path)
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8)
    guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else { throw SpeechError.badRequest("socket path too long") }
    withUnsafeMutableBytes(of: &addr.sun_path) { p in
        for (i, b) in bytes.enumerated() { p[i] = b }
    }
    let ok = withUnsafePointer(to: &addr) {
        $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
    }
    guard ok == 0 else { throw SpeechError.badRequest("bind \(path): \(String(cString: strerror(errno)))") }
    chmod(path, 0o600)
    defer { unlink(path) }
    listen(fd, 4)
    log("serving on \(path)")
    var last = Date()
    while Date().timeIntervalSince(last) < idle {
        var p = pollfd(fd: fd, events: Int16(POLLIN), revents: 0)
        if poll(&p, 1, 5000) <= 0 {
            // acmd stops engines by removing their socket
            if !FileManager.default.fileExists(atPath: path) { return }
            if await speaker.isSpeaking { last = Date() }
            continue
        }
        let c = accept(fd, nil, nil)
        if c < 0 { continue }
        var one: Int32 = 1
        setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        var tv = timeval(tv_sec: 30, tv_usec: 0)
        setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        // One at a time would leave stop waiting for whatever is playing.
        Task.detached {
            await handle(c, engine, speaker)
            close(c)
        }
        last = Date()
    }
    log("idle, stopping")
}

do {
    let o = try parseArgs(Array(CommandLine.arguments.dropFirst()))
    let dir = URL(fileURLWithPath: o.dir!, isDirectory: true)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let t0 = Date()
    let engine = try await Engine(dir: dir, spec: o.voice)
    // Pay for the first prediction before answering, so the first request plays
    // at once.
    _ = try await engine.samples("Ready.", speed: 1)
    log(String(format: "loaded in %.2f s (voice %@)", Date().timeIntervalSince(t0), engine.voice))
    if o.prefetch {
        print(dir.path)
        exit(0)
    }
    try await serve(engine, path: o.serve!, idle: o.idle)
} catch {
    log("error: \(error)")
    FileHandle.standardError.write(Data("acmd-speech: \(error)\n".utf8))
    exit(1)
}
