import Foundation
import Network

// Finding a paired computer again, and reaching a Mac nearby with no network in common.
//
// On a network in common, the computer is at its saved address, or Bonjour (_ferry._tcp) says where
// it went. With no network in common, a Mac announces its secure address over Apple's peer-to-peer
// Wi-Fi (awdl0, what AirDrop uses) as _ferrys._tcp. URLSession can't use that link, so the app relays:
// PeerRelay listens on this iPhone's own loopback and passes each connection's bytes to the Mac over
// the link. The encryption stays end to end: URLSession speaks TLS to the Mac, pinned to its own
// authority as everywhere else, and the relay only ever sees ciphertext.

/// One-off Bonjour lookups.
nonisolated enum Nearby {
    struct Service: Sendable {
        let endpoint: NWEndpoint
        let txt: [String: String]
        /// Apple's peer-to-peer link it was found on (awdl0), when that's the only way it was found: connections
        /// to it must take it, or the system picks another way (mobile data, say) that can't reach it. Found on
        /// a network too, the network is used.
        let peerLink: NWInterface?
    }

    static let queue = DispatchQueue(label: "app.ferry.nearby")

    /// Whether this iPhone is on a Wi-Fi network now (it may have Wi-Fi switched on and be on none).
    static var onWiFi: Bool { wifi.box.lock.withLock { wifi.box.on } }
    private static let wifi: (monitor: NWPathMonitor, box: Box) = {
        let monitor = NWPathMonitor(requiredInterfaceType: .wifi)
        let box = Box()
        monitor.pathUpdateHandler = { path in box.lock.withLock { box.on = path.status == .satisfied } }
        monitor.start(queue: queue)
        return (monitor, box)
    }()
    private final class Box: @unchecked Sendable {
        let lock = NSLock()
        var on = true
    }

    /// The first service of `type` whose TXT record passes `matches`, or nil after `timeout`.
    /// `peerToPeer` looks over Apple's peer-to-peer Wi-Fi as well as the network.
    static func find(_ type: String, peerToPeer: Bool, timeout: TimeInterval,
                     matching matches: @escaping @Sendable ([String: String]) -> Bool) async -> Service? {
        let params = NWParameters()
        params.includePeerToPeer = peerToPeer
        let browser = NWBrowser(for: .bonjourWithTXTRecord(type: type, domain: nil), using: params)
        let once = Once()
        return await withTaskCancellationHandler {
            await withCheckedContinuation { (done: CheckedContinuation<Service?, Never>) in
                let finish: @Sendable (Service?) -> Void = { found in
                    guard once.take() else { return }
                    browser.cancel()
                    done.resume(returning: found)
                }
                browser.browseResultsChangedHandler = { results, _ in
                    Trace.log("browse \(type) p2p=\(peerToPeer): \(results.count) result(s) " + results.map { "\($0.endpoint) on \($0.interfaces.map(\.name))" }.joined(separator: ", "))
                    for result in results {
                        guard case let .bonjour(record) = result.metadata, matches(record.dictionary) else { continue }
                        let isPeerLink = { (i: NWInterface) in i.name.hasPrefix("awdl") || i.name.hasPrefix("llw") }
                        let peerLink = result.interfaces.allSatisfy(isPeerLink) ? result.interfaces.first : nil
                        finish(Service(endpoint: result.endpoint, txt: record.dictionary, peerLink: peerLink))
                        return
                    }
                }
                browser.stateUpdateHandler = { state in
                    Trace.log("browse \(type) p2p=\(peerToPeer): \(state)")
                    switch state {
                    case .failed, .cancelled: finish(nil) // cancelled: the lookup isn't wanted any more
                    default: break
                    }
                }
                browser.start(queue: queue)
                queue.asyncAfter(deadline: .now() + timeout) { finish(nil) }
            }
        } onCancel: {
            browser.cancel()
        }
    }

    /// Whether a TCP connection to `endpoint` opens within `timeout` (it says nothing about who answers:
    /// URLSession's pinned check does that). `over` is a peer-to-peer link to take.
    static func opens(_ endpoint: NWEndpoint, over link: NWInterface? = nil, timeout: TimeInterval) async -> Bool {
        let connection = NWConnection(to: endpoint, using: parameters(over: link))
        let once = Once()
        return await withTaskCancellationHandler {
            await withCheckedContinuation { (done: CheckedContinuation<Bool, Never>) in
                let finish: @Sendable (Bool) -> Void = { ok in
                    guard once.take() else { return }
                    connection.cancel()
                    done.resume(returning: ok)
                }
                connection.stateUpdateHandler = { state in
                    Trace.log("open \(endpoint) over \(link?.name ?? "the network"): \(state)\(state == .ready ? " via \(connection.currentPath?.availableInterfaces.first?.name ?? "?")" : "")")
                    switch state {
                    case .ready: finish(true)
                    case .failed, .waiting, .cancelled: finish(false)
                    default: break
                    }
                }
                connection.pathUpdateHandler = { path in
                    Trace.log("open \(endpoint) path: \(path.status) \(path.availableInterfaces.map(\.name)) unsatisfied=\(path.unsatisfiedReason)")
                }
                connection.viabilityUpdateHandler = { viable in Trace.log("open \(endpoint) viable: \(viable)") }
                connection.start(queue: queue)
                queue.asyncAfter(deadline: .now() + timeout) { finish(false) }
            }
        } onCancel: {
            connection.cancel()
        }
    }

    /// TCP, over a peer-to-peer link when there is one (and never mobile data, which can't reach a computer nearby).
    static func parameters(over link: NWInterface?) -> NWParameters {
        let params = NWParameters.tcp
        if let link {
            params.includePeerToPeer = true
            params.requiredInterface = link
            params.prohibitedInterfaceTypes = [.cellular]
        }
        return params
    }

    /// This iPhone's own IPv4 addresses.
    static func ownAddresses() -> [String] {
        var list: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&list) == 0, let first = list else { return [] }
        defer { freeifaddrs(list) }
        var out: [String] = []
        for entry in sequence(first: first, next: { $0.pointee.ifa_next }) {
            guard let addr = entry.pointee.ifa_addr, addr.pointee.sa_family == UInt8(AF_INET) else { continue }
            var name = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            if getnameinfo(addr, socklen_t(addr.pointee.sa_len), &name, socklen_t(name.count), nil, 0, NI_NUMERICHOST) == 0 {
                out.append(name.withUnsafeBufferPointer { String(cString: $0.baseAddress!) })
            }
        }
        return out
    }

    /// When this iPhone is on a computer's own direct Wi-Fi (Windows' 192.168.137.x, or 10.42.0.x from
    /// NetworkManager on Linux), the computer is that network's gateway.
    static func gateways(_ own: [String]) -> [String] {
        var out: [String] = []
        for ip in own where ip.hasPrefix("192.168.137.") || ip.hasPrefix("10.42.0.") {
            let gateway = ip.split(separator: ".").prefix(3).joined(separator: ".") + ".1"
            if gateway != ip, !out.contains(gateway) { out.append(gateway) }
        }
        return out
    }
}

