// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import UIKit

enum PersonAvatarFallback: Equatable {
    case organization
    case monogram(String)
    case placeholder

    static func resolve(
        displayName: String?,
        organizationName: String?
    ) -> PersonAvatarFallback {
        if let organizationName,
           !organizationName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return .organization
        }

        guard let displayName = displayName?.trimmingCharacters(in: .whitespacesAndNewlines),
              !displayName.isEmpty,
              displayName.unicodeScalars.contains(where: CharacterSet.letters.contains) else {
            return .placeholder
        }

        let parts = displayName.split(whereSeparator: \Character.isWhitespace)
        let monogram = parts.prefix(2).compactMap(\.first)
        guard !monogram.isEmpty else { return .placeholder }
        return .monogram(String(monogram).uppercased())
    }
}

enum PersonAvatarPalette {
    static let top = UIColor(
        red: 183.0 / 255.0,
        green: 200.0 / 255.0,
        blue: 225.0 / 255.0,
        alpha: 1
    )
    static let bottom = UIColor(
        red: 143.0 / 255.0,
        green: 154.0 / 255.0,
        blue: 198.0 / 255.0,
        alpha: 1
    )
}

enum PersonAvatarImageRenderer {
    static func image(
        displayName: String?,
        organizationName: String? = nil,
        size: CGFloat = 180,
        scale: CGFloat = 1
    ) -> UIImage? {
        guard size > 0, scale > 0 else { return nil }
        let fallback = PersonAvatarFallback.resolve(
            displayName: displayName,
            organizationName: organizationName
        )
        let format = UIGraphicsImageRendererFormat()
        format.scale = scale
        format.opaque = false
        let renderer = UIGraphicsImageRenderer(size: CGSize(width: size, height: size), format: format)
        return renderer.image { context in
            let bounds = CGRect(origin: .zero, size: CGSize(width: size, height: size))
            let shape = fallback == .organization
                ? UIBezierPath(
                    roundedRect: bounds,
                    cornerRadius: size * 0.24
                )
                : UIBezierPath(ovalIn: bounds)
            context.cgContext.saveGState()
            shape.addClip()
            drawGradient(in: context.cgContext, bounds: bounds)
            context.cgContext.restoreGState()

            switch fallback {
            case .organization:
                drawSymbol("building.2.fill", in: bounds, size: size * 0.43)
            case .monogram(let monogram):
                drawMonogram(monogram, in: bounds, size: size)
            case .placeholder:
                drawSymbol("person.fill", in: bounds, size: size * 0.46)
            }
        }
    }

    static func pngData(
        displayName: String?,
        organizationName: String? = nil,
        size: CGFloat = 180
    ) -> Data? {
        image(
            displayName: displayName,
            organizationName: organizationName,
            size: size
        )?.pngData()
    }

    private static func drawGradient(in context: CGContext, bounds: CGRect) {
        guard let gradient = CGGradient(
            colorsSpace: CGColorSpaceCreateDeviceRGB(),
            colors: [PersonAvatarPalette.top.cgColor, PersonAvatarPalette.bottom.cgColor] as CFArray,
            locations: [0, 1]
        ) else { return }
        context.drawLinearGradient(
            gradient,
            start: CGPoint(x: bounds.midX, y: bounds.minY),
            end: CGPoint(x: bounds.midX, y: bounds.maxY),
            options: []
        )
    }

    private static func drawMonogram(_ monogram: String, in bounds: CGRect, size: CGFloat) {
        let baseFont = UIFont.systemFont(ofSize: size * 0.4, weight: .medium)
        let font = baseFont.fontDescriptor.withDesign(.rounded)
            .map { UIFont(descriptor: $0, size: size * 0.4) }
            ?? baseFont
        let attributes: [NSAttributedString.Key: Any] = [
            .font: font,
            .foregroundColor: UIColor.white,
        ]
        let textSize = monogram.size(withAttributes: attributes)
        monogram.draw(
            at: CGPoint(
                x: bounds.midX - textSize.width / 2,
                y: bounds.midY - textSize.height / 2
            ),
            withAttributes: attributes
        )
    }

    private static func drawSymbol(_ name: String, in bounds: CGRect, size: CGFloat) {
        let configuration = UIImage.SymbolConfiguration(pointSize: size, weight: .medium)
        guard let image = UIImage(systemName: name, withConfiguration: configuration)?
            .withTintColor(.white, renderingMode: .alwaysOriginal) else { return }
        image.draw(
            at: CGPoint(
                x: bounds.midX - image.size.width / 2,
                y: bounds.midY - image.size.height / 2
            )
        )
    }
}
