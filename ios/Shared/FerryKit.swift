import CryptoKit
import Foundation
import Security
import UniformTypeIdentifiers

/// A computer this phone is paired with: where Ferry runs on it, its certificate authority (the only
/// one trusted for its secure address) and this phone's own key for it.
nonisolated struct FerryComputer: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var name: String
    var host: String      // its .local name or address
    var port: Int         // the page (plain HTTP)
    var tlsPort: Int?     // the secure address
    var ca: Data?         // DER
    var token: String

    /// Everything goes over the secure address whenever the computer has one.
    var secure: Bool { tlsPort != nil && ca != nil }

    var base: URL {
        var c = URLComponents()
        c.scheme = secure ? "https" : "http"
        c.host = host
        c.port = secure ? tlsPort : port
        return c.url!
    }

    /// Lets this phone trust the computer's secure address.
    func register() {
        if let ca, let tlsPort { TrustStore.shared.trust(ca, host: host, port: tlsPort) }
    }
}

/// The pairing older versions kept: one Mac and its code. Read once, to move it to a key of its own.
nonisolated struct FerryMac: Codable, Equatable, Sendable {
    var name: String
    var url: URL
    var fallbackURL: URL?
    var code: String
}

/// One file in the Mac's Ferry folder.
nonisolated struct FerryFile: Codable, Identifiable, Hashable, Sendable {
    let name: String
    let size: Int64
    let added: Int64      // unix ms
    let kind: String      // image, video, audio, pdf, file
    let from: String
    let v: String
    var id: String { name }
    var addedDate: Date { Date(timeIntervalSince1970: Double(added) / 1000) }
    var isMedia: Bool { kind == "image" || kind == "video" }
    var ext: String {
        let e = (name as NSString).pathExtension.uppercased()
        return e.isEmpty ? "FILE" : String(e.prefix(4))
    }
}

nonisolated struct FerryInfo: Codable, Sendable {
    var id: String?
    let host: String
    /// What the computer calls its bin ("Trash", "Recycle Bin"); older servers don't say.
    var trash: String?
}

nonisolated enum FerryError: LocalizedError {
    case notPaired, wrongCode, removed, notThisComputer, unreachable, server(Int, String)
    var errorDescription: String? {
        switch self {
        case .notPaired: "Ferry isn't connected to a computer yet."
        case .wrongCode: "That code doesn't match. Check the one on your computer."
        case .removed: "This iPhone was removed from the computer's list."
        case .notThisComputer: "That isn't the computer whose code you scanned. Check you're on the same Wi-Fi as it, and scan again."
        case .unreachable: "Can't reach your computer. Is Ferry running there, and is this iPhone on the same Wi-Fi?"
        case let .server(status, text): text.isEmpty ? "Your computer said no (\(status))." : text
        }
    }
}

// MARK: Pairing

