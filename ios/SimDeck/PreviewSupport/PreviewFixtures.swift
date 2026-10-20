// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

#if DEBUG
import Foundation

enum PreviewFixtures {
    static let now = Date(timeIntervalSince1970: 1_797_000_000)

    static let devices = [
        DeviceLineIdentity(
            id: UUID(uuidString: "00000000-0000-0000-0000-000000000001")!,
            deviceID: UUID(uuidString: "00000000-0000-0000-0000-000000000001")!,
            slotID: UUID(uuidString: "00000000-0000-0000-0000-000000000101")!,
            simID: UUID(uuidString: "30000000-0000-0000-0000-000000000001")!,
            numericID: 1,
            slotKey: "slot-1",
            slotIndex: 1,
            deviceName: "Office EC20",
            lineName: "Work",
            phoneNumber: "13500000000",
            isOnline: true,
            radioStatus: CellularRadioStatus(technology: .lte, signalBars: 3),
            commandPublicKey: nil
        ),
        DeviceLineIdentity(
            id: UUID(uuidString: "00000000-0000-0000-0000-000000000002")!,
            deviceID: UUID(uuidString: "00000000-0000-0000-0000-000000000002")!,
            slotID: UUID(uuidString: "00000000-0000-0000-0000-000000000102")!,
            simID: UUID(uuidString: "30000000-0000-0000-0000-000000000002")!,
            numericID: 2,
            slotKey: "slot-1",
            slotIndex: 1,
            deviceName: "Bedroom EC20",
            lineName: "Backup",
            phoneNumber: "13800000000",
            isOnline: true,
            radioStatus: CellularRadioStatus(technology: .nrNSA, signalBars: 4),
            commandPublicKey: nil
        ),
        DeviceLineIdentity(
            id: UUID(uuidString: "00000000-0000-0000-0000-000000000003")!,
            deviceID: UUID(uuidString: "00000000-0000-0000-0000-000000000003")!,
            slotID: UUID(uuidString: "00000000-0000-0000-0000-000000000103")!,
            simID: UUID(uuidString: "30000000-0000-0000-0000-000000000003")!,
            numericID: 3,
            slotKey: "slot-1",
            slotIndex: 1,
            deviceName: "Backup modem with a deliberately long management name",
            lineName: "Travel",
            phoneNumber: "",
            isOnline: false,
            commandPublicKey: nil
        )
    ]

    static let deviceManagementDevices = [
        DeviceLineIdentity(
            id: UUID(uuidString: "40000000-0000-0000-0000-000000000001")!,
            deviceID: UUID(uuidString: "40000000-0000-0000-0000-000000000000")!,
            slotID: UUID(uuidString: "40000000-0000-0000-0000-000000000011")!,
            simID: UUID(uuidString: "40000000-0000-0000-0000-000000000021")!,
            numericID: 2,
            slotKey: "slot-a",
            slotIndex: 1,
            deviceName: "Living Room Modem",
            lineName: "Home",
            phoneNumber: "13500000000",
            isOnline: true,
            radioStatus: CellularRadioStatus(technology: .lte, signalBars: 3),
            commandPublicKey: nil
        ),
        DeviceLineIdentity(
            id: UUID(uuidString: "40000000-0000-0000-0000-000000000012")!,
            deviceID: UUID(uuidString: "40000000-0000-0000-0000-000000000000")!,
            slotID: UUID(uuidString: "40000000-0000-0000-0000-000000000012")!,
            simID: nil,
            numericID: 3,
            slotKey: "slot-b",
            slotIndex: 2,
            deviceName: "Living Room Modem",
            lineName: "",
            phoneNumber: "",
            isOnline: true,
            radioStatus: CellularRadioStatus(technology: .lte, signalBars: 2),
            commandPublicKey: nil
        ),
        DeviceLineIdentity(
            id: UUID(uuidString: "40000000-0000-0000-0000-000000000002")!,
            deviceID: UUID(uuidString: "40000000-0000-0000-0000-000000000100")!,
            slotID: UUID(uuidString: "40000000-0000-0000-0000-000000000013")!,
            simID: UUID(uuidString: "40000000-0000-0000-0000-000000000022")!,
            numericID: 4,
            slotKey: "slot-a",
            slotIndex: 1,
            deviceName: "Backup Modem",
            lineName: "Travel",
            phoneNumber: "13800000000",
            isOnline: false,
            radioStatus: nil,
            commandPublicKey: nil
        )
    ]

