// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import SwiftUI
import UIKit

enum MessageSwipeInteraction {
    static let replyThreshold: CGFloat = 52
    static let maximumReplyOffset: CGFloat = 68
    static let maximumTimestampOffset: CGFloat = 112

    static func offset(horizontal: CGFloat, vertical: CGFloat) -> CGFloat {
        guard abs(horizontal) > abs(vertical) else { return 0 }
        if horizontal > 0 {
            return min(horizontal, maximumReplyOffset)
        }
        return max(horizontal, -maximumTimestampOffset)
    }

    static func shouldReply(horizontal: CGFloat, vertical: CGFloat) -> Bool {
        abs(horizontal) > abs(vertical) && horizontal >= replyThreshold
    }

    static func shouldBegin(horizontalVelocity: CGFloat, verticalVelocity: CGFloat) -> Bool {
        abs(horizontalVelocity) > abs(verticalVelocity)
    }
}

struct MessageTimestampRevealState: Equatable {
    private(set) var offset: CGFloat = 0

    var progress: CGFloat {
        min(max(-offset / 36, 0), 1)
    }

    mutating func update(horizontal: CGFloat, vertical: CGFloat) {
        offset = min(
            MessageSwipeInteraction.offset(
                horizontal: horizontal,
                vertical: vertical
            ),
            0
        )
    }

    mutating func end() {
        offset = 0
    }
}

struct MessageHorizontalSwipeGesture: UIGestureRecognizerRepresentable {
    let onChanged: @MainActor (CGFloat, CGFloat) -> Void
    let onEnded: @MainActor (CGFloat, CGFloat) -> Void

    func makeCoordinator(converter: CoordinateSpaceConverter) -> Coordinator {
        Coordinator(onChanged: onChanged, onEnded: onEnded)
    }

    func makeUIGestureRecognizer(context: Context) -> UIPanGestureRecognizer {
        let recognizer = UIPanGestureRecognizer()
        recognizer.delegate = context.coordinator
        recognizer.maximumNumberOfTouches = 1
        return recognizer
    }

    func updateUIGestureRecognizer(_ recognizer: UIPanGestureRecognizer, context: Context) {
        context.coordinator.onChanged = onChanged
        context.coordinator.onEnded = onEnded
    }

    func handleUIGestureRecognizerAction(_ recognizer: UIPanGestureRecognizer, context: Context) {
        let translation = recognizer.translation(in: recognizer.view)
        switch recognizer.state {
        case .began, .changed:
            context.coordinator.onChanged(translation.x, translation.y)
        case .ended, .cancelled, .failed:
            context.coordinator.onEnded(translation.x, translation.y)
        default:
            break
        }
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var onChanged: @MainActor (CGFloat, CGFloat) -> Void
        var onEnded: @MainActor (CGFloat, CGFloat) -> Void

        init(
            onChanged: @escaping @MainActor (CGFloat, CGFloat) -> Void,
            onEnded: @escaping @MainActor (CGFloat, CGFloat) -> Void
        ) {
            self.onChanged = onChanged
            self.onEnded = onEnded
        }

        func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
            guard let pan = gestureRecognizer as? UIPanGestureRecognizer else { return false }
            let velocity = pan.velocity(in: pan.view)
            return MessageSwipeInteraction.shouldBegin(
                horizontalVelocity: velocity.x,
                verticalVelocity: velocity.y
            )
        }

        func gestureRecognizer(
            _ gestureRecognizer: UIGestureRecognizer,
            shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer
        ) -> Bool {
            true
        }
    }
}
