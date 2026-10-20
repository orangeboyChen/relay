// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//
//  NotificationService.swift
//  SMSNotificationService
//
//  Created by orangeboy Chen on 2026/10/19.
//

import Intents
import OSLog
import UserNotifications

class NotificationService: UNNotificationServiceExtension {
    private let logger = Logger(subsystem: "com.nowcent.simdeck", category: "SMSNotificationService")
    private let communicationBuilder = CommunicationNotificationContentBuilder()
    private let deliveryLock = NSLock()
    private var contentHandler: ((UNNotificationContent) -> Void)?
    private var bestAttemptContent: UNMutableNotificationContent?

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        self.contentHandler = contentHandler
        bestAttemptContent = (request.content.mutableCopy() as? UNMutableNotificationContent)
        
        guard let bestAttemptContent else { return deliver(request.content) }
        guard let (payload, showPreview) = NotificationPayloadDecoder.open(userInfo: request.content.userInfo) else {
            logger.error("Unable to authenticate or decrypt incoming SMS notification")
            return deliver(bestAttemptContent)
        }
        let visibleBody = showPreview
            ? payload.body
            : NSLocalizedString("New message", bundle: .main, comment: "Generic SMS notification body")
        bestAttemptContent.title = payload.remoteNumber
        bestAttemptContent.body = visibleBody
        let conversationIdentifier = MessageNotificationConversationIdentifier.make(
            simID: payload.simID,
            slotID: payload.slotID,
            deviceUUID: payload.deviceUUID,
            remoteNumber: payload.remoteNumber
        )
        let intent = communicationBuilder.makeIntent(
            remoteNumber: payload.remoteNumber,
            messageBody: visibleBody,
            conversationIdentifier: conversationIdentifier
        )
        let verificationCode = VerificationCodeDetector.explicitCode(in: payload.body)
        if let verificationCode {
            NotificationOTPStore.append(
                code: verificationCode,
                remoteNumber: payload.remoteNumber,
                numericID: payload.numericID
            )
        }

        let interaction = INInteraction(intent: intent, response: nil)
        interaction.direction = .incoming
        interaction.donate { [weak self] error in
            guard let self else { return }
            if let error {
                logger.error("Unable to donate incoming SMS interaction: \(error.localizedDescription, privacy: .public)")
            }
            let communicationContent = communicationBuilder.update(
                bestAttemptContent,
                using: intent,
                remoteNumber: payload.remoteNumber,
                numericID: payload.numericID,
                conversationIdentifier: conversationIdentifier
            )
            if let verificationCode {
                var userInfo = communicationContent.userInfo
                userInfo[NotificationPayloadDecoder.otpKey] = verificationCode
                communicationContent.userInfo = userInfo
                communicationContent.categoryIdentifier = "simdeck.sms.otp"
            }
            self.bestAttemptContent = communicationContent
            deliver(communicationContent)
        }
    }
    
    override func serviceExtensionTimeWillExpire() {
        guard let bestAttemptContent else { return }
        deliver(bestAttemptContent)
    }

    private func deliver(_ content: UNNotificationContent) {
        deliveryLock.lock()
        let contentHandler = self.contentHandler
        self.contentHandler = nil
        deliveryLock.unlock()
        guard let contentHandler else { return }
        contentHandler(content)
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