/// Relays connections from this iPhone's loopback to a Mac over Apple's peer-to-peer Wi-Fi (or any
/// endpoint), one listener per computer.
nonisolated final class PeerRelay: @unchecked Sendable {
    static let shared = PeerRelay()
    private let lock = NSLock()
    private var open: [String: (listener: NWListener, port: UInt16, target: NWEndpoint, link: NWInterface?, awake: NWBrowser?)] = [:]
    private let queue = DispatchQueue(label: "app.ferry.relay")

    /// The loopback port that leads to the computer `key`, while there is one.
    func port(for key: String) -> UInt16? { lock.withLock { open[key]?.port } }

    /// Starts relaying to `target` (over the peer-to-peer link `link`) for `key` (a computer's id), and gives the
    /// loopback port.
    func start(_ key: String, to target: NWEndpoint, over link: NWInterface? = nil) async throws -> UInt16 {
        if let existing = lock.withLock({ open[key] }), existing.target == target, existing.link == link { return existing.port }
        stop(key)
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any) // only this iPhone reaches it
        let listener = try NWListener(using: params)
        let queue = queue
        listener.newConnectionHandler = { inbound in Self.relay(inbound, to: target, over: link, on: queue) }
        let once = Once()
        let port: UInt16 = try await withCheckedThrowingContinuation { done in
            listener.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    if once.take() { done.resume(returning: listener.port?.rawValue ?? 0) }
                case let .failed(error):
                    if once.take() { done.resume(throwing: error) }
                default: break
                }
            }
            listener.start(queue: queue)
        }
        // iOS keeps its peer-to-peer link up only while something looks over it or uses it: a search held for the
        // relay's life keeps it up between connections.
        var awake: NWBrowser?
        if link != nil {
            let params = NWParameters()
            params.includePeerToPeer = true
            let browser = NWBrowser(for: .bonjour(type: "_ferrys._tcp", domain: nil), using: params)
            browser.start(queue: queue)
            awake = browser
        }
        lock.withLock { open[key] = (listener, port, target, link, awake) }
        return port
    }

    /// Hands the relay for `old` (one opened while pairing) to `new` (the computer's id).
    func rename(_ old: String, to new: String) {
        lock.withLock {
            guard let relay = open.removeValue(forKey: old) else { return }
            if let replaced = open.removeValue(forKey: new) {
                replaced.listener.cancel()
                replaced.awake?.cancel()
            }
            open[new] = relay
        }
    }

    func stop(_ key: String) {
        guard let relay = lock.withLock({ open.removeValue(forKey: key) }) else { return }
        relay.listener.cancel()
        relay.awake?.cancel()
    }

    private static func relay(_ inbound: NWConnection, to target: NWEndpoint, over link: NWInterface?, on queue: DispatchQueue) {
        let outbound = NWConnection(to: target, using: Nearby.parameters(over: link))
        let stop: @Sendable () -> Void = { inbound.cancel(); outbound.cancel() }
        let once = Once()
        outbound.stateUpdateHandler = { state in
            Trace.log("relay to \(target): \(state)")
            switch state {
            case .ready:
                if once.take() {
                    pipe(inbound, outbound, stop)
                    pipe(outbound, inbound, stop)
                }
            case .failed, .cancelled:
                stop()
            case let .waiting(error) where error == .posix(.ECONNREFUSED):
                stop() // nothing listening there
            default: break // .waiting otherwise: a peer-to-peer link coming (back) up
            }
        }
        // No way to the Mac after a while: give up, so the app looks for it again.
        queue.asyncAfter(deadline: .now() + 15) { if once.take() { stop() } }
        inbound.stateUpdateHandler = { state in
            if case .failed = state { stop() }
        }
        inbound.start(queue: queue)
        outbound.start(queue: queue)
    }

    /// Copies everything from one connection to the other, one piece at a time (the next is read
    /// once the last has gone), and closes both when either side ends.
    private static func pipe(_ from: NWConnection, _ to: NWConnection, _ stop: @escaping @Sendable () -> Void) {
        from.receive(minimumIncompleteLength: 1, maximumLength: 256 * 1024) { data, _, ended, error in
            let next: @Sendable () -> Void = { ended || error != nil ? stop() : pipe(from, to, stop) }
            if let data, !data.isEmpty {
                to.send(content: data, completion: .contentProcessed { sent in sent == nil ? next() : stop() })
            } else {
                next()
            }
        }
    }
}

