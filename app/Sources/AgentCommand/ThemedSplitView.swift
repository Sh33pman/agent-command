import AppKit

/// A split view whose divider is a thin line in the theme's colors.
final class ThemedSplitView: NSSplitView {
    override var dividerColor: NSColor { AppTheme.shared.line }
    override var dividerThickness: CGFloat { 1 }
    // The thin style has no grab handle drawn on the line.
    override var dividerStyle: NSSplitView.DividerStyle {
        get { .thin }
        set {}
    }
}
