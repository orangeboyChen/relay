//
//  NotificationService.swift
//  SMSNotificationService
//
//  Created by orangeboy Chen on 2026/10/19.
//

import UserNotifications

class NotificationService: UNNotificationServiceExtension {

    var contentHandler: ((UNNotificationContent) -> Void)?
    var bestAttemptContent: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        self.contentHandler = contentHandler
        bestAttemptContent = (request.content.mutableCopy() as? UNMutableNotificationContent)
        
        guard let bestAttemptContent else { return contentHandler(request.content) }
        guard let (payload, showPreview) = NotificationPayloadDecoder.open(userInfo: request.content.userInfo) else {
            return contentHandler(bestAttemptContent)
        }
        bestAttemptContent.title = payload.remoteNumber
        bestAttemptContent.subtitle = "SIM \(payload.numericID)"
        bestAttemptContent.body = showPreview
            ? payload.body
            : NSLocalizedString("New message", bundle: .main, comment: "Generic SMS notification body")
        if let code = VerificationCodeDetector.explicitCode(in: payload.body) {
            NotificationOTPStore.append(
                code: code,
                remoteNumber: payload.remoteNumber,
                numericID: payload.numericID
            )
            var userInfo = bestAttemptContent.userInfo
            userInfo[NotificationPayloadDecoder.otpKey] = code
            bestAttemptContent.userInfo = userInfo
            bestAttemptContent.categoryIdentifier = "simdeck.sms.otp"
        }
        contentHandler(bestAttemptContent)
    }
    
    override func serviceExtensionTimeWillExpire() {
        // Called just before the extension will be terminated by the system.
        // Use this as an opportunity to deliver your "best attempt" at modified content, otherwise the original push payload will be used.
        if let contentHandler = contentHandler, let bestAttemptContent =  bestAttemptContent {
            contentHandler(bestAttemptContent)
        }
    }

}

private enum VerificationCodeDetector {
    private static let pattern = #"(?i)(?:验证码|verification\s*code|security\s*code|one[- ]?time\s*password|otp|code)\D{0,8}(\d{4,8})|\b(\d{4,8})\b"#
    private static let explicitPattern = #"(?i)(?:验证码|verification\s*code|security\s*code|one[- ]?time\s*password|otp)\D{0,8}(\d{4,8})"#

    static func code(in message: String) -> String? {
        guard let expression = try? NSRegularExpression(pattern: explicitPattern) else { return nil }
        let range = NSRange(message.startIndex..<message.endIndex, in: message)
        for match in expression.matches(in: message, range: range) {
            for index in 1..<match.numberOfRanges {
                let candidateRange = match.range(at: index)
                guard candidateRange.location != NSNotFound,
                      let swiftRange = Range(candidateRange, in: message) else { continue }
                return String(message[swiftRange])
            }
        }
        return nil
    }

    static func explicitCode(in message: String) -> String? {
        code(in: message)
    }
}
