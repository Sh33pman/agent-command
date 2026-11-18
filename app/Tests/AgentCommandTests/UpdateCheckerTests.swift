import Foundation
import Testing

@testable import AgentCommand

@Test func anUpdateIsTheNewestAppRelease() {
    let releases: [UpdateChecker.Release] = [
        .init(tagName: "speech-model-v1"),
        .init(tagName: "agent-command-v0.60.1"),
        .init(tagName: "agent-command-v0.9.0"),
        .init(tagName: "agent-command-v0.61.0", draft: true),
        .init(tagName: "agent-command-v0.62.0", prerelease: true),
    ]
    #expect(UpdateChecker.newer(than: "0.60.0", in: releases) == "0.60.1")
    #expect(UpdateChecker.newer(than: "0.9.0", in: releases) == "0.60.1", "compared as numbers, not text")
    #expect(UpdateChecker.newer(than: "0.60.1", in: releases) == nil, "up to date")
    #expect(UpdateChecker.newer(than: "0.70.0", in: releases) == nil, "ahead of the releases")
    #expect(UpdateChecker.newer(than: "dev", in: releases) == nil)
    #expect(UpdateChecker.newer(than: "0.60.0", in: []) == nil)
}

@Test func aVersionParsesOnlyAsThreeNumbers() {
    #expect(UpdateChecker.parse("0.60.1") == [0, 60, 1])
    #expect(UpdateChecker.parse("v1.2.3") == [1, 2, 3])
    #expect(UpdateChecker.parse("0.0.0-dev") == nil)
    #expect(UpdateChecker.parse("1.2") == nil)
}

@Test func releasesDecodeFromGitHub() throws {
    let json = #"[{"tag_name":"agent-command-v0.60.1","draft":false,"prerelease":false,"name":"x"},{"tag_name":"speech-model-v1"}]"#
    let releases = try JSONDecoder().decode([UpdateChecker.Release].self, from: Data(json.utf8))
    #expect(releases.map(\.tagName) == ["agent-command-v0.60.1", "speech-model-v1"])
}
