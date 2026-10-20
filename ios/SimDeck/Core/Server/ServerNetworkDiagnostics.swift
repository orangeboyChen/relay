import Darwin
import Foundation
import MarsRSNet
import OSLog

@MainActor
final class ServerNetworkDiagnostics {
    nonisolated private static let logger = Logger(
        subsystem: "com.nowcent.simdeck",
        category: "NetworkDiagnostics"
    )
    nonisolated private static let sdtLock = NSLock()

    private let queue = DispatchQueue(label: "com.nowcent.simdeck.network-diagnostics", qos: .utility)
    private var lastRunAt = Date.distantPast

    func diagnose(url: URL) {
        diagnose(url: url, networkType: 0)
    }

    func diagnose(url: URL, networkType: Int32) {
        guard Date().timeIntervalSince(lastRunAt) >= 60 else { return }
        lastRunAt = Date()
        queue.async {
            guard let report = Self.run(
                url: url,
                networkType: networkType,
                probe: Self.probe,
                httpProbe: Self.probeHTTP
            ) else {
                return
            }
            Self.logger.error("Server connectivity diagnosis: \(report, privacy: .public)")
        }
    }

    nonisolated static func run(
        url: URL,
        networkType: Int32,
        probe: @escaping (MarsSdt.Query) -> MarsSdt.Result,
        httpProbe: (String, UInt32) -> MarsSdt.Result
    ) -> String? {
        guard let host = url.host, !host.isEmpty else { return nil }
        guard url.scheme == "https" || url.scheme == "http" else { return nil }

        sdtLock.lock()
        // BASIC has a Ping step before DNS. An empty port list skips Ping while retaining the host name for DNS.
        let dnsReport = performDNSCheck(
            longLink: [MarsSdt.Link(name: host, ports: [])],
            networkType: networkType,
            probe: probe
        )
        sdtLock.unlock()

        let httpResult = httpProbe(url.absoluteString, 5_000)
        let httpSummary: String
        if case let .http(errorCode, rtt, statusCode) = httpResult {
            httpSummary = "errorCode=\(errorCode) statusCode=\(statusCode) rtt=\(rtt)ms"
        } else {
            httpSummary = "unavailable"
        }
        return "DNS: \(dnsReport ?? "unavailable"); HTTP: \(httpSummary)"
    }

    nonisolated private static func performDNSCheck(
        longLink: [MarsSdt.Link],
        networkType: Int32,
        probe: @escaping (MarsSdt.Query) -> MarsSdt.Result
    ) -> String? {
        MarsSdt.reset()
        guard MarsSdt.startActiveCheck(
            longLink: longLink,
            shortLink: [],
            mode: 1,
            timeout: 5_000
        ) == 0 else { return nil }
        guard MarsSdt.runChecks(networkType: networkType, probe: probe) == 0 else { return nil }
        return MarsSdt.takeReport()
    }

    nonisolated private static func probe(_ query: MarsSdt.Query) -> MarsSdt.Result {
        switch query.probe {
        case .dns:
            return probeDNS(host: query.host)
        default:
            return .nothing
        }
    }

    nonisolated private static func probeDNS(host: String) -> MarsSdt.Result {
        let started = DispatchTime.now().uptimeNanoseconds
        var hints = addrinfo()
        hints.ai_family = AF_UNSPEC
        hints.ai_socktype = SOCK_STREAM
        var first: UnsafeMutablePointer<addrinfo>?
        let errorCode = getaddrinfo(host, nil, &hints, &first)
        defer { if let first { freeaddrinfo(first) } }

        var addresses: [String] = []
        var cursor = first
        while let entry = cursor {
            var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            if getnameinfo(
                entry.pointee.ai_addr,
                entry.pointee.ai_addrlen,
                &buffer,
                socklen_t(buffer.count),
                nil,
                0,
                NI_NUMERICHOST
            ) == 0 {
                let address = String(
                    decoding: buffer.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) },
                    as: UTF8.self
                )
                if !addresses.contains(address) {
                    addresses.append(address)
                }
            }
            cursor = entry.pointee.ai_next
        }
        return .dns(
            errorCode: errorCode == 0 ? 0 : -abs(errorCode),
            rtt: elapsedMilliseconds(since: started),
            addresses: addresses
        )
    }

    nonisolated private static func probeHTTP(
        urlString: String,
        timeout: UInt32
    ) -> MarsSdt.Result {
        guard let url = URL(string: urlString),
              url.scheme == "https" || url.scheme == "http" else {
            return .http(errorCode: -1, rtt: 0, statusCode: 0)
        }
        let started = DispatchTime.now().uptimeNanoseconds
        let timeoutSeconds = TimeInterval(max(timeout, 1_000)) / 1_000
        var request = URLRequest(url: url)
        request.timeoutInterval = timeoutSeconds
        let result = HTTPProbeResult()
        let completion = DispatchSemaphore(value: 0)
        let session = URLSession(configuration: .ephemeral)
        let task = session.dataTask(with: request) { _, response, error in
            result.store(response: response, error: error)
            completion.signal()
        }
        task.resume()
        if completion.wait(timeout: .now() + timeoutSeconds) == .timedOut {
            task.cancel()
            session.invalidateAndCancel()
            return .http(
                errorCode: -1,
                rtt: elapsedMilliseconds(since: started),
                statusCode: 0
            )
        }
        session.finishTasksAndInvalidate()
        let outcome = result.read()
        return .http(
            errorCode: outcome.errorCode,
            rtt: elapsedMilliseconds(since: started),
            statusCode: outcome.statusCode
        )
    }

    nonisolated private static func elapsedMilliseconds(since started: UInt64) -> UInt64 {
        (DispatchTime.now().uptimeNanoseconds - started) / 1_000_000
    }
}

private final class HTTPProbeResult: @unchecked Sendable {
    private let lock = NSLock()
    private var errorCode: Int32 = -1
    private var statusCode: Int32 = 0

    func store(response: URLResponse?, error: Error?) {
        lock.lock()
        defer { lock.unlock() }
        errorCode = error.map { Int32(($0 as NSError).code) } ?? 0
        statusCode = Int32((response as? HTTPURLResponse)?.statusCode ?? 0)
    }

    func read() -> (errorCode: Int32, statusCode: Int32) {
        lock.lock()
        defer { lock.unlock() }
        return (errorCode, statusCode)
    }
}
