import Foundation
import Observation

/// Looks for a newer release of the app, so the status bar can say there is one.
/// Releases are published to the Homebrew tap, tagged "agent-command-vX.Y.Z",
/// next to other releases (the speech model) that aren't the app's.
@Observable
final class UpdateChecker {
    static let shared = UpdateChecker()

    /// The newest released version, as "0.61.0", when it's newer than this one.
    private(set) var available: String?

    /// The release page of the newer version.
    var releaseURL: URL? {
        available.flatMap { URL(string: "https://github.com/brettmostert/homebrew-tap/releases/tag/\(Self.tagPrefix)\($0)") }
    }

    static let tagPrefix = "agent-command-v"
    private static let releasesURL = URL(string: "https://api.github.com/repos/brettmostert/homebrew-tap/releases?per_page=30")!
    private static let interval: TimeInterval = 6 * 60 * 60

    @ObservationIgnored private var timer: Timer?

    /// Checks now and every few hours. A development build has no version to compare.
    func start(current: String?) {
        guard let current, Self.parse(current) != nil, timer == nil else { return }
        check(current: current)
        timer = Timer.scheduledTimer(withTimeInterval: Self.interval, repeats: true) { [weak self] _ in
            self?.check(current: current)
        }
    }

    private func check(current: String) {
        var request = URLRequest(url: Self.releasesURL, timeoutInterval: 30)
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        URLSession.shared.dataTask(with: request) { [weak self] data, response, error in
            guard error == nil, (response as? HTTPURLResponse)?.statusCode == 200, let data,
                  let releases = try? JSONDecoder().decode([Release].self, from: data) else { return }
            let newer = Self.newer(than: current, in: releases)
            DispatchQueue.main.async { self?.available = newer }
        }.resume()
    }

    struct Release: Decodable {
        let tagName: String
        var draft = false
        var prerelease = false

        enum CodingKeys: String, CodingKey {
            case tagName = "tag_name", draft, prerelease
        }

        init(tagName: String, draft: Bool = false, prerelease: Bool = false) {
            self.tagName = tagName
            self.draft = draft
            self.prerelease = prerelease
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            tagName = try c.decode(String.self, forKey: .tagName)
            draft = try c.decodeIfPresent(Bool.self, forKey: .draft) ?? false
            prerelease = try c.decodeIfPresent(Bool.self, forKey: .prerelease) ?? false
        }
    }

    /// The newest of the app's published releases, when it's newer than current.
    static func newer(than current: String, in releases: [Release]) -> String? {
        guard let mine = parse(current) else { return nil }
        let best = releases
            .filter { !$0.draft && !$0.prerelease && $0.tagName.hasPrefix(tagPrefix) }
            .compactMap { r -> (String, [Int])? in
                let v = String(r.tagName.dropFirst(tagPrefix.count))
                return parse(v).map { (v, $0) }
            }
            .max { $0.1.lexicographicallyPrecedes($1.1) }
        guard let best, mine.lexicographicallyPrecedes(best.1) else { return nil }
        return best.0
    }

    /// "0.60.1" or "v0.60.1" as [0, 60, 1]. Anything else, such as "0.0.0-dev", is nil.
    static func parse(_ version: String) -> [Int]? {
        let text = version.hasPrefix("v") ? String(version.dropFirst()) : version
        let parts = text.split(separator: ".", omittingEmptySubsequences: false).map { Int($0) }
        guard parts.count == 3, parts.allSatisfy({ $0 != nil }) else { return nil }
        return parts.map { $0! }
    }
}
