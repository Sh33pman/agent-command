import AppKit
import SwiftUI
import Testing

@testable import AgentCommand

/// The Scheduled view in a live window, so SwiftUI updates it as the app would.
@MainActor @Test func closingTheFormDoesNotEndTheApp() async throws {
    try isolateFromTheRealDaemon()
    _ = NSApplication.shared
    GhosttyAppearance.locateResources()
    try #require(ghostty_init_ok(), "the theme comes from libghostty")
    let store = SessionStore()
    let model = ScheduledModel(store: store)
    model.checkDelay = 0
    let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1000, height: 800), styleMask: [.titled], backing: .buffered, defer: false)
    window.isReleasedWhenClosed = false // Swift releases it: AppKit releasing it too would crash a later test
    window.contentView = NSHostingView(rootView: ScheduledView(model: model))
    window.orderFront(nil)
    defer { window.close() }
    func settle() async throws { try await Task.sleep(for: .milliseconds(300)) }

    // Saving a first job closes the form by setting the draft to nil, as cancelling
    // does. A binding that unwraps the draft ended the app there (0.47.0).
    model.newJob()
    model.draft?.name = "First"
    model.draft?.prompt = "# Hello"
    try await settle()
    model.cancel(force: true)
    try await settle()
    #expect(!model.isEditing)

    model.newJob()
    try await settle()
    model.draft = nil
    try await settle()
    #expect(model.draft == nil)
}
