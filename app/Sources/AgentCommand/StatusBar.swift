import SwiftUI

/// The bar along the bottom of the window.
struct StatusBarView: View {
    static let height: CGFloat = 22

    private var theme: AppTheme { .shared }

    /// The version from the app's bundle, which a development build doesn't have.
    private var version: String {
        (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String).map { "v\($0)" } ?? "dev"
    }

    var body: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Color(nsColor: theme.line)).frame(height: 1)
            HStack {
                Spacer()
                Text(version)
                    .font(AppFont.ui(11))
                    .foregroundStyle(theme.dim)
                    .lineLimit(1)
            }
            .padding(.horizontal, 12)
            .frame(maxHeight: .infinity)
        }
        .frame(height: Self.height)
        .background(theme.bg)
    }
}
