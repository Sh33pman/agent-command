// acmd-speech: acmd's speech engine (SPEC §8.9). It speaks the protocol
// acmd's Python engine used before v0.32, so internal/speech talks to it
// unchanged:
//
//   acmd-speech --prefetch --dir DIR --voice SPEC
//       download the model and voices into DIR, compile them, exit
//   acmd-speech --serve SOCKET --dir DIR --voice SPEC [--idle SECONDS]
//       load once and answer requests on a unix socket until idle
//
// A request is one JSON line: {"op": "stream", "text": ..., "speed": 1.0},
// {"op": "synth", "text": ..., "out": WAV path, "speed": 1.0} or
// {"op": "ping"}. The reply is one JSON line {"ok": true, ...} or
// {"ok": false, "error": ...}; after the ok of a stream come frames of
// 16-bit little-endian mono PCM at "rate" Hz (4-byte big-endian length +
// data), ended by a 0 length (or 0xFFFFFFFF and an error line). Closing
// the connection stops the stream.
//
// The model is Kokoro-82M (https://huggingface.co/hexgrad/Kokoro-82M) on
// Core ML through FluidAudio's Kokoro ANE v3.
import Darwin
import FluidAudio
import Foundation
import SpeechCore

let sampleRate = 24_000

struct Options {
    var prefetch = false
    var serve: String?
    var dir: String?
    var voice = "af_heart:0.6,af_bella:0.4"
    var idle: Double = 600
    var phonemes: String?  // debugging: print the phonemes of each piece
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
        case "--phonemes": o.phonemes = try value(a)
        case "--serve": o.serve = try value(a)
        case "--dir": o.dir = try value(a)
        case "--voice": o.voice = try value(a)
        case "--model": _ = try value(a)  // accepted and ignored (the old engine took one); the model is fixed
        case "--idle":
            guard let v = Double(try value(a)), v > 0 else { throw SpeechError.badRequest("bad --idle") }
            o.idle = v
        case "--version": print("acmd-speech (FluidAudio 0.18.2, Kokoro ANE v3)"); exit(0)
        default: throw SpeechError.badRequest("unknown argument \(a)")
        }
    }
    if o.dir == nil { throw SpeechError.badRequest("--dir is required") }
    if !o.prefetch && o.serve == nil && o.phonemes == nil {
        throw SpeechError.badRequest("--prefetch, --serve or --phonemes")
    }
    return o
}

func log(_ s: String) {
    FileHandle.standardError.write(Data("\(ISO8601DateFormatter().string(from: Date())) \(s)\n".utf8))
}

/// The model, loaded once, with the voice (or blend) in use.
final class Engine {
    let manager: KokoroAneManager
    let voice: String
    let dir: URL

    init(dir: URL, spec: String) async throws {
        let voices = try parseVoice(spec)
        self.dir = dir
        manager = KokoroAneManager(variant: .english, directory: dir, version: .v3)
        try await manager.initialize(preloadVoices: Set(voices.map(\.name)))
        voice = voiceName(for: voices)
        if voices.count > 1 { try writeBlend(voices) }
    }

    /// Stores the weighted sum of the voices as a voice of its own, next to
    /// the downloaded ones, where FluidAudio looks for voices.
    func writeBlend(_ voices: [VoiceWeight]) throws {
        var packs: [[Float]] = []
        var root: URL?
        for v in voices {
            guard let url = findVoice(v.name) else { throw SpeechError.badVoice("voice \(v.name) wasn't downloaded") }
            if root == nil {
                let parent = url.deletingLastPathComponent()
                root = parent.lastPathComponent == "voices" ? parent.deletingLastPathComponent() : parent
            }
            packs.append(try KokoroAneVoicePack.load(from: url).storage)
        }
        let out = root!.appendingPathComponent("voices/\(voice).bin")
        try FileManager.default.createDirectory(at: out.deletingLastPathComponent(), withIntermediateDirectories: true)
        try KokoroAneVoicePack(storage: try blend(packs, voices)).binaryData.write(to: out, options: .atomic)
    }

    func findVoice(_ name: String) -> URL? {
        let e = FileManager.default.enumerator(at: dir, includingPropertiesForKeys: nil)
        var found: URL?
        while let u = e?.nextObject() as? URL {
            if u.lastPathComponent == "\(name).bin" && u.path.contains("ANE-v3") {
                found = u
                if u.deletingLastPathComponent().lastPathComponent != "voices" { break }
            }
        }
        return found
    }

    func samples(_ text: String, speed: Float) async throws -> [Float] {
        try await manager.synthesizeDetailed(text: text, voice: voice, speed: speed).samples
    }

    /// One piece of a request. On macOS 26.6 Core ML fails some inputs
    /// for good after their first run ("Tile: Shape deduction failed",
    /// a BNNS op in the alignment stage, cached per input length), so a
    /// piece that fails is said in two halves (other lengths) instead; a
    /// half that still fails is skipped and logged rather than ending the
    /// whole reply.
    func speak(_ text: String, speed: Float) async throws -> [Float] {
        do {
            return try await samples(text, speed: speed)
        } catch {
            log("piece failed, saying it in two halves: \(text.debugDescription): \(error)")
        }
        var out: [Float] = []
        for half in halves(text) {
            do {
                out += try await samples(half, speed: speed)
            } catch {
                log("half failed, skipped: \(half.debugDescription): \(error)")
            }
        }
        return out
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

func handle(_ c: Int32, _ engine: Engine) async {
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
            continue
        }
        let c = accept(fd, nil, nil)
        if c < 0 { continue }
        var one: Int32 = 1
        setsockopt(c, SOL_SOCKET, SO_NOSIGPIPE, &one, socklen_t(MemoryLayout<Int32>.size))
        var tv = timeval(tv_sec: 30, tv_usec: 0)
        setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        await handle(c, engine)
        close(c)
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
    // The first prediction is slow (Core ML warms up): pay it before
    // answering, so the first request plays at once.
    _ = try await engine.samples("Ready.", speed: 1)
    log(String(format: "loaded in %.2f s (voice %@)", Date().timeIntervalSince(t0), engine.voice))
    if let text = o.phonemes {
        for p in pieces(text) {
            print("\(p)\n  \(try await engine.manager.phonemes(for: p))")
            for _ in 0..<3 {
                let n = try await engine.speak(p, speed: 1).count
                print(String(format: "  %.2f s of audio", Double(n) / Double(sampleRate)))
            }
        }
        exit(0)
    }
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