/// The computers this phone is paired with, in a keychain group the app and its share extension
/// both read ("<team>.app.ferry.shared", named in each Info.plist).
nonisolated enum Pairing {
    static let group = Bundle.main.object(forInfoDictionaryKey: "FerryKeychainGroup") as? String ?? "app.ferry.shared"

    struct Saved: Codable, Equatable, Sendable {
        var computers: [FerryComputer] = []
        var current: String?
        /// The computer in use: the chosen one, else the first.
        var active: FerryComputer? { computers.first { $0.id == current } ?? computers.first }
    }

    static func load() -> Saved {
        read("computers").flatMap { try? JSONDecoder().decode(Saved.self, from: $0) } ?? Saved()
    }

    static func save(_ saved: Saved) { write("computers", try? JSONEncoder().encode(saved)) }

    static func legacy() -> FerryMac? { read("paired-mac").flatMap { try? JSONDecoder().decode(FerryMac.self, from: $0) } }
    static func clearLegacy() { write("paired-mac", nil) }
    #if DEBUG
    static func saveLegacy(_ mac: FerryMac) { write("paired-mac", try? JSONEncoder().encode(mac)) }
    #endif

    private static func read(_ account: String) -> Data? {
        let q: [CFString: Any] = [kSecClass: kSecClassGenericPassword, kSecAttrAccount: account, kSecAttrAccessGroup: group,
                                  kSecReturnData: true, kSecMatchLimit: kSecMatchLimitOne]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    private static func write(_ account: String, _ data: Data?) {
        let base: [CFString: Any] = [kSecClass: kSecClassGenericPassword, kSecAttrAccount: account, kSecAttrAccessGroup: group]
        SecItemDelete(base as CFDictionary)
        guard let data else { return }
        var add = base
        add[kSecValueData] = data
        add[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlock
        SecItemAdd(add as CFDictionary, nil)
    }

    /// What the QR code on Ferry's computer page carries: http://<computer>:7070/?k=<code>&f=<fingerprint>&s=<secure port>.
    struct Scanned: Equatable, Sendable {
        var host: String
        var port: Int
        var code: String
        var fingerprint: String?
        var tlsPort: Int?
    }

    static func parse(_ text: String) -> Scanned? {
        guard let parts = URLComponents(string: text.trimmingCharacters(in: .whitespacesAndNewlines)),
              parts.scheme == "http" || parts.scheme == "https", let host = parts.host, !host.isEmpty,
              let code = parts.queryItems?.first(where: { $0.name == "k" })?.value, code.count >= 6 else { return nil }
        let item = { (name: String) in parts.queryItems?.first { $0.name == name }?.value }
        return Scanned(host: host, port: parts.port ?? (parts.scheme == "https" ? 443 : 80), code: normalise(code),
                       fingerprint: item("f"), tlsPort: item("s").flatMap(Int.init))
    }

    static func normalise(_ code: String) -> String {
        code.lowercased().filter { $0.isLetter || $0.isNumber }
    }

    /// How the QR code names a certificate authority: base64url SHA-256, first 128 bits.
    static func fingerprint(_ der: Data) -> String {
        let b64 = Data(SHA256.hash(data: der)).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        return String(b64.prefix(22))
    }
}

// MARK: Trust

/// Each paired computer's own certificate authority, the only one trusted for its secure address,
/// so the phone talks to that computer encrypted, and never to something pretending to be it.
nonisolated final class TrustStore: NSObject, URLSessionDelegate, @unchecked Sendable {
    static let shared = TrustStore()
    private let lock = NSLock()
    private var anchors: [String: SecCertificate] = [:]

    func trust(_ ca: Data, host: String, port: Int) {
        guard let cert = SecCertificateCreateWithData(nil, ca as CFData) else { return }
        lock.withLock { anchors["\(host.lowercased()):\(port)"] = cert }
    }

    /// Accepts a computer's secure address only with a certificate from its own authority.
    func answer(_ challenge: URLAuthenticationChallenge) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let trust = space.serverTrust else {
            return (.performDefaultHandling, nil)
        }
        guard let anchor = lock.withLock({ anchors["\(space.host.lowercased()):\(space.port)"] }) else {
            return (.performDefaultHandling, nil)
        }
        SecTrustSetAnchorCertificates(trust, [anchor] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        guard SecTrustEvaluateWithError(trust, nil) else { return (.cancelAuthenticationChallenge, nil) }
        return (.useCredential, URLCredential(trust: trust))
    }

    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge) async
        -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        answer(challenge)
    }
}

// MARK: Talking to the computer

