// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import Contacts
import Intents
import UserNotifications

struct CommunicationNotificationIdentity: Equatable {
    let displayName: String
    let contactIdentifier: String?
    let avatarData: Data?
}

protocol CommunicationNotificationContactResolving {
    func identity(for phoneNumber: String) -> CommunicationNotificationIdentity?
}

struct SystemCommunicationNotificationContactResolver: CommunicationNotificationContactResolving {
    private let store: CNContactStore

    init(store: CNContactStore = CNContactStore()) {
        self.store = store
    }

    func identity(for phoneNumber: String) -> CommunicationNotificationIdentity? {
        let authorization = CNContactStore.authorizationStatus(for: .contacts)
        guard authorization == .authorized || authorization == .limited else { return nil }

        let keys: [CNKeyDescriptor] = [
            CNContactIdentifierKey as CNKeyDescriptor,
            CNContactFormatter.descriptorForRequiredKeys(for: .fullName),
            CNContactThumbnailImageDataKey as CNKeyDescriptor,
        ]
        guard let contact = try? store.unifiedContacts(
            matching: CNContact.predicateForContacts(matching: CNPhoneNumber(stringValue: phoneNumber)),
            keysToFetch: keys
        ).first else { return nil }

        let formattedName = CNContactFormatter.string(from: contact, style: .fullName)?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let displayName: String
        if let formattedName, !formattedName.isEmpty {
            displayName = formattedName
        } else {
            displayName = phoneNumber
        }
        return CommunicationNotificationIdentity(
            displayName: displayName,
            contactIdentifier: contact.identifier,
            avatarData: contact.thumbnailImageData
        )
    }
}

struct CommunicationNotificationContentBuilder {
    private let contacts: any CommunicationNotificationContactResolving

    init(contacts: any CommunicationNotificationContactResolving = SystemCommunicationNotificationContactResolver()) {
        self.contacts = contacts
    }

    func makeIntent(
        remoteNumber: String,
        messageBody: String,
        conversationIdentifier: String
    ) -> INSendMessageIntent {
        let identity = contacts.identity(for: remoteNumber)
        let sender = INPerson(
            personHandle: INPersonHandle(value: remoteNumber, type: .phoneNumber),
            nameComponents: nil,
            displayName: identity?.displayName ?? remoteNumber,
            image: identity?.avatarData.map(INImage.init(imageData:)),
            contactIdentifier: identity?.contactIdentifier,
            customIdentifier: remoteNumber,
            isMe: false,
            suggestionType: .none
        )
        return INSendMessageIntent(
            recipients: nil,
            outgoingMessageType: .outgoingMessageText,
            content: messageBody,
            speakableGroupName: nil,
            conversationIdentifier: conversationIdentifier,
            serviceName: "Sim Deck",
            sender: sender,
            attachments: nil
        )
    }

    func update(
        _ content: UNMutableNotificationContent,
        using intent: INSendMessageIntent,
        remoteNumber: String,
        numericID: Int,
        conversationIdentifier: String
    ) -> UNMutableNotificationContent {
        let updated = ((try? content.updating(from: intent)) as? UNMutableNotificationContent)
            ?? (content.mutableCopy() as? UNMutableNotificationContent)
            ?? content
        updated.subtitle = "\(remoteNumber) · SIM \(numericID)"
        updated.threadIdentifier = conversationIdentifier
        updated.targetContentIdentifier = conversationIdentifier
        return updated
    }
}
