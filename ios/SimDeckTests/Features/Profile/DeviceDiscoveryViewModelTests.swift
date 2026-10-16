import Foundation
import XCTest
@testable import SimDeck

@MainActor
final class DeviceDiscoveryViewModelTests: XCTestCase {
    func testLoadPublishesActiveSessionAndPendingDevices() async {
        let candidate = makeCandidate(id: UUID())
        let api = DeviceDiscoveryAPISpy(
            state: makeSessionState(active: true),
            discoveries: [candidate]
        )
        let model = DeviceDiscoveryViewModel(api: api, clock: TestDeviceDiscoveryClock())

        await model.load()

        XCTAssertEqual(model.loadState, .loaded)
        XCTAssertTrue(model.isDiscoveryEnabled)
        XCTAssertEqual(model.discoveries, [candidate])
        XCTAssertEqual(api.sessionReadCount, 1)
        XCTAssertEqual(api.discoveryReadCount, 1)
    }

    func testEnablingAndDisablingDiscoveryUsesServerSession() async {
        let candidate = makeCandidate(id: UUID())
        let api = DeviceDiscoveryAPISpy(
            state: makeSessionState(active: true),
            discoveries: [candidate]
        )
        let model = DeviceDiscoveryViewModel(api: api, clock: TestDeviceDiscoveryClock())

        await model.setDiscoveryEnabled(true)

        XCTAssertTrue(model.isDiscoveryEnabled)
        XCTAssertEqual(model.discoveries, [candidate])
        XCTAssertEqual(api.openCount, 1)

        await model.setDiscoveryEnabled(false)

        XCTAssertFalse(model.isDiscoveryEnabled)
        XCTAssertTrue(model.discoveries.isEmpty)
        XCTAssertEqual(api.closeCount, 1)
    }

    func testFailedDiscoveryToggleRestoresPreviousState() async {
        let api = DeviceDiscoveryAPISpy(
            state: makeSessionState(active: false),
            discoveries: [],
            error: TestFailure.unavailable
        )
        let model = DeviceDiscoveryViewModel(api: api, clock: TestDeviceDiscoveryClock())

        await model.setDiscoveryEnabled(true)

        XCTAssertFalse(model.isDiscoveryEnabled)
        XCTAssertEqual(model.loadState, .failed(TestFailure.unavailable.localizedDescription))
    }

    func testApprovalAndRejectionRemoveOnlyTheDecidedCandidate() async {
        let approvedCandidate = makeCandidate(id: UUID())
        let rejectedCandidate = makeCandidate(id: UUID())
        let api = DeviceDiscoveryAPISpy(
            state: makeSessionState(active: true),
            discoveries: [approvedCandidate, rejectedCandidate]
        )
        let model = DeviceDiscoveryViewModel(
            api: api,
            clock: TestDeviceDiscoveryClock(),
            isDiscoveryEnabled: true,
            session: makeSessionState(active: true).session,
            discoveries: [approvedCandidate, rejectedCandidate],
            loadState: .loaded
        )

        await model.approve(approvedCandidate)

        XCTAssertEqual(
            model.approvedDevice,
            ApprovedDevice(deviceUUID: approvedCandidate.id, numericID: 2)
        )
        XCTAssertEqual(model.discoveries, [rejectedCandidate])
        XCTAssertEqual(api.approvedIDs, [approvedCandidate.id])

        await model.reject(rejectedCandidate)

        XCTAssertTrue(model.discoveries.isEmpty)
        XCTAssertEqual(api.rejectedIDs, [rejectedCandidate.id])
    }

    func testTrustCuesAreStableAndDerivedFromImmutableIdentity() {
        let id = UUID(uuidString: "01900000-0000-7000-8000-000000000401")!
        let candidate = makeCandidate(id: id)

        XCTAssertEqual(candidate.shortIdentifier, "01900000…0401")
        XCTAssertEqual(candidate.identityFingerprint, "9F 4F B6 8F 3E 1D")
    }

    private func makeSessionState(active: Bool) -> DeviceDiscoverySessionState {
        DeviceDiscoverySessionState(
            active: active,
            session: DeviceDiscoverySession(
                openedAt: Date(timeIntervalSince1970: 1_792_108_800),
                expiresAt: Date(timeIntervalSince1970: 1_792_109_400),
                closedAt: nil
            )
        )
    }

    private func makeCandidate(id: UUID) -> DiscoveredDevice {
        DiscoveredDevice(
            deviceUUID: id,
            identityPublicKey: Data(repeating: 4, count: 32),
            commandPublicKey: Data(repeating: 5, count: 32),
            status: .pending,
            firstSeenAt: Date(timeIntervalSince1970: 1_792_108_815),
            lastSeenAt: Date(timeIntervalSince1970: 1_792_108_830),
            expiresAt: Date(timeIntervalSince1970: 1_792_109_400)
        )
    }
}

private enum TestFailure: LocalizedError {
    case unavailable

    var errorDescription: String? { "Discovery unavailable" }
}

@MainActor
private final class DeviceDiscoveryAPISpy: DeviceDiscoveryAPI {
    var state: DeviceDiscoverySessionState
    var discoveries: [DiscoveredDevice]
    var error: Error?
    var sessionReadCount = 0
    var discoveryReadCount = 0
    var openCount = 0
    var closeCount = 0
    var approvedIDs: [UUID] = []
    var rejectedIDs: [UUID] = []

    init(
        state: DeviceDiscoverySessionState,
        discoveries: [DiscoveredDevice],
        error: Error? = nil
    ) {
        self.state = state
        self.discoveries = discoveries
        self.error = error
    }

    func openDeviceDiscovery() async throws -> DeviceDiscoverySessionState {
        try throwIfNeeded()
        openCount += 1
        return state
    }

    func deviceDiscoverySession() async throws -> DeviceDiscoverySessionState {
        try throwIfNeeded()
        sessionReadCount += 1
        return state
    }

    func closeDeviceDiscovery() async throws {
        try throwIfNeeded()
        closeCount += 1
    }

    func discoveredDevices() async throws -> [DiscoveredDevice] {
        try throwIfNeeded()
        discoveryReadCount += 1
        return discoveries
    }

    func approveDiscoveredDevice(id: UUID) async throws -> ApprovedDevice {
        try throwIfNeeded()
        approvedIDs.append(id)
        return ApprovedDevice(deviceUUID: id, numericID: 2)
    }

    func rejectDiscoveredDevice(id: UUID) async throws {
        try throwIfNeeded()
        rejectedIDs.append(id)
    }

    private func throwIfNeeded() throws {
        if let error { throw error }
    }
}

private struct TestDeviceDiscoveryClock: DeviceDiscoveryClock {
    let now = Date(timeIntervalSince1970: 1_792_108_800)

    func sleepForPolling() async throws {
        throw CancellationError()
    }
}