/// Ferry on a paired computer. Requests carry this phone's key; changes carry Ferry's header.
nonisolated struct FerryAPI: Sendable {
    let computer: FerryComputer
    var base: URL { computer.base }

    init(_ computer: FerryComputer) {
        self.computer = computer
        computer.register()
    }

    static let session: URLSession = {
        let c = URLSessionConfiguration.default
        c.timeoutIntervalForRequest = 20
        c.timeoutIntervalForResource = 60 * 60 * 6
        c.waitsForConnectivity = false
        c.httpCookieStorage = nil
        return URLSession(configuration: c, delegate: TrustStore.shared, delegateQueue: nil)
    }()

    func url(_ path: String, _ query: [String: String] = [:]) -> URL {
        var c = URLComponents(url: base, resolvingAgainstBaseURL: false)!
        c.percentEncodedPath = path
        if !query.isEmpty { c.queryItems = query.sorted { $0.key < $1.key }.map { URLQueryItem(name: $0.key, value: $0.value) } }
        return c.url!
    }

    static func escape(_ name: String) -> String {
        name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/?#"))) ?? name
    }

    func request(_ path: String, method: String = "GET", query: [String: String] = [:]) -> URLRequest {
        var r = URLRequest(url: url(path, query))
        r.httpMethod = method
        r.setValue("Ferry \(computer.token)", forHTTPHeaderField: "Authorization")
        if method != "GET" { r.setValue("1", forHTTPHeaderField: "X-Ferry") }
        return r
    }

    /// Headers for players and image loaders that make their own requests.
    var authHeaders: [String: String] { ["Authorization": "Ferry \(computer.token)"] }

    func fileURL(_ f: FerryFile) -> URL { url("/f/" + Self.escape(f.name)) }
    func thumbURL(_ f: FerryFile, large: Bool = false) -> URL {
        url("/thumb/" + Self.escape(f.name), large ? ["s": "large", "v": f.v] : ["v": f.v])
    }

    private func check(_ response: URLResponse, _ data: Data) throws {
        guard let http = response as? HTTPURLResponse else { throw FerryError.unreachable }
        if http.statusCode == 401 { throw FerryError.removed }
        if !(200..<300).contains(http.statusCode) {
            throw FerryError.server(http.statusCode, String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines))
        }
    }

    private func send(_ r: URLRequest) async throws -> Data {
        do {
            let (data, response) = try await Self.session.data(for: r)
            try check(response, data)
            return data
        } catch let e as FerryError {
            throw e
        } catch {
            throw FerryError.unreachable
        }
    }

    func info() async throws -> FerryInfo {
        try JSONDecoder().decode(FerryInfo.self, from: await send(request("/api/info", query: ["device": "iPhone"])))
    }

    func files() async throws -> [FerryFile] {
        try JSONDecoder().decode([FerryFile].self, from: await send(request("/api/files")))
    }

    func delete(_ f: FerryFile) async throws {
        _ = try await send(request("/api/files/" + Self.escape(f.name), method: "DELETE"))
    }

    /// Sends a file to the Mac, streaming it from disk, reporting progress (0…1).
    func upload(_ file: URL, name: String, progress: @escaping @Sendable (Double) -> Void) async throws -> FerryFile {
        var r = request("/api/upload", method: "PUT", query: ["name": name, "device": "iPhone"])
        r.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
        r.timeoutInterval = 60 * 30
        let delegate = UploadProgress(progress)
        do {
            let (data, response) = try await Self.session.upload(for: r, fromFile: file, delegate: delegate)
            try check(response, data)
            return try JSONDecoder().decode(FerryFile.self, from: data)
        } catch let e as FerryError {
            throw e
        } catch let e as URLError where e.code == .cancelled {
            throw CancellationError()
        } catch {
            throw FerryError.unreachable
        }
    }

    /// Fetches a file to a temporary place under its own name (for Photos, sharing, Quick Look).
    func download(_ f: FerryFile) async throws -> URL {
        do {
            let (tmp, response) = try await Self.session.download(for: request("/f/" + Self.escape(f.name)))
            try check(response, Data())
            let dir = FileManager.default.temporaryDirectory.appendingPathComponent("ferry-\(f.v)", isDirectory: true)
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            let dest = dir.appendingPathComponent(f.name)
            try? FileManager.default.removeItem(at: dest)
            try FileManager.default.moveItem(at: tmp, to: dest)
            return dest
        } catch let e as FerryError {
            throw e
        } catch {
            throw FerryError.unreachable
        }
    }

    /// The Mac's live list: a fresh copy of every file each time anything changes.
    func events() -> AsyncThrowingStream<[FerryFile], Error> {
        let r = request("/api/events", query: ["device": "iPhone"])
        return AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var stream = r
                    stream.timeoutInterval = 60 * 60 * 24
                    let (bytes, response) = try await Self.session.bytes(for: stream)
                    if (response as? HTTPURLResponse)?.statusCode == 401 { throw FerryError.removed }
                    var event = "", data = ""
                    for try await line in bytes.lines {
                        if line.hasPrefix("event:") { event = line.dropFirst(6).trimmingCharacters(in: .whitespaces) }
                        else if line.hasPrefix("data:") { data = line.dropFirst(5).trimmingCharacters(in: .whitespaces) }
                        else if line.isEmpty || line.hasPrefix(":") { continue }
                        // A server-sent event ends at a blank line, which `lines` skips: handle it once both parts are in.
                        guard !event.isEmpty, !data.isEmpty else { continue }
                        defer { event = ""; data = "" }
                        let json = Data(data.utf8)
                        if event == "files", let files = try? JSONDecoder().decode([FerryFile].self, from: json) {
                            continuation.yield(files)
                        } else if event == "hello", let hello = try? JSONDecoder().decode(Hello.self, from: json) {
                            continuation.yield(hello.files)
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private struct Hello: Decodable { let files: [FerryFile] }

    // MARK: Pairing

    private struct PairReply: Decodable {
        let token: String
        let id: String
        let name: String
        let tlsPort: Int?
        let ca: String?
    }

    /// Pairs with what the QR code carries. With a fingerprint in it, the phone fetches the computer's
    /// certificate authority, checks it's the one in the code, and sends the code only over the secure address.
    static func pair(_ scanned: Pairing.Scanned, device: String, kind: String, previous: String? = nil) async throws -> FerryComputer {
        try await pair(host: scanned.host, port: scanned.port, tlsPort: scanned.tlsPort, fingerprint: scanned.fingerprint,
                       code: scanned.code, device: device, kind: kind, previous: previous)
    }

    /// Pairs with a computer at host:port. With a secure port, the code goes only over it, to the certificate
    /// authority the computer hands out (checked against the fingerprint when there is one). `previous` is this
    /// phone's old key there, when pairing again, so the computer replaces its entry.
    static func pair(host: String, port: Int, tlsPort: Int?, fingerprint: String?, code: String, device: String,
                     kind: String, previous: String? = nil) async throws -> FerryComputer {
        guard let tlsPort else {
            return try await exchange(host: host, port: port, secure: nil, code: code, device: device, kind: kind, previous: previous)
        }
        let ca = try await authority(host: host, port: port)
        if let fingerprint, Pairing.fingerprint(ca) != fingerprint { throw FerryError.notThisComputer }
        return try await exchange(host: host, port: port, secure: (tlsPort, ca), code: code, device: device, kind: kind, previous: previous)
    }

    /// Tells the computer this phone forgot it, so it drops the phone from its list too.
    func unpairSelf() async {
        _ = try? await send(request("/api/devices/self", method: "DELETE"))
    }

    /// Moves an older pairing (the code itself) to a key of this phone's own.
    static func migrate(_ old: FerryMac, device: String, kind: String) async throws -> FerryComputer {
        for url in [old.url, old.fallbackURL].compactMap({ $0 }) {
            guard let host = url.host() else { continue }
            do {
                var c = try await exchange(host: host, port: url.port ?? 80, secure: nil, code: old.code, device: device, kind: kind)
                if c.name.isEmpty { c.name = old.name }
                return c
            } catch FerryError.unreachable {
                continue
            }
        }
        throw FerryError.unreachable
    }

    private static func authority(host: String, port: Int) async throws -> Data {
        var c = URLComponents()
        c.scheme = "http"
        c.host = host
        c.port = port
        c.path = "/ferry.cer"
        do {
            let (data, response) = try await session.data(from: c.url!)
            guard (response as? HTTPURLResponse)?.statusCode == 200, SecCertificateCreateWithData(nil, data as CFData) != nil else {
                throw FerryError.server((response as? HTTPURLResponse)?.statusCode ?? 0, "Your computer didn't offer a secure connection.")
            }
            return data
        } catch let e as FerryError {
            throw e
        } catch {
            throw FerryError.unreachable
        }
    }

    private static func exchange(host: String, port: Int, secure: (port: Int, ca: Data)?, code: String, device: String,
                                 kind: String, previous: String? = nil) async throws -> FerryComputer {
        var c = URLComponents()
        c.scheme = secure == nil ? "http" : "https"
        c.host = host
        c.port = secure?.port ?? port
        c.path = "/api/pair"
        if let secure { TrustStore.shared.trust(secure.ca, host: host, port: secure.port) }
        var r = URLRequest(url: c.url!)
        r.httpMethod = "POST"
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        var body = ["code": Pairing.normalise(code), "name": device, "kind": kind]
        if let previous { body["previous"] = previous }
        r.httpBody = try JSONEncoder().encode(body)
        let data: Data, response: URLResponse
        do {
            (data, response) = try await session.data(for: r)
        } catch let e as URLError where e.code == .cancelled || e.code.rawValue <= URLError.Code.secureConnectionFailed.rawValue
            && e.code.rawValue >= URLError.Code.clientCertificateRequired.rawValue {
            throw FerryError.notThisComputer // the secure address answered with someone else's certificate
        } catch {
            throw FerryError.unreachable
        }
        guard let http = response as? HTTPURLResponse else { throw FerryError.unreachable }
        if http.statusCode == 401 { throw FerryError.wrongCode }
        guard (200..<300).contains(http.statusCode), let reply = try? JSONDecoder().decode(PairReply.self, from: data) else {
            throw FerryError.server(http.statusCode, "Your computer's Ferry needs updating to pair.")
        }
        let ca = reply.ca.flatMap { Data(base64Encoded: $0) } ?? secure?.ca
        return FerryComputer(id: reply.id, name: reply.name, host: host, port: port, tlsPort: reply.tlsPort ?? secure?.port,
                             ca: ca, token: reply.token)
    }
}

nonisolated final class UploadProgress: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    let report: @Sendable (Double) -> Void
    init(_ report: @escaping @Sendable (Double) -> Void) { self.report = report }
    func urlSession(_ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge) async
        -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        TrustStore.shared.answer(challenge)
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64, totalBytesSent: Int64,
                    totalBytesExpectedToSend: Int64) {
        guard totalBytesExpectedToSend > 0 else { return }
        report(Double(totalBytesSent) / Double(totalBytesExpectedToSend))
    }
}

/// A file name for something shared or picked without one.
nonisolated func suggestedName(_ base: String?, type: UTType?) -> String {
    let ext = type?.preferredFilenameExtension ?? "dat"
    let stem = (base?.isEmpty == false ? base! : (type?.conforms(to: .movie) == true ? "video" : "image"))
    return (stem as NSString).pathExtension.isEmpty ? "\(stem).\(ext)" : stem
}
