// The parts of acmd's Swift speech engine that don't need the model:
// voice blends, text splitting, the wire format and WAV files.
import Foundation

/// One voice of a blend and its share (the shares add up to 1).
public struct VoiceWeight: Equatable, Sendable {
    public let name: String
    public let weight: Float

    public init(name: String, weight: Float) {
        self.name = name
        self.weight = weight
    }
}

public enum SpeechError: Error, CustomStringConvertible, Equatable {
    case badVoice(String)
    case badRequest(String)

    public var description: String {
        switch self {
        case .badVoice(let s), .badRequest(let s): return s
        }
    }
}

/// Parses a voice SPEC: a name, or a weighted blend
/// "af_heart:0.6,af_bella:0.4" (weights are normalised; a missing weight
/// is 1). The same rules as engine.py's parse_voice.
public func parseVoice(_ spec: String) throws -> [VoiceWeight] {
    var out: [(String, Float)] = []
    for part in spec.split(separator: ",") {
        let p = part.trimmingCharacters(in: .whitespaces)
        if p.isEmpty { continue }
        let bits = p.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
        let name = bits[0]
        guard !name.isEmpty, name.utf8.allSatisfy({ (48...57).contains($0) || (97...122).contains($0) || $0 == 95 })
        else { throw SpeechError.badVoice("bad voice name \"\(name)\"") }
        var w: Float = 1
        if bits.count == 2 {
            guard let v = Float(bits[1]), v >= 0, v.isFinite else {
                throw SpeechError.badVoice("bad weight for \(name): \"\(bits[1])\"")
            }
            w = v
        }
        out.append((name, w))
    }
    if out.isEmpty { throw SpeechError.badVoice("no voice given") }
    let total = out.reduce(Float(0)) { $0 + $1.1 }
    if total <= 0 { throw SpeechError.badVoice("voice weights must add up to more than 0") }
    return out.map { VoiceWeight(name: $0.0, weight: $0.1 / total) }
}

/// Only American English voices can be used: the hosted model has the text
/// processing for them alone, and for any other language the library would go and
/// download its own from wherever it was built to look.
public func checkAvailable(_ voices: [VoiceWeight]) throws {
    for v in voices where !v.name.hasPrefix("af_") && !v.name.hasPrefix("am_") {
        throw SpeechError.badVoice(
            "can't use voice \(v.name): only American English voices (af_, am_) are available")
    }
}

/// The voice name the engine uses for a SPEC: the voice itself, or for a
/// blend a made-up name in the first voice's language ("af_acmd" + a hash
/// of the blend), so the blended pack can be stored next to real voices.
public func voiceName(for voices: [VoiceWeight]) -> String {
    if voices.count == 1 { return voices[0].name }
    var h: UInt64 = 0xcbf2_9ce4_8422_2325  // FNV-1a
    for v in voices {
        for b in "\(v.name):\(v.weight),".utf8 {
            h ^= UInt64(b)
            h = h &* 0x100_0000_01b3
        }
    }
    let prefix = voices[0].name.prefix(3)  // "af_", "bm_", …
    return prefix + "acmd" + String(h, radix: 16)
}

/// The weighted sum of voice packs (each the same length).
public func blend(_ packs: [[Float]], _ voices: [VoiceWeight]) throws -> [Float] {
    guard packs.count == voices.count, let n = packs.first?.count, packs.allSatisfy({ $0.count == n }) else {
        throw SpeechError.badVoice("voice packs differ in size")
    }
    var out = [Float](repeating: 0, count: n)
    for (pack, v) in zip(packs, voices) {
        for i in 0..<n { out[i] += pack[i] * v.weight }
    }
    return out
}

/// A request: one JSON line.
public struct Request: Decodable, Equatable {
    public var op: String
    public var text: String?
    public var out: String?
    public var speed: Float?
    /// For stop: the read to stop. Without it, whatever is playing.
    public var id: Int?

    public init(op: String, text: String? = nil, out: String? = nil, speed: Float? = nil, id: Int? = nil) {
        self.op = op
        self.text = text
        self.out = out
        self.speed = speed
        self.id = id
    }

    public static func parse(_ line: Data) throws -> Request {
        do {
            return try JSONDecoder().decode(Request.self, from: line)
        } catch {
            throw SpeechError.badRequest("bad request: \(error.localizedDescription)")
        }
    }
}

/// A JSON reply line.
public func replyLine(_ fields: [String: Any]) -> Data {
    var d = (try? JSONSerialization.data(withJSONObject: fields, options: [.sortedKeys])) ?? Data("{}".utf8)
    d.append(0x0a)
    return d
}

