import Foundation

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
}
