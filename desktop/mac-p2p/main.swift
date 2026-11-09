// ferry-p2p: Ferry on Apple's peer-to-peer Wi-Fi (awdl0, what AirDrop uses), so an iPhone nearby reaches
// this Mac with no network in common. Ferry starts it (server/p2p_darwin.go).
//
// Only Network framework can take connections over that link (Apple's TN3151: "the service's listener must
// be run by a peer-to-peer aware API"), so this listens there, announces Ferry's secure address as
// _ferrys._tcp, and passes each connection's bytes to Ferry over a Unix socket in Ferry's data folder.
// Ferry treats what comes that way like any other device: encrypted end to end, and only with a paired
// device's key. It quits when Ferry does (its standard input closes).
//
// "connect" is the other way, Mac to Mac: a connection to the paired Mac with that id, found over the same
// link, carried on standard input and output (Ferry speaks TLS over it, pinned to that Mac's authority).
//
//   ferry-p2p <name> <id> <fingerprint> <socket>
//   ferry-p2p connect <id>
import Foundation
import Network

let args = CommandLine.arguments
let queue = DispatchQueue(label: "app.ferry.p2p")

func say(_ line: String) { FileHandle.standardError.write(Data("ferry-p2p: \(line)\n".utf8)) }

if args.count == 3, args[1] == "connect" {
    connect(to: args[2])
}
guard args.count == 5 else {
    say("usage: ferry-p2p <name> <id> <fingerprint> <socket> | ferry-p2p connect <id>")
    exit(2)
}
let (name, id, fingerprint, socket) = (args[1], args[2], args[3], args[4])

// Quit when Ferry does.
let stdin = DispatchSource.makeReadSource(fileDescriptor: STDIN_FILENO, queue: queue)
stdin.setEventHandler {
    var byte: UInt8 = 0
    if read(STDIN_FILENO, &byte, 1) <= 0 { exit(0) }
}
stdin.resume()

let params = NWParameters.tcp
params.includePeerToPeer = true
let listener: NWListener
do {
    listener = try NWListener(using: params)
} catch {
    say("can't listen: \(error)")
    exit(1)
}
var txt = NWTXTRecord()
txt["v"] = "1"
txt["id"] = id
txt["fp"] = fingerprint
listener.service = NWListener.Service(name: name, type: "_ferrys._tcp", domain: nil, txtRecord: txt)
listener.stateUpdateHandler = { state in
    if case let .failed(error) = state {
        say("stopped listening: \(error)")
        exit(1) // Ferry starts it again
    }
}
listener.newConnectionHandler = { inbound in
    let ferry = NWConnection(to: .unix(path: socket), using: .tcp)
    let stop = { inbound.cancel(); ferry.cancel() }
    var started = false
    ferry.stateUpdateHandler = { state in
        switch state {
        case .ready:
            guard !started else { return }
            started = true
            pipe(inbound, ferry, stop)
            pipe(ferry, inbound, stop)
        case .failed, .cancelled, .waiting:
            stop()
        default: break
        }
    }
    inbound.stateUpdateHandler = { state in
        if case .failed = state { stop() }
    }
    inbound.start(queue: queue)
    ferry.start(queue: queue)
}
listener.start(queue: queue)

// A Mac only answers on the peer-to-peer link while something on it looks over the link, so a search runs
// for as long as this does (what keeps the link awake costs a little battery and Wi-Fi speed).
let awake: NWBrowser = {
    let params = NWParameters()
    params.includePeerToPeer = true
    let browser = NWBrowser(for: .bonjour(type: "_ferrys._tcp", domain: nil), using: params)
    browser.stateUpdateHandler = { state in
        if case let .failed(error) = state { say("stopped looking: \(error)") }
    }
    browser.start(queue: queue)
    return browser
}()

/// Copies everything from one connection to the other, one piece at a time (the next is read once the last
/// has gone), and closes both when either side ends.
func pipe(_ from: NWConnection, _ to: NWConnection, _ stop: @escaping () -> Void) {
    from.receive(minimumIncompleteLength: 1, maximumLength: 256 * 1024) { data, _, ended, error in
        let next = { ended || error != nil ? stop() : pipe(from, to, stop) }
        if let data, !data.isEmpty {
            to.send(content: data, completion: .contentProcessed { sent in sent == nil ? next() : stop() })
        } else {
            next()
        }
    }
}

/// Finds the Mac with this id (_ferrys._tcp over peer-to-peer Wi-Fi, or a network), connects, and carries the
/// connection on standard input and output until either side ends. The search runs throughout: the system
/// keeps the peer-to-peer link up only while something looks over it or uses it.
func connect(to id: String) -> Never {
    let params = NWParameters()
    params.includePeerToPeer = true
    let browser = NWBrowser(for: .bonjourWithTXTRecord(type: "_ferrys._tcp", domain: nil), using: params)
    var connection: NWConnection?
    browser.browseResultsChangedHandler = { results, _ in
        guard connection == nil, let found = results.first(where: {
            if case let .bonjour(txt) = $0.metadata { return txt.dictionary["id"] == id }
            return false
        }) else { return }
        let tcp = NWParameters.tcp
        tcp.includePeerToPeer = true
        // Seen only on the peer-to-peer link: take it (left to itself the system may try another way).
        if found.interfaces.allSatisfy({ $0.name.hasPrefix("awdl") || $0.name.hasPrefix("llw") }), let link = found.interfaces.first {
            tcp.requiredInterface = link
        }
        let c = NWConnection(to: found.endpoint, using: tcp)
        connection = c
        c.stateUpdateHandler = { state in
            switch state {
            case .ready: carry(c)
            case .failed, .cancelled: exit(1)
            default: break
            }
        }
        c.start(queue: queue)
    }
    browser.start(queue: queue)
    queue.asyncAfter(deadline: .now() + 10) { if connection?.state != .ready { exit(1) } }
    dispatchMain()
}

/// Standard input to the connection (each piece once the last has gone) and the connection to standard output.
func carry(_ c: NWConnection) {
    Thread.detachNewThread {
        var buffer = [UInt8](repeating: 0, count: 64 * 1024)
        while true {
            let n = read(STDIN_FILENO, &buffer, buffer.count)
            if n <= 0 {
                c.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .idempotent)
                return
            }
            let sent = DispatchSemaphore(value: 0)
            c.send(content: Data(buffer[0..<n]), completion: .contentProcessed { error in
                if error != nil { exit(0) }
                sent.signal()
            })
            sent.wait()
        }
    }
    func receive() {
        c.receive(minimumIncompleteLength: 1, maximumLength: 256 * 1024) { data, _, ended, error in
            if let data, !data.isEmpty { FileHandle.standardOutput.write(data) }
            if ended || error != nil { exit(0) }
            receive()
        }
    }
    receive()
}

dispatchMain()
