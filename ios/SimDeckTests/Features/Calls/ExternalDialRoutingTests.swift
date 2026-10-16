import AVFAudio
import Foundation
import Intents
import XCTest
@testable import SimDeck

@MainActor
final class ExternalDialRoutingTests: XCTestCase {
    func testExternalDialStartsWithPreferredOnlineLine() async {
        let preferred = makeLine(numericID: 2, isOnline: true)
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [makeLine(numericID: 1, isOnline: true), preferred],
            defaultLineID: preferred.id,
            transport: transport
        )

        await model.handleExternalDial(number: "+86 135 0000 0000")

        XCTAssertEqual(transport.startedCalls.map(\.number), ["+8613500000000"])
        XCTAssertEqual(transport.startedCalls.map(\.lineID), [preferred.id])
        XCTAssertEqual(model.lastSuccessfulDialLineID, preferred.id)
        XCTAssertNil(model.pendingExternalDialRequest)
        XCTAssertEqual(model.selectedTab, .calls)
    }

    func testExternalDialUsesOnlyOnlineLineWhenPreferredLineIsOffline() async {
        let offline = makeLine(numericID: 1, isOnline: false)
        let online = makeLine(numericID: 2, isOnline: true)
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [offline, online],
            defaultLineID: offline.id,
            transport: transport
        )

        await model.handleExternalDial(number: "10086")

        XCTAssertEqual(transport.startedCalls.map(\.lineID), [online.id])
        XCTAssertNil(model.pendingExternalDialRequest)
    }

    func testExternalDialRequiresSelectionForMultipleOnlineLinesWithoutDefault() async {
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [
                makeLine(numericID: 1, isOnline: true),
                makeLine(numericID: 2, isOnline: true)
            ],
            defaultLineID: nil,
            transport: transport
        )

        await model.handleExternalDial(number: "10086")

        XCTAssertTrue(transport.startedCalls.isEmpty)
        XCTAssertEqual(model.pendingExternalDialRequest?.number, "10086")
    }

    func testExternalDialPresentsDialerWhenNoLineIsOnline() async {
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [makeLine(numericID: 1, isOnline: false)],
            defaultLineID: nil,
            transport: transport
        )

        await model.handleExternalDial(number: "10086")

        XCTAssertTrue(transport.startedCalls.isEmpty)
        XCTAssertEqual(model.pendingExternalDialRequest?.number, "10086")
    }

    func testExternalDialFailureFallsBackToPrefilledDialer() async {
        let transport = ExternalDialRecordingTransport(startError: ExternalDialTestError.failed)
        let model = makeModel(
            lines: [makeLine(numericID: 1, isOnline: true)],
            defaultLineID: nil,
            transport: transport
        )

        await model.handleExternalDial(number: "10086")

        XCTAssertEqual(transport.startedCalls.count, 1)
        XCTAssertEqual(model.pendingExternalDialRequest?.number, "10086")
        XCTAssertNil(model.callCoordinator.activeCall)
    }

    func testInvalidExternalNumberIsIgnored() async {
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [makeLine(numericID: 1, isOnline: true)],
            defaultLineID: nil,
            transport: transport
        )

        await model.handleExternalDial(number: "not a number")

        XCTAssertTrue(transport.startedCalls.isEmpty)
        XCTAssertNil(model.pendingExternalDialRequest)
    }

    func testStartCallIntentExtractsFirstValidPhoneNumber() {
        let invalidContact = makeIntentPerson(handle: "not a number")
        let phoneContact = makeIntentPerson(handle: "+86 135 0000 0000")
        let intent = INStartCallIntent(
            callRecordFilter: nil,
            callRecordToCallBack: nil,
            audioRoute: .unknown,
            destinationType: .normal,
            contacts: [invalidContact, phoneContact],
            callCapability: .audioCall
        )

        XCTAssertEqual(
            DefaultCallingRequestParser.number(from: intent),
            "+8613500000000"
        )
    }

    func testTelURLExtractsEncodedPhoneNumber() throws {
        let url = try XCTUnwrap(URL(string: "tel://%2B86%20135%200000%200000"))

        XCTAssertEqual(DefaultCallingRequestParser.number(from: url), "+8613500000000")
    }

    func testUnsupportedOrInvalidURLIsIgnored() throws {
        let webURL = try XCTUnwrap(URL(string: "https://example.com/13500000000"))
        let invalidTelURL = try XCTUnwrap(URL(string: "tel:not-a-number"))

        XCTAssertNil(DefaultCallingRequestParser.number(from: webURL))
        XCTAssertNil(DefaultCallingRequestParser.number(from: invalidTelURL))
    }

    private func makeModel(
        lines: [DeviceLineIdentity],
        defaultLineID: UUID?,
        transport: ExternalDialRecordingTransport
    ) -> AppModel {
        AppModel(
            devices: lines,
            defaultCallLineID: defaultLineID,
            callCoordinator: CallKitCoordinator(
                transport: transport,
                integratesWithSystem: false
            ),
            defaultLineStore: ExternalDialDefaultLineStore()
        )
    }

    private func makeLine(numericID: Int, isOnline: Bool) -> DeviceLineIdentity {
        DeviceLineIdentity(
            id: UUID(),
            numericID: numericID,
            deviceName: "Device \(numericID)",
            lineName: "Line \(numericID)",
            phoneNumber: "13500000000",
            isOnline: isOnline
        )
    }

    private func makeIntentPerson(handle: String) -> INPerson {
        INPerson(
            personHandle: INPersonHandle(value: handle, type: .phoneNumber),
            nameComponents: nil,
            displayName: nil,
            image: nil,
            contactIdentifier: nil,
            customIdentifier: nil
        )
    }
}

private enum ExternalDialTestError: Error {
    case failed
}

private final class ExternalDialDefaultLineStore: DefaultLineStoring {
    var callLineID: UUID?
    var messageLineID: UUID?
}

@MainActor
private final class ExternalDialRecordingTransport: CallTransport {
    struct StartedCall: Equatable {
        let callID: UUID
        let number: String
        let lineID: UUID
    }

    let capabilities = CallCapabilities.cellular
    private let startError: Error?
    private(set) var startedCalls: [StartedCall] = []

    init(startError: Error? = nil) {
        self.startError = startError
    }

    func setEventHandler(_ handler: @escaping @MainActor (CallTransportEvent) -> Void) {}
    func registerIncomingCall(_ call: IncomingCallRegistration) throws {}
    func discardIncomingCall(callID: UUID) {}

    func startOutgoingCall(callID: UUID, number: String, lineID: UUID) async throws {
        startedCalls.append(StartedCall(callID: callID, number: number, lineID: lineID))
        if let startError {
            throw startError
        }
    }

    func answerIncomingCall(callID: UUID) async throws {}
    func rejectIncomingCall(callID: UUID) async throws {}
    func endCall(callID: UUID) async throws {}
    func setHeld(_ isHeld: Bool, callID: UUID) async throws {}
    func setMuted(_ isMuted: Bool, callID: UUID) async throws {}
    func playDTMF(_ digits: String, callID: UUID) async throws {}
    func audioSessionDidActivate(_ audioSession: AVAudioSession) async {}
    func audioSessionDidDeactivate(_ audioSession: AVAudioSession) async {}
    func reset() async {}
}
