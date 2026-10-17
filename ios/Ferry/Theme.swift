import SwiftUI
import UIKit

/// Ferry's colours: warm paper and ink, with a life-ring orange. The same as its web page.
enum Theme {
    static let bg = dynamic(light: 0xF3F1EC, dark: 0x111214)
    static let surface = dynamic(light: 0xFFFFFF, dark: 0x1B1C1F)
    static let surface2 = dynamic(light: 0xE9E6DF, dark: 0x26272B)
    static let ink = dynamic(light: 0x16171A, dark: 0xF3F2EF)
    static let ink2 = dynamic(light: 0x5E6168, dark: 0xA3A6AD)
    static let ink3 = dynamic(light: 0x95979D, dark: 0x6D7077)
    static let accent = dynamic(light: 0xEE5229, dark: 0xFF6B45)
    static let ok = dynamic(light: 0x1E9A54, dark: 0x3DD68C)
    static let danger = dynamic(light: 0xD93A2B, dark: 0xFF5F52)

    private static func dynamic(light: UInt32, dark: UInt32) -> Color {
        Color(UIColor { $0.userInterfaceStyle == .dark ? UIColor(hex: dark) : UIColor(hex: light) })
    }
}

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}

/// The ink button: Send, Connect.
struct InkButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .foregroundStyle(Theme.bg)
            .frame(maxWidth: .infinity, minHeight: 54)
            .background(Theme.ink, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .opacity(configuration.isPressed ? 0.85 : 1)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

/// The quieter button beside it.
struct SoftButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 17, weight: .semibold))
            .foregroundStyle(Theme.ink)
            .frame(maxWidth: .infinity, minHeight: 54)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Theme.ink.opacity(0.08)))
            .opacity(configuration.isPressed ? 0.85 : 1)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

extension FerryFile {
    /// "Today", "Yesterday", "Monday 13 October".
    static func dayLabel(_ date: Date, now: Date = .now) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(date) { return "Today" }
        if cal.isDateInYesterday(date) { return "Yesterday" }
        var style = Date.FormatStyle().weekday(.wide).day().month(.wide)
        if !cal.isDate(date, equalTo: now, toGranularity: .year) { style = style.year() }
        return date.formatted(style)
    }

    var sizeLabel: String { ByteCountFormatter.string(fromByteCount: size, countStyle: .file) }
}

/// "1 photo", "3 videos", "2 files" (mixed kinds are files).
func describe(_ kinds: [String]) -> String {
    let one = ["image": "photo", "video": "video"][kinds.first ?? ""] ?? "file"
    let word = Set(kinds).count == 1 ? one : "file"
    return "\(kinds.count) \(word)\(kinds.count == 1 ? "" : "s")"
}
