// Draws the app icon: a terminal window split into two panes, with a prompt.
//   swift scripts/make-icon.swift <output.png>
// scripts/make-icon.sh turns it into packaging/AppIcon.icns.
import AppKit

let size: CGFloat = 1024
let image = NSImage(size: NSSize(width: size, height: size))
image.lockFocus()

func color(_ hex: UInt32) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 255) / 255, green: CGFloat((hex >> 8) & 255) / 255,
            blue: CGFloat(hex & 255) / 255, alpha: 1)
}

// macOS's icon grid: the shape sits inside a transparent margin.
let body = NSRect(x: 100, y: 100, width: 824, height: 824)
let shape = NSBezierPath(roundedRect: body, xRadius: 185, yRadius: 185)
color(0x282C34).setFill()
shape.fill()
shape.addClip()

// Title bar.
color(0x21252B).setFill()
NSRect(x: 100, y: 800, width: 824, height: 124).fill()
for (index, dot) in [UInt32(0xE06C75), 0xE5C07B, 0x98C379].enumerated() {
    color(dot).setFill()
    NSBezierPath(ovalIn: NSRect(x: 170 + CGFloat(index) * 66, y: 840, width: 44, height: 44)).fill()
}

// The split between panes.
color(0x4B5263).setFill()
NSRect(x: 632, y: 100, width: 12, height: 700).fill()

// The prompt, drawn so it needs no font.
let chevron = NSBezierPath()
chevron.lineWidth = 62
chevron.lineCapStyle = .round
chevron.lineJoinStyle = .round
chevron.move(to: NSPoint(x: 205, y: 590))
chevron.line(to: NSPoint(x: 355, y: 470))
chevron.line(to: NSPoint(x: 205, y: 350))
color(0xE5C07B).setStroke()
chevron.stroke()

color(0xABB2BF).setFill()
NSBezierPath(roundedRect: NSRect(x: 395, y: 325, width: 170, height: 58), xRadius: 14, yRadius: 14).fill()

// The other pane's status lines.
for (index, line) in [UInt32(0x98C379), 0x61AFEF, 0x5C6370].enumerated() {
    color(line).setFill()
    NSBezierPath(roundedRect: NSRect(x: 696, y: 650 - CGFloat(index) * 100, width: 170, height: 44),
                 xRadius: 12, yRadius: 12).fill()
}

image.unlockFocus()
let tiff = image.tiffRepresentation!
let png = NSBitmapImageRep(data: tiff)!.representation(using: .png, properties: [:])!
try! png.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
