import Foundation
import Testing

@testable import AgentCommand

private func tempDir() throws -> String {
    let dir = (NSTemporaryDirectory() as NSString).appendingPathComponent("applog-\(UUID().uuidString)")
    try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
    return dir
}

private func write(_ path: String, _ text: String, modified: Date) throws {
    try Data(text.utf8).write(to: URL(fileURLWithPath: path))
    try FileManager.default.setAttributes([.modificationDate: modified], ofItemAtPath: path)
}

@Test func theAppLogSitsBesideTheDaemonLog() {
    #expect(AppLog.path(["ACMD_STATE_DIR": "/s"]) == "/s/app.log")
}

@Test func todaysLogStaysWhereItIs() throws {
    let dir = try tempDir()
    let log = dir + "/app.log"
    let now = Date()
    try write(log, "today", modified: now)
    #expect(!AppLog.rotate(log, now: now))
    #expect(FileManager.default.fileExists(atPath: log))
}

@Test func anEarlierDaysLogTakesItsDate() throws {
    let dir = try tempDir()
    let log = dir + "/app.log"
    let now = Date()
    let yesterday = Calendar.current.date(byAdding: .day, value: -1, to: now)!
    try write(log, "yesterday", modified: yesterday)
    #expect(AppLog.rotate(log, now: now))
    #expect(!FileManager.default.fileExists(atPath: log))
    let dated = log + "." + AppLog.day(yesterday)
    #expect(try String(contentsOfFile: dated, encoding: .utf8) == "yesterday")
}

@Test func onlyTheDaysKeptAreKept() throws {
    let dir = try tempDir()
    let log = dir + "/app.log"
    let now = Date()
    func ago(_ n: Int) -> String { AppLog.day(Calendar.current.date(byAdding: .day, value: -n, to: now)!) }
    for n in 1...AppLog.days + 1 { try write(log + "." + ago(n), "", modified: now) }
    try write(dir + "/daemon.log." + ago(10), "", modified: now)
    AppLog.removeOld(log, now: now)
    let left = try FileManager.default.contentsOfDirectory(atPath: dir).sorted()
    let kept = (1..<AppLog.days).map { "app.log." + ago($0) }
    #expect(left == (kept + ["daemon.log." + ago(10)]).sorted(), "another log isn't the app's to remove")
}
