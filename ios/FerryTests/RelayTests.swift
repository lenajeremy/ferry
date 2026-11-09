import Foundation
import Network
import Testing
@testable import Ferry

@Suite("Reaching a computer nearby")
struct RelayTests {
    @Test func relaysEverythingBothWaysUnchanged() async throws {
        let echo = try await Echo.start()
        let relay = try await PeerRelay.shared.start("test-bytes", to: echo.endpoint)
        defer { PeerRelay.shared.stop("test-bytes"); echo.stop() }

        let sent = Data((0..<3_000_000).map { UInt8(truncatingIfNeeded: $0 &* 31 &+ 7) })
        let back = try await Wire.roundTrip(sent, port: relay)
        #expect(back == sent)
    }

    @Test func relaysManyConnectionsAtOnce() async throws {
        let echo = try await Echo.start()
        let relay = try await PeerRelay.shared.start("test-many", to: echo.endpoint)
        defer { PeerRelay.shared.stop("test-many"); echo.stop() }

        try await withThrowingTaskGroup(of: Bool.self) { group in
            for i in 0..<8 {
                group.addTask {
                    let sent = Data(repeating: UInt8(i), count: 200_000 + i)
                    return try await Wire.roundTrip(sent, port: relay) == sent
                }
            }
            for try await same in group { #expect(same) }
        }
    }

    @Test func closesWhenTheComputerIsGone() async throws {
        let echo = try await Echo.start()
        let gone = echo.endpoint
        echo.stop()
        let relay = try await PeerRelay.shared.start("test-gone", to: gone)
        defer { PeerRelay.shared.stop("test-gone") }

        let start = Date()
        let back = try? await Wire.roundTrip(Data("hello".utf8), port: relay)
        #expect(back?.isEmpty ?? true)
        #expect(Date().timeIntervalSince(start) < 5)
    }

    @Test func aComputerIsReachedThroughItsRelayWhileThereIsOne() async throws {
        let mac = FerryComputer(id: "test-route", name: "Mac", host: "mac.local", port: 7070, tlsPort: 7071, ca: Data([1]), token: "t")
        #expect(mac.base.absoluteString == "https://mac.local:7071")
        let echo = try await Echo.start()
        defer { echo.stop() }
        let relay = try await PeerRelay.shared.start("test-route", to: echo.endpoint)
        #expect(mac.base.absoluteString == "https://127.0.0.1:\(relay)")
        PeerRelay.shared.stop("test-route")
        #expect(mac.base.absoluteString == "https://mac.local:7071")

        // A computer paired without a secure address never goes through one.
        let plain = FerryComputer(id: "test-route", name: "PC", host: "192.168.1.9", port: 7070, tlsPort: nil, ca: nil, token: "t")
        _ = try await PeerRelay.shared.start("test-route", to: echo.endpoint)
        #expect(plain.base.absoluteString == "http://192.168.1.9:7070")
        PeerRelay.shared.stop("test-route")
    }

    @Test func aComputersOwnWiFiHasItAtTheGateway() {
        #expect(Nearby.gateways(["192.168.137.45", "10.42.0.7", "192.168.0.3", "172.20.10.2"]) == ["192.168.137.1", "10.42.0.1"])
        #expect(Nearby.gateways(["192.168.137.1"]).isEmpty) // this is the computer
    }

    @Test func aComputerWhereItWasIsUsedStraightAway() async throws {
        let echo = try await Echo.start()
        defer { echo.stop() }
        guard case let .hostPort(host, port) = echo.endpoint else { return }
        let here = FerryComputer(id: "here", name: "Mac", host: "\(host)", port: 7070, tlsPort: Int(port.rawValue), ca: Data([1]), token: "t")
        let start = Date()
        #expect(await Reconnect.find(here) == here)
        #expect(Date().timeIntervalSince(start) < 1) // the lookups elsewhere stop as soon as it answers
    }

    @Test func computersPairedWithoutEncryptionAreNeverLookedForElsewhere() async {
        let plain = FerryComputer(id: "p", name: "PC", host: "192.0.2.9", port: 7070, tlsPort: nil, ca: nil, token: "t")
        let start = Date()
        #expect(await Reconnect.find(plain) == plain)
        #expect(Date().timeIntervalSince(start) < 0.5)
    }

    @Test func atHomeTheNetworkBeatsTailscale() async throws {
        let echo = try await Echo.start()
        defer { echo.stop() }
        guard case let .hostPort(host, port) = echo.endpoint else { return }
        let here = FerryComputer(id: "home", name: "Mac", host: "\(host)", port: 7070, tlsPort: Int(port.rawValue), ca: Data([1]), token: "t", tailnet: "localhost")
        let start = Date()
        #expect(await Reconnect.find(here) == here)
        #expect(Date().timeIntervalSince(start) < 1)
    }

    @Test func awayItIsFoundAtItsTailscaleAddressAndKeepsItsHomeOne() async throws {
        let echo = try await Echo.start()
        defer { echo.stop() }
        guard case let .hostPort(host, port) = echo.endpoint else { return }
        let paired = FerryComputer(id: "away", name: "Mac", host: "192.0.2.9", port: 7070, tlsPort: Int(port.rawValue), ca: Data([1]), token: "t", tailnet: "\(host)")
        let found = try #require(await Reconnect.find(paired))
        #expect(found.host == "\(host)" && found.host == paired.tailnet && paired.host == "192.0.2.9")
    }

    @Test func aPairingFromBeforeTailscaleStillLoads() throws {
        let old = #"{"id":"a","name":"Mac","host":"mac.local","port":7070,"tlsPort":7071,"token":"t"}"#
        #expect(try JSONDecoder().decode(FerryComputer.self, from: Data(old.utf8)).tailnet == nil)
    }

    @Test func nowhereMentionsTailscaleOnlyWhenTheComputerHasIt() {
        var c = FerryComputer(id: "a", name: "Mac", host: "h", port: 1, tlsPort: 2, ca: Data([1]), token: "t")
        #expect(!Reconnect.nowhere(c, onWiFi: true).contains("Tailscale"))
        c.tailnet = "100.101.102.103"
        #expect(Reconnect.nowhere(c, onWiFi: true).contains("Tailscale"))
        #expect(Reconnect.nowhere(c, onWiFi: false).contains("Tailscale"))
    }

    /// End to end, against a Ferry whose certificate doesn't list the address it's reached at, as with Tailscale's
    /// (opt in: TEST_RUNNER_FERRY_AWAY_QR=<its pairing link>, TEST_RUNNER_FERRY_OTHER_CA=<another authority, base64 DER>).
    @Test(.enabled(if: ProcessInfo.processInfo.environment["FERRY_AWAY_QR"] != nil))
    func reachesAComputerAtAnAddressItsCertificateDoesntList() async throws {
        let env = ProcessInfo.processInfo.environment
        var scanned = try #require(Pairing.parse(env["FERRY_AWAY_QR"] ?? ""))
        let otherCA = try #require(Data(base64Encoded: env["FERRY_OTHER_CA"] ?? ""))
        let fingerprint = try #require(scanned.fingerprint)
        let address = try #require(Nearby.ownAddresses().first { !$0.hasPrefix("127.") })
        let tlsPort = try #require(scanned.tlsPort)
        scanned.host = "nowhere.invalid"
        // Paired through the relay, where the certificate is good for the address.
        let c = try await FerryAPI.pair(through: .hostPort(host: .init(address), port: .init(rawValue: UInt16(tlsPort))!),
                                        scanned, fingerprint: fingerprint, device: "Tailscale Test", kind: "iPhone", previous: nil)
        PeerRelay.shared.stop(c.id)

        // Not marked as its Tailscale address: refused, as any address the certificate doesn't list. (First, so
        // no connection that passed the check is reused for it.)
        var unmarked = c
        unmarked.host = address
        await #expect(throws: (any Error).self) { _ = try await FerryAPI(unmarked).info() }

        // Its Tailscale address, but another computer's authority: refused, and the key never sent.
        var impostor = unmarked
        impostor.tailnet = address
        impostor.ca = otherCA
        await #expect(throws: (any Error).self) { _ = try await FerryAPI(impostor).info() }

        // The real thing: found there, and everything works.
        var paired = c
        paired.tailnet = address
        let away = try #require(await Reconnect.find(paired))
        #expect(away.host == address)
        let api = FerryAPI(away)
        #expect(try await api.info().id == c.id)
        let file = FileManager.default.temporaryDirectory.appendingPathComponent("away-test-\(UUID().uuidString.prefix(6)).txt")
        try Data("over tailscale".utf8).write(to: file)
        let sent = try await api.upload(file, name: file.lastPathComponent) { _ in }
        #expect(try await api.files().contains { $0.name == sent.name })
        try await api.delete(sent)
        await api.unpairSelf()
    }

    /// End to end, against a running Ferry (opt in: TEST_RUNNER_FERRY_NEARBY_QR=<its pairing link>, with
    /// FERRY_LAN_IS_REMOTE=1 there, as this Mac is then another device to it). It's found by the fingerprint in its
    /// code (_ferrys._tcp); pairing, sending and looking go through the relay; a lost relay is found again.
    @Test(.enabled(if: ProcessInfo.processInfo.environment["FERRY_NEARBY_QR"] != nil))
    func pairsAndSendsThroughTheRelay() async throws {
        let link = try #require(ProcessInfo.processInfo.environment["FERRY_NEARBY_QR"])
        var scanned = try #require(Pairing.parse(link))
        let fingerprint = try #require(scanned.fingerprint)
        let found = try #require(await Nearby.find("_ferrys._tcp", peerToPeer: true, timeout: 8, matching: { $0["fp"] == fingerprint }))
        #expect(found.txt["id"] != nil)
        // Through the relay to its network address (the Simulator shares this Mac's, which Bonjour may hand
        // out as loopback, and loopback is the computer itself to Ferry).
        let address = try #require(Nearby.ownAddresses().first { !$0.hasPrefix("127.") })
        scanned.host = "nowhere.invalid"
        let c = try await FerryAPI.pair(through: .hostPort(host: .init(address), port: .init(rawValue: UInt16(scanned.tlsPort!))!),
                                        scanned, fingerprint: fingerprint, device: "Relay Test", kind: "iPhone", previous: nil)
        defer { Task { await FerryAPI(c).unpairSelf(); PeerRelay.shared.stop(c.id) } }
        #expect(c.host == "nowhere.invalid" && c.secure && c.id == found.txt["id"])
        #expect(PeerRelay.shared.port(for: c.id) != nil)
        #expect(FerryAPI(c).base.host() == "127.0.0.1")

        let file = FileManager.default.temporaryDirectory.appendingPathComponent("relay-test-\(UUID().uuidString.prefix(6)).txt")
        try Data("through the relay".utf8).write(to: file)
        let sent = try await FerryAPI(c).upload(file, name: file.lastPathComponent) { _ in }
        #expect(try await FerryAPI(c).files().contains { $0.name == sent.name })
        try await FerryAPI(c).delete(sent)

        PeerRelay.shared.stop(c.id)
        let back = try #require(await Reconnect.find(c))
        #expect(PeerRelay.shared.port(for: c.id) != nil)
        #expect(try await FerryAPI(back).info().id == c.id)
    }
}

/// Talks to a TCP port on this device's loopback.
nonisolated enum Wire {
    /// Sends `data` while reading what comes back, until as much has (or the connection ends).
    static func roundTrip(_ data: Data, port: UInt16) async throws -> Data {
        let c = NWConnection(to: .hostPort(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: port)!), using: .tcp)
        defer { c.cancel() }
        try await ready(c)
        c.send(content: data, completion: .contentProcessed { _ in })
        var back = Data()
        while back.count < data.count {
            let (piece, ended) = try await receive(c)
            back.append(piece)
            if ended { break }
        }
        return back
    }