/// Float samples (-1…1) as 16-bit little-endian PCM.
public func pcm16(_ samples: [Float]) -> Data {
    var d = Data(count: samples.count * 2)
    d.withUnsafeMutableBytes { raw in
        let p = raw.bindMemory(to: Int16.self)
        for (i, s) in samples.enumerated() {
            let c = max(-1, min(1, s.isNaN ? 0 : s))
            p[i] = Int16(c * 32767).littleEndian
        }
    }
    return d
}

/// A stream frame: 4-byte big-endian length, then the bytes.
public func frame(_ pcm: Data) -> Data {
    var d = Data()
    var n = UInt32(pcm.count).bigEndian
    withUnsafeBytes(of: &n) { d.append(contentsOf: $0) }
    d.append(pcm)
    return d
}

/// The end of a stream.
public let endFrame = Data([0, 0, 0, 0])

/// A stream that failed after it started: 0xFFFFFFFF and an error line.
public func errorFrame(_ message: String) -> Data {
    var d = Data([0xff, 0xff, 0xff, 0xff])
    d.append(replyLine(["ok": false, "error": message]))
    return d
}

/// Splits text into the pieces synthesised one at a time: each line, then
/// each sentence, and sentences longer than maxChars at clause marks or
/// spaces. Short pieces reach the first audio sooner and stay on Kokoro
/// ANE v3's fast decoder (≤ 5 s of audio).
public func pieces(_ text: String, maxChars: Int = 160) -> [String] {
    var out: [String] = []
    for line in text.split(whereSeparator: \.isNewline) {
        for s in sentences(String(line)) {
            out.append(contentsOf: cut(s, maxChars))
        }
    }
    return out.map { $0.trimmingCharacters(in: .whitespaces) }.filter { piece in
        piece.unicodeScalars.contains { CharacterSet.alphanumerics.contains($0) }
    }
}

func sentences(_ line: String) -> [String] {
    var out: [String] = []
    var cur = ""
    let chars = Array(line)
    for (i, c) in chars.enumerated() {
        cur.append(c)
        if ".!?".contains(c), i + 1 < chars.count, chars[i + 1] == " " {
            // "e.g. " and "v0.3. " stay together when the next word is lower case
            let next = chars.dropFirst(i + 1).first { $0 != " " }
            if let n = next, n.isLowercase { continue }
            out.append(cur)
            cur = ""
        }
    }
    if !cur.trimmingCharacters(in: .whitespaces).isEmpty { out.append(cur) }
    return out
}

func cut(_ s: String, _ max: Int) -> [String] {
    var rest = s.trimmingCharacters(in: .whitespaces)
    var out: [String] = []
    while rest.count > max {
        let head = String(rest.prefix(max))
        // the last clause mark, else the last space, in the second half
        var at: String.Index?
        for marks in [[", ", "; ", ": ", " - ", " — "], [" "]] {
            for m in marks {
                if let r = head.range(of: m, options: .backwards),
                    head.distance(from: head.startIndex, to: r.lowerBound) >= max / 2
                {
                    let end = m == " " ? r.lowerBound : head.index(before: r.upperBound)
                    if at == nil || end > at! { at = end }
                }
            }
            if at != nil { break }
        }
        let split = at ?? head.endIndex
        let piece = String(rest[rest.startIndex..<split])
        out.append(piece)
        rest = String(rest[split...]).trimmingCharacters(in: .whitespaces)
    }
    if !rest.isEmpty { out.append(rest) }
    return out
}

/// A mono 16-bit PCM WAV file.
public func wav(_ pcm: Data, rate: Int) -> Data {
    var d = Data()
    func u32(_ v: UInt32) { var x = v.littleEndian; withUnsafeBytes(of: &x) { d.append(contentsOf: $0) } }
    func u16(_ v: UInt16) { var x = v.littleEndian; withUnsafeBytes(of: &x) { d.append(contentsOf: $0) } }
    d.append(contentsOf: Array("RIFF".utf8)); u32(UInt32(36 + pcm.count))
    d.append(contentsOf: Array("WAVE".utf8))
    d.append(contentsOf: Array("fmt ".utf8)); u32(16); u16(1); u16(1)
    u32(UInt32(rate)); u32(UInt32(rate * 2)); u16(2); u16(16)
    d.append(contentsOf: Array("data".utf8)); u32(UInt32(pcm.count))
    d.append(pcm)
    return d
}

/// Splits a piece in two at the space nearest its middle (one piece when
/// it has no space).
public func halves(_ s: String) -> [String] {
    let chars = Array(s)
    let mid = chars.count / 2
    var best: Int?
    for (i, c) in chars.enumerated() where c == " " {
        if best == nil || abs(i - mid) < abs(best! - mid) { best = i }
    }
    guard let at = best else { return [s] }
    let a = String(chars[..<at]).trimmingCharacters(in: .whitespaces)
    let b = String(chars[(at + 1)...]).trimmingCharacters(in: .whitespaces)
    return [a, b].filter { !$0.isEmpty }
}