    static let managedSIMs = [
        ManagedSIM(id: UUID(uuidString: "30000000-0000-0000-0000-000000000001")!, numericID: 1, assignedDeviceID: devices[0].deviceID, assignedSlotID: devices[0].slotID, lastSeenAt: now, lineName: "Work", phoneNumber: "13500000000"),
        ManagedSIM(id: UUID(uuidString: "30000000-0000-0000-0000-000000000002")!, numericID: 2, assignedDeviceID: devices[1].deviceID, assignedSlotID: devices[1].slotID, lastSeenAt: now, lineName: "Backup", phoneNumber: "13800000000"),
        ManagedSIM(id: UUID(uuidString: "30000000-0000-0000-0000-000000000003")!, numericID: 3, assignedDeviceID: devices[2].deviceID, assignedSlotID: devices[2].slotID, lastSeenAt: now.addingTimeInterval(-3_600), lineName: "Travel", phoneNumber: ""),
        ManagedSIM(id: UUID(uuidString: "30000000-0000-0000-0000-000000000004")!, numericID: 4, assignedDeviceID: nil, assignedSlotID: nil, lastSeenAt: now.addingTimeInterval(-86_400), lineName: "Spare", phoneNumber: "")
    ]

    static let calls = [
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000001")!,
            lineID: devices[0].id,
            lineSnapshot: LineHistorySnapshot(line: devices[0]),
            remoteNumber: "13800000000",
            contactName: "China Mobile",
            direction: .incoming,
            result: .answered,
            startedAt: now.addingTimeInterval(-420),
            endedAt: now.addingTimeInterval(-301)
        ),
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000002")!,
            lineID: devices[1].id,
            lineSnapshot: LineHistorySnapshot(line: devices[1]),
            remoteNumber: "13500000000",
            contactName: nil,
            direction: .incoming,
            result: .missed,
            startedAt: now.addingTimeInterval(-86_400),
            endedAt: nil
        ),
        CallRecord(
            id: UUID(uuidString: "10000000-0000-0000-0000-000000000003")!,
            lineID: devices[0].id,
            lineSnapshot: LineHistorySnapshot(line: devices[0]),
            remoteNumber: "13800000000",
            contactName: "Service Desk",
            direction: .outgoing,
            result: .answered,
            startedAt: now.addingTimeInterval(-172_800),
            endedAt: now.addingTimeInterval(-172_134)
        )
    ]

    static let activeCall = ActiveCallSession(
        id: UUID(uuidString: "30000000-0000-0000-0000-000000000099")!,
        lineID: devices[0].id,
        remoteNumber: "13500000000",
        displayName: nil,
        status: .active(connectedAt: now.addingTimeInterval(-6)),
        isMuted: false
    )

    static let conversations = [
        MessageConversation(
            lineID: devices[0].id,
            lineSnapshot: LineHistorySnapshot(line: devices[0]),
            remoteNumber: "13800000000",
            contactName: "Alex",
            unreadCount: 2,
            messages: [
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000001")!,
                    direction: .received,
                    body: "Are you free for dinner?",
                    attachment: nil,
                    sentAt: now.addingTimeInterval(-600),
                    status: .delivered
                ),
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000002")!,
                    direction: .sent,
                    body: "Yes, see you at seven.",
                    attachment: nil,
                    sentAt: now.addingTimeInterval(-480),
                    status: .delivered
                ),
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000003")!,
                    direction: .received,
                    body: "This is the place.",
                    attachment: MessageImageAttachment(
                        id: UUID(uuidString: "21000000-0000-0000-0000-000000000001")!,
                        systemImageName: "fork.knife"
                    ),
                    sentAt: now.addingTimeInterval(-300),
                    status: .delivered
                )
            ]
        ),
        MessageConversation(
            lineID: devices[1].id,
            lineSnapshot: LineHistorySnapshot(line: devices[1]),
            remoteNumber: "13800000000",
            contactName: "Alex",
            unreadCount: 0,
            messages: [
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000004")!,
                    direction: .sent,
                    body: "This thread uses another SIM.",
                    attachment: nil,
                    sentAt: now.addingTimeInterval(-86_400),
                    status: .sent
                )
            ]
        ),
        MessageConversation(
            lineID: devices[0].id,
            remoteNumber: "+861069100979",
            contactName: nil,
            unreadCount: 0,
            messages: [
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000005")!,
                    direction: .received,
                    body: "Your reservation has been confirmed.",
                    attachment: nil,
                    sentAt: now.addingTimeInterval(-172_800),
                    status: .delivered
                )
            ]
        ),
        MessageConversation(
            lineID: devices[1].id,
            remoteNumber: "+8619945168684",
            contactName: nil,
            unreadCount: 1,
            messages: [
                MessageItem(
                    id: UUID(uuidString: "20000000-0000-0000-0000-000000000006")!,
                    direction: .received,
                    body: "Sorry, I called the wrong number.",
                    attachment: nil,
                    sentAt: now.addingTimeInterval(-259_200),
                    status: .delivered
                )
            ]
        )
    ]
}
#endif