    static func ready(_ c: NWConnection) async throws {
        let once = Once()
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            c.stateUpdateHandler = { state in
                switch state {
                case .ready: if once.take() { done.resume() }
                case let .failed(e), let .waiting(e): if once.take() { done.resume(throwing: e) }
                default: break
                }
            }
            c.start(queue: .global())
        }
    }

    static func receive(_ c: NWConnection) async throws -> (Data, Bool) {
        try await withCheckedThrowingContinuation { done in
            c.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { data, _, ended, error in
                if let error { done.resume(throwing: error) } else { done.resume(returning: (data ?? Data(), ended)) }
            }
        }
    }
}

/// A server that sends back whatever it's sent. The relay only goes to other devices, never back into
/// this one, so it's reached at this device's network address, as another device would be.
nonisolated final class Echo: @unchecked Sendable {
    let listener: NWListener
    let port: NWEndpoint.Port
    var endpoint: NWEndpoint {
        let address = Nearby.ownAddresses().first { !$0.hasPrefix("127.") } ?? "127.0.0.1"
        return .hostPort(host: .init(address), port: port)
    }

    private init(_ listener: NWListener, _ port: NWEndpoint.Port) {
        self.listener = listener
        self.port = port
    }

    static func start() async throws -> Echo {
        let listener = try NWListener(using: .tcp)
        listener.newConnectionHandler = { c in
            c.start(queue: .global())
            echo(c)
        }
        let once = Once()
        let port: NWEndpoint.Port = try await withCheckedThrowingContinuation { done in
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready: if once.take() { done.resume(returning: listener.port!) }
                case let .failed(e): if once.take() { done.resume(throwing: e) }
                default: break
                }
            }
            listener.start(queue: .global())
        }
        return Echo(listener, port)
    }

    func stop() { listener.cancel() }

    private static func echo(_ c: NWConnection) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 1 << 16) { data, _, ended, error in
            if let data, !data.isEmpty {
                c.send(content: data, completion: .contentProcessed { _ in ended || error != nil ? c.cancel() : echo(c) })
            } else if ended || error != nil {
                c.cancel()
            } else {
                echo(c)
            }
        }
    }
}
