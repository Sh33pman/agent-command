// Unit checks for SpeechCore, run by `make speech-test` (swift run
// speech-core-checks). A plain executable rather than Swift Testing or
// XCTest: those need Xcode, and acmd builds with the Command Line Tools.
import Foundation
import SpeechCore

nonisolated(unsafe) var failures = 0
nonisolated(unsafe) var current = ""

func expect(_ ok: @autoclosure () throws -> Bool, _ line: Int = #line) {
    if (try? ok()) != true {
        failures += 1
        print("FAIL \(current) (line \(line))")
    }
}

func expectThrows(_ body: () throws -> Void, _ line: Int = #line) {
    do {
        try body()
        failures += 1
        print("FAIL \(current) (line \(line)): no error")
    } catch {}
}

func check(_ name: String, _ body: () throws -> Void) {
    current = name
    do { try body() } catch {
        failures += 1
        print("FAIL \(name): \(error)")
    }
}

check("parsesBlends") {
    let v = try parseVoice("af_heart:0.6, af_bella:0.4")
    expect(v.map(\.name) == ["af_heart", "af_bella"])
    expect(abs(v[0].weight - 0.6) < 1e-6 && abs(v[1].weight - 0.4) < 1e-6)
    expect(try parseVoice("af_sky") == [VoiceWeight(name: "af_sky", weight: 1)])
    let n = try parseVoice("a_x:3,b_y:1")
    expect(abs(n[0].weight - 0.75) < 1e-6)
}

check("rejectsBadVoices") {
    expectThrows { try parseVoice("") }
    expectThrows { try parseVoice("af_heart:0") }
    expectThrows { try parseVoice("af_heart:x") }
    expectThrows { try parseVoice("../etc:1") }
    expectThrows { try parseVoice("AF_Heart") }
}

check("blendNames") {
    let a = voiceName(for: try parseVoice("af_heart:0.6,af_bella:0.4"))
    let b = voiceName(for: try parseVoice("af_heart:0.5,af_bella:0.5"))
    expect(a.hasPrefix("af_acmd") && a != b)
    expect(a.utf8.allSatisfy { (48...57).contains($0) || (97...122).contains($0) || $0 == 95 })
    expect(voiceName(for: try parseVoice("bm_george")) == "bm_george")
}

check("blendsPacks") {
    let v = try parseVoice("a_x:0.75,b_y:0.25")
    expect(try blend([[4, 0], [0, 4]], v) == [3, 1])
    expectThrows { try blend([[1], [1, 2]], v) }
}

check("framesPCM") {
    let p = pcm16([0, 1, -1, 2])
    expect([UInt8](p) == [0, 0, 0xff, 0x7f, 0x01, 0x80, 0xff, 0x7f])
    expect([UInt8](frame(Data([9, 9]))) == [0, 0, 0, 2, 9, 9])
    expect([UInt8](endFrame) == [0, 0, 0, 0])
    let e = errorFrame("boom")
    expect([UInt8](e.prefix(4)) == [0xff, 0xff, 0xff, 0xff])
    expect(String(decoding: e.dropFirst(4), as: UTF8.self) == "{\"error\":\"boom\",\"ok\":false}\n")
}

check("parsesRequests") {
    let r = try Request.parse(Data(#"{"op":"stream","text":"hi","speed":1.5}"#.utf8))
    expect(r == Request(op: "stream", text: "hi", out: nil, speed: 1.5))
    expectThrows { try Request.parse(Data("nope".utf8)) }
}

check("splitsIntoPieces") {
    expect(pieces("One. Two!\nThree? Four") == ["One.", "Two!", "Three?", "Four"])
    expect(pieces("Use e.g. this. Then v0.30.1 ships.") == ["Use e.g. this.", "Then v0.30.1 ships."])
    expect(pieces("---\n\n***\nOk") == ["Ok"])
    let long = String(repeating: "word ", count: 60) + "end, " + String(repeating: "more ", count: 20)
    for p in pieces(long, maxChars: 100) { expect(p.count <= 100) }
    expect(pieces(long, maxChars: 100).joined(separator: " ").split(separator: " ").count == 81)
}

check("halvesAtTheMiddleSpace") {
    expect(halves("See SPEC.md for the design and roadmap.") == ["See SPEC.md for the", "design and roadmap."])
    expect(halves("Word") == ["Word"])
}

check("writesWAV") {
    let w = wav(Data([1, 0, 2, 0]), rate: 24000)
    expect(w.count == 48 && String(decoding: w.prefix(4), as: UTF8.self) == "RIFF")
    expect(w[24] == 0xc0 && w[25] == 0x5d)  // 24000 LE
}

if failures > 0 {
    print("\(failures) check(s) failed")
    exit(1)
}
print("speech-core-checks: all passed")
