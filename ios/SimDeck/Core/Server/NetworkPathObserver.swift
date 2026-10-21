import Foundation
import Network

enum NetworkInterfaceKind: Int32, Equatable, Sendable {
    case unavailable = 0
    case wifi = 1
    case cellular = 2
    case other = 3
}

struct NetworkPathUpdate: Equatable, Sendable {
    let isAvailable: Bool
    let interface: NetworkInterfaceKind
    let requiresConnectionMigration: Bool
}

struct NetworkPathStateTracker {
    private var hasReceivedPath = false
    private var wasAvailable = false
    private var lastAvailableInterface: NetworkInterfaceKind?

    mutating func update(
        isAvailable: Bool,
        interface: NetworkInterfaceKind
    ) -> NetworkPathUpdate? {
        let requiresConnectionMigration = isAvailable && hasReceivedPath && (
            !wasAvailable || lastAvailableInterface.map { $0 != interface } == true
        )

        defer {
            hasReceivedPath = true
            wasAvailable = isAvailable
            if isAvailable {
                lastAvailableInterface = interface
            }
        }

        if !hasReceivedPath, isAvailable {
            return nil
        }
        return NetworkPathUpdate(
            isAvailable: isAvailable,
            interface: interface,
            requiresConnectionMigration: requiresConnectionMigration
        )
    }
}

@MainActor
final class NetworkPathObserver {
    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "com.nowcent.simdeck.network-path")
    private let onChange: @MainActor (NetworkPathUpdate) -> Void
    private var stateTracker = NetworkPathStateTracker()
    private var isRunning = false
    private(set) var networkType: Int32 = 0

    init(onChange: @escaping @MainActor (NetworkPathUpdate) -> Void) {
        self.onChange = onChange
    }

    func start() {
        guard !isRunning else { return }
        isRunning = true
        monitor.pathUpdateHandler = { [weak self] path in
            Task { @MainActor [weak self] in
                self?.handle(path)
            }
        }
        monitor.start(queue: queue)
    }

    private func handle(_ path: NWPath) {
        let isAvailable = path.status == .satisfied
        let interface: NetworkInterfaceKind = if !isAvailable {
            .unavailable
        } else if path.usesInterfaceType(.wifi) {
            .wifi
        } else if path.usesInterfaceType(.cellular) {
            .cellular
        } else {
            .other
        }
        networkType = interface.rawValue
        if let update = stateTracker.update(isAvailable: isAvailable, interface: interface) {
            onChange(update)
        }
    }
}