/// Where a paired computer is now.
nonisolated enum Reconnect {
    private enum Found: Sendable {
        case at(FerryComputer)   // on a network in common, at this address
        case nearby(NWEndpoint, NWInterface?)  // a Mac over Apple's peer-to-peer Wi-Fi
    }

    /// What to say when a computer is nowhere to be found.
    static func nowhere(_ c: FerryComputer, onWiFi: Bool) -> String {
        let awayFromHome = c.tailnet == nil ? "" : " Away from home? Turn on Tailscale on this iPhone, and check \(c.name) is awake."
        return onWiFi
            ? "Can't reach \(c.name). Is Ferry running there, on this Wi-Fi?\(awayFromHome)"
            : "Can't reach \(c.name).\(awayFromHome) With no Wi-Fi network around, keep Wi-Fi switched on in Settings anyway: this iPhone reaches a Mac nearby without one."
    }

    /// The computer as it can be reached now: at its saved address, where Bonjour heard it (a PC
    /// that got a new address), as the gateway of its own direct Wi-Fi this iPhone is on, over
    /// Apple's peer-to-peer Wi-Fi (a Mac nearby, through PeerRelay), or at its Tailscale address
    /// (away from home; the address saved for it stays its home one). Nil if it's nowhere.
    ///
    /// Only a computer paired securely is looked for: everything then goes to it over TLS pinned to
    /// its own authority, so wherever a lookup points, this phone's key only reaches that computer.
    static func find(_ c: FerryComputer) async -> FerryComputer? {
        guard c.secure, let tlsPort = c.tlsPort, let port = NWEndpoint.Port(rawValue: UInt16(tlsPort)) else { return c }
        let found = await withTaskGroup(of: Found?.self) { group -> Found? in
            group.addTask {
                await Nearby.opens(.hostPort(host: .init(c.host), port: port), timeout: 2.5) ? .at(c) : nil
            }
            for gateway in Nearby.gateways(Nearby.ownAddresses()) where gateway != c.host {
                group.addTask {
                    guard await Nearby.opens(.hostPort(host: .init(gateway), port: port), timeout: 2.5) else { return nil }
                    var moved = c
                    moved.host = gateway
                    return .at(moved)
                }
            }
            group.addTask {
                guard let found = await Nearby.find("_ferry._tcp", peerToPeer: false, timeout: 4, matching: { $0["id"] == c.id }),
                      let host = found.txt["host"], host != c.host,
                      await Nearby.opens(.hostPort(host: .init(host), port: port), timeout: 2.5) else { return nil }
                var moved = c
                moved.host = host
                return .at(moved)
            }
            group.addTask {
                // Apple's peer-to-peer link can take a while to come up the first time.
                guard let found = await Nearby.find("_ferrys._tcp", peerToPeer: true, timeout: 6, matching: { $0["id"] == c.id }),
                      await Nearby.opens(found.endpoint, over: found.peerLink, timeout: 20) else { return nil }
                return .nearby(found.endpoint, found.peerLink)
            }
            if let tailnet = c.tailnet, tailnet != c.host {
                group.addTask {
                    // A moment later than the rest, so at home the faster network in common wins. Tailscale
                    // can take a few seconds to wake its connection.
                    try? await Task.sleep(for: .seconds(1.5))
                    guard !Task.isCancelled, await Nearby.opens(.hostPort(host: .init(tailnet), port: port), timeout: 6) else { return nil }
                    var away = c
                    away.host = tailnet
                    return .at(away)
                }
            }
            for await result in group {
                if let result {
                    group.cancelAll()
                    return result
                }
            }
            return nil
        }
        Trace.log("find \(c.name): \(found.map { "\($0)" } ?? "nowhere")")
        switch found {
        case let .at(moved):
            PeerRelay.shared.stop(c.id) // on a network again: no relay
            return moved
        case let .nearby(endpoint, link):
            return (try? await PeerRelay.shared.start(c.id, to: endpoint, over: link)) != nil ? c : nil
        case nil:
            PeerRelay.shared.stop(c.id)
            return nil
        }
    }
}

/// A development build's record of how computers were looked for (Caches/ferry-trace.log), to read off a device.
nonisolated enum Trace {
    static func log(_ line: @autoclosure () -> String) {
        #if DEBUG
        let text = "\(Date().formatted(.iso8601.time(includingFractionalSeconds: true))) \(line())\n"
        let url = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("ferry-trace.log")
        lock.withLock {
            if let h = try? FileHandle(forWritingTo: url) {
                h.seekToEndOfFile()
                h.write(Data(text.utf8))
                try? h.close()
            } else {
                try? Data(text.utf8).write(to: url)
            }
        }
        #endif
    }
    private static let lock = NSLock()
}

/// True the first time only.
nonisolated final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var done = false
    func take() -> Bool {
        lock.withLock {
            defer { done = true }
            return !done
        }
    }
}
