// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import CryptoKit
import Foundation
import Observation

@MainActor
protocol DeviceDiscoveryClock: Sendable {
    var now: Date { get }
    func sleepForPolling() async throws
}

struct SystemDeviceDiscoveryClock: DeviceDiscoveryClock {
    var now: Date { .now }

    func sleepForPolling() async throws {
        try await Task.sleep(for: .seconds(2))
    }
}

struct FixedDeviceDiscoveryClock: DeviceDiscoveryClock {
    let now: Date

    func sleepForPolling() async throws {
        try await Task.sleep(for: .seconds(60))
    }
}

enum DeviceDiscoveryLoadState: Equatable {
    case loading
    case loaded
    case failed(String)
}

@MainActor
@Observable
final class DeviceDiscoveryViewModel {
    var isDiscoveryEnabled: Bool {
        didSet {
            guard isDiscoveryEnabled != oldValue, !isApplyingServerState else { return }
            isChangingDiscovery = true
            let requestedValue = isDiscoveryEnabled
            Task { await updateDiscovery(requestedValue, fallback: oldValue) }
        }
    }
    private(set) var session: DeviceDiscoverySession?
    private(set) var discoveries: [DiscoveredDevice]
    private(set) var loadState: DeviceDiscoveryLoadState
    private(set) var isChangingDiscovery: Bool
    private(set) var decisionDeviceID: UUID?
    private(set) var approvedDevice: ApprovedDevice?

    @ObservationIgnored private let api: any DeviceDiscoveryAPI
    @ObservationIgnored private let clock: any DeviceDiscoveryClock
    @ObservationIgnored let automaticallyRefreshes: Bool
    @ObservationIgnored private var isApplyingServerState = false

    init(
        api: any DeviceDiscoveryAPI,
        clock: any DeviceDiscoveryClock = SystemDeviceDiscoveryClock(),
        isDiscoveryEnabled: Bool = false,
        session: DeviceDiscoverySession? = nil,
        discoveries: [DiscoveredDevice] = [],
        loadState: DeviceDiscoveryLoadState = .loading,
        automaticallyRefreshes: Bool = true
    ) {
        self.api = api
        self.clock = clock
        self.isDiscoveryEnabled = isDiscoveryEnabled
        self.session = session
        self.discoveries = discoveries
        self.loadState = loadState
        self.automaticallyRefreshes = automaticallyRefreshes
        self.isChangingDiscovery = false
        self.decisionDeviceID = nil
        self.approvedDevice = nil
    }

    var now: Date { clock.now }

    func load() async {
        if loadState != .loaded {
            loadState = .loading
        }
        await refresh()
    }

    func poll() async {
        while !Task.isCancelled {
            do {
                if case .failed = loadState {
                    try await Task.sleep(for: .seconds(10))
                } else {
                    try await clock.sleepForPolling()
                }
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            await refresh()
        }
    }

    func setDiscoveryEnabled(_ enabled: Bool) async {
        let fallback = isDiscoveryEnabled
        setDiscoveryEnabledFromServer(enabled)
        await updateDiscovery(enabled, fallback: fallback)
    }

    func approve(_ discovery: DiscoveredDevice) async {
        decisionDeviceID = discovery.id
        do {
            approvedDevice = try await api.approveDiscoveredDevice(id: discovery.id)
            discoveries.removeAll { $0.id == discovery.id }
            loadState = .loaded
        } catch {
            loadState = .failed(error.localizedDescription)
        }
        decisionDeviceID = nil
    }

    func reject(_ discovery: DiscoveredDevice) async {
        decisionDeviceID = discovery.id
        do {
            try await api.rejectDiscoveredDevice(id: discovery.id)
            discoveries.removeAll { $0.id == discovery.id }
            loadState = .loaded
        } catch {
            loadState = .failed(error.localizedDescription)
        }
        decisionDeviceID = nil
    }

    func clearApproval() {
        approvedDevice = nil
    }

    private func updateDiscovery(_ enabled: Bool, fallback: Bool) async {
        isChangingDiscovery = true
        do {
            if enabled {
                apply(try await api.openDeviceDiscovery())
                discoveries = try await api.discoveredDevices()
            } else {
                try await api.closeDeviceDiscovery()
                apply(DeviceDiscoverySessionState(active: false, session: session))
                discoveries = []
            }
            loadState = .loaded
        } catch {
            setDiscoveryEnabledFromServer(fallback)
            loadState = .failed(error.localizedDescription)
        }
        isChangingDiscovery = false
    }

    private func refresh() async {
        do {
            let state = try await api.deviceDiscoverySession()
            apply(state)
            discoveries = state.active ? try await api.discoveredDevices() : []
            loadState = .loaded
        } catch {
            loadState = .failed(error.localizedDescription)
        }
    }

    private func apply(_ state: DeviceDiscoverySessionState) {
        session = state.session
        setDiscoveryEnabledFromServer(state.active)
    }

    private func setDiscoveryEnabledFromServer(_ enabled: Bool) {
        isApplyingServerState = true
        isDiscoveryEnabled = enabled
        isApplyingServerState = false
    }
}

enum DeviceDiscoveryTimer {
    static func interval(now: Date, expiresAt: Date) -> ClosedRange<Date> {
        now...max(now, expiresAt)
    }
}

extension DiscoveredDevice {
    var shortIdentifier: String {
        let raw = deviceUUID.uuidString.uppercased()
        return "\(raw.prefix(8))…\(raw.suffix(4))"
    }

    var identityFingerprint: String {
        let digest = SHA256.hash(data: identityPublicKey)
        return digest.prefix(6).map { String(format: "%02X", $0) }.joined(separator: " ")
    }
}
