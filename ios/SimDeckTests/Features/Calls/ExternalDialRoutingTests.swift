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

        let request = DefaultCallingRequestParser.request(from: intent)

        XCTAssertEqual(request?.number, "+8613500000000")
        XCTAssertEqual(request?.isCallBack, false)
        XCTAssertNil(request?.callRecordIdentifier)
    }

    func testTelURLExtractsEncodedPhoneNumber() throws {
        let url = try XCTUnwrap(URL(string: "tel://%2B86%20135%200000%200000"))

        XCTAssertEqual(
            DefaultCallingRequestParser.request(from: url),
            DefaultCallingRequest(
                number: "+8613500000000",
                callRecordIdentifier: nil,
                isCallBack: false
            )
        )
    }

    func testUnsupportedOrInvalidURLIsIgnored() throws {
        let webURL = try XCTUnwrap(URL(string: "https://example.com/13500000000"))
        let invalidTelURL = try XCTUnwrap(URL(string: "tel:not-a-number"))

        XCTAssertNil(DefaultCallingRequestParser.request(from: webURL))
        XCTAssertNil(DefaultCallingRequestParser.request(from: invalidTelURL))
    }

    func testSystemRecentCallBackUsesTheRecordsOriginalLineInsteadOfDefault() async {
        let originalLine = makeLine(numericID: 1, isOnline: true)
        let defaultLine = makeLine(numericID: 2, isOnline: true)
        let record = makeCallRecord(lineID: originalLine.id, remoteNumber: "10086")
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [originalLine, defaultLine],
            defaultLineID: defaultLine.id,
            callHistory: .loaded([record]),
            transport: transport
        )

        await model.handleDefaultCallingRequest(
            DefaultCallingRequest(
                number: "10086",
                callRecordIdentifier: record.id.uuidString,
                isCallBack: true
            )
        )

        XCTAssertEqual(transport.startedCalls.map(\.lineID), [originalLine.id])
        XCTAssertNil(model.pendingExternalDialRequest)
    }

    func testSystemRecentCallBackUsesPersistedLineWhenHistoryIsNotLoaded() async {
        let originalLine = makeLine(numericID: 1, isOnline: true)
        let defaultLine = makeLine(numericID: 2, isOnline: true)
        let store = ExternalDialRecentCallLineStore()
        let callID = UUID()
        store.record(callID: callID, remoteNumber: "10086", lineID: originalLine.id)
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [originalLine, defaultLine],
            defaultLineID: defaultLine.id,
            callHistory: .loading,
            recentCallLineStore: store,
            transport: transport
        )

        await model.handleDefaultCallingRequest(
            DefaultCallingRequest(
                number: "10086",
                callRecordIdentifier: callID.uuidString,
                isCallBack: true
            )
        )

        XCTAssertEqual(transport.startedCalls.map(\.lineID), [originalLine.id])
    }

    func testSystemRecentCallBackDoesNotSilentlyReplaceAnOfflineOriginalLine() async {
        let originalLine = makeLine(numericID: 1, isOnline: false)
        let otherLine = makeLine(numericID: 2, isOnline: true)
        let record = makeCallRecord(lineID: originalLine.id, remoteNumber: "10086")
        let transport = ExternalDialRecordingTransport()
        let model = makeModel(
            lines: [originalLine, otherLine],
            defaultLineID: otherLine.id,
            callHistory: .loaded([record]),
            transport: transport
        )

        await model.handleDefaultCallingRequest(
            DefaultCallingRequest(
                number: "10086",
                callRecordIdentifier: record.id.uuidString,
                isCallBack: true
            )
        )

        XCTAssertTrue(transport.startedCalls.isEmpty)
        XCTAssertEqual(model.pendingExternalDialRequest?.number, "10086")
        XCTAssertEqual(
            model.pendingExternalDialRequest?.lineSelection,
            .preferred(originalLine.id)
        )
    }

    private func makeModel(
        lines: [DeviceLineIdentity],
        defaultLineID: UUID?,
        callHistory: CallHistoryState = .loaded([]),
        recentCallLineStore: (any RecentCallLineStoring)? = nil,
        transport: ExternalDialRecordingTransport
    ) -> AppModel {
        AppModel(
            devices: lines,
            callHistory: callHistory,
            defaultCallLineID: defaultLineID,
            callCoordinator: CallKitCoordinator(
                transport: transport,
                integratesWithSystem: false
            ),
            defaultLineStore: ExternalDialDefaultLineStore(),
            recentCallLineStore: recentCallLineStore
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

    private func makeCallRecord(lineID: UUID, remoteNumber: String) -> CallRecord {
        CallRecord(
            id: UUID(),
            lineID: lineID,
            remoteNumber: remoteNumber,
            contactName: nil,
            direction: .incoming,
            result: .answered,
            startedAt: Date(timeIntervalSince1970: 1_000),
            endedAt: Date(timeIntervalSince1970: 1_100)
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
private final class ExternalDialRecentCallLineStore: RecentCallLineStoring {
    private struct Route {
        let callID: UUID
        let remoteNumber: String
        let lineID: UUID
    }

    private var routes: [Route] = []

    func record(callID: UUID, remoteNumber: String, lineID: UUID) {
        routes.append(Route(callID: callID, remoteNumber: remoteNumber, lineID: lineID))
    }

    func lineID(callRecordIdentifier: String?, remoteNumber: String) -> UUID? {
        if let callRecordIdentifier,
           let callID = UUID(uuidString: callRecordIdentifier),
           let exact = routes.last(where: { $0.callID == callID }) {
            return exact.lineID
        }
        let normalizedNumber = DialString.normalized(remoteNumber)
        return routes.last(where: {
            DialString.normalized($0.remoteNumber) == normalizedNumber
        })?.lineID
    }
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
