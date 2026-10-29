// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Foundation
import Observation
import SwiftUI
import UIKit

enum MessageSwipeInteraction {
    static let maximumTimestampOffset: CGFloat = 72

    static func offset(horizontal: CGFloat, vertical: CGFloat) -> CGFloat {
        guard horizontal < 0, abs(horizontal) > abs(vertical) else { return 0 }
        return max(horizontal, -maximumTimestampOffset)
    }

    static func shouldBegin(horizontalVelocity: CGFloat, verticalVelocity: CGFloat) -> Bool {
        horizontalVelocity < 0 && abs(horizontalVelocity) > abs(verticalVelocity)
    }
}

struct MessageTimestampRevealState: Equatable {
    private(set) var offset: CGFloat = 0

    var progress: CGFloat {
        min(max(-offset / 28, 0), 1)
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

@MainActor
@Observable
final class MessageTimestampRevealModel {
    private(set) var progress: CGFloat
    private(set) var horizontalOffset: CGFloat = 0

    init(progress: CGFloat = 0) {
        self.progress = progress
    }

    func update(horizontal: CGFloat, vertical: CGFloat) {
        let offset = MessageSwipeInteraction.offset(
            horizontal: horizontal,
            vertical: vertical
        )
        horizontalOffset = offset
        progress = min(max(-offset / 28, 0), 1)
    }

    func end() {
        progress = 0
        horizontalOffset = 0
    }
}

struct MessageHorizontalSwipeGesture: UIViewRepresentable {
    let onChanged: @MainActor (CGFloat, CGFloat) -> Void
    let onEnded: @MainActor (CGFloat, CGFloat) -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(onChanged: onChanged, onEnded: onEnded)
    }

    func makeUIView(context: Context) -> GestureAttachmentView {
        let view = GestureAttachmentView()
        view.isUserInteractionEnabled = false
        view.didMoveToSuperviewAction = { [weak coordinator = context.coordinator] view in
            coordinator?.attach(to: view.superview)
        }
        return view
    }

    func updateUIView(_ view: GestureAttachmentView, context: Context) {
        context.coordinator.onChanged = onChanged
        context.coordinator.onEnded = onEnded
        context.coordinator.attach(to: view.superview)
    }

    static func dismantleUIView(_ view: GestureAttachmentView, coordinator: Coordinator) {
        view.didMoveToSuperviewAction = nil
        coordinator.detach()
    }

    @MainActor
    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var onChanged: @MainActor (CGFloat, CGFloat) -> Void
        var onEnded: @MainActor (CGFloat, CGFloat) -> Void
        private let recognizer = UIPanGestureRecognizer()
        private weak var attachedScrollView: UIScrollView?

        init(
            onChanged: @escaping @MainActor (CGFloat, CGFloat) -> Void,
            onEnded: @escaping @MainActor (CGFloat, CGFloat) -> Void
        ) {
            self.onChanged = onChanged
            self.onEnded = onEnded
            super.init()
            recognizer.addTarget(self, action: #selector(handleGesture(_:)))
            recognizer.delegate = self
            recognizer.maximumNumberOfTouches = 1
        }

        func attach(to view: UIView?) {
            guard let scrollView = view?.firstAncestor(of: UIScrollView.self) else { return }
            guard attachedScrollView !== scrollView else { return }
            detach()
            scrollView.addGestureRecognizer(recognizer)
            attachedScrollView = scrollView
        }

        func detach() {
            attachedScrollView?.removeGestureRecognizer(recognizer)
            attachedScrollView = nil
        }

        @objc private func handleGesture(_ recognizer: UIPanGestureRecognizer) {
            let translation = recognizer.translation(in: recognizer.view)
            switch recognizer.state {
            case .began, .changed:
                onChanged(translation.x, translation.y)
            case .ended, .cancelled, .failed:
                onEnded(translation.x, translation.y)
            default:
                break
            }
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

    final class GestureAttachmentView: UIView {
        var didMoveToSuperviewAction: ((GestureAttachmentView) -> Void)?

        override func didMoveToSuperview() {
            super.didMoveToSuperview()
            didMoveToSuperviewAction?(self)
        }

        override func didMoveToWindow() {
            super.didMoveToWindow()
            didMoveToSuperviewAction?(self)
        }
    }
}

private extension UIView {
    func firstAncestor<T: UIView>(of type: T.Type) -> T? {
        var candidate = superview
        while let view = candidate {
            if let match = view as? T {
                return match
            }
            candidate = view.superview
        }
        return nil
    }
}
