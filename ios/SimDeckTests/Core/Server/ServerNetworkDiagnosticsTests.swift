import Foundation
import MarsRSNet
import XCTest
@testable import SimDeck

final class ServerNetworkDiagnosticsTests: XCTestCase {
    func testMarsSDTReportsDNSAndHTTPSProbeUsesConfiguredServer() throws {
        let url = try XCTUnwrap(URL(string: "https://deck.example.test/healthz"))
        var queried: [MarsSdt.Probe] = []
        let report = try XCTUnwrap(ServerNetworkDiagnostics.run(
            url: url,
            networkType: 2,
            probe: { query in
                queried.append(query.probe)
                switch query.probe {
                case .dns:
                    XCTAssertEqual(query.host, "deck.example.test")
                    return .dns(errorCode: 0, rtt: 12, addresses: ["192.0.2.1"])
                default:
                    return .nothing()
                }
            }, httpProbe: { urlString, timeout in
                XCTAssertEqual(urlString, url.absoluteString)
                XCTAssertEqual(timeout, 5_000)
                return .http(errorCode: 0, rtt: 40, statusCode: 200)
            }
        ))

        XCTAssertTrue(queried.contains(.dns))
        XCTAssertFalse(queried.contains(.ping))
        XCTAssertTrue(report.contains("\"networkType\":2"))
        XCTAssertTrue(report.contains("statusCode=200"))
        XCTAssertTrue(report.contains("192.0.2.1"))
    }
}
