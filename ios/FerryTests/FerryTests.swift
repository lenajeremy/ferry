import Foundation
import Testing
@testable import Ferry

@Suite("Pairing and the computer's addresses")
struct FerryTests {
    @Test func readsTheComputersQRCode() throws {
        let s = try #require(Pairing.parse("http://Ferry-Test-MacBook-Pro4.local:7070/?k=k7m2q9xa&f=gZ_x2YJh2S2DD_40s0j8a8&s=7071"))
        #expect(s.host == "Ferry-Test-MacBook-Pro4.local" && s.port == 7070 && s.code == "k7m2q9xa")
        #expect(s.fingerprint == "gZ_x2YJh2S2DD_40s0j8a8" && s.tlsPort == 7071)
        // Codes from before the fingerprint still pair (over the plain address).
        let old = try #require(Pairing.parse("http://192.168.1.20:7070/?k=k7m2q9xa"))
        #expect(old.fingerprint == nil && old.tlsPort == nil)
    }

    @Test func fingerprintsMatchFerrysComputers() {
        // The same bytes Ferry's Go tests use: base64url SHA-256, first 22 characters.
        #expect(Pairing.fingerprint(Data("authority".utf8)) == "j3b9UBu2jvcfTidrwo8pvO")
    }

    @Test func ignoresOtherCodes() {
        #expect(Pairing.parse("https://example.com/") == nil)
        #expect(Pairing.parse("hello") == nil)
        #expect(Pairing.parse("http://mac.local:7070/?k=ab") == nil)
    }

    @Test func typedCodesAreForgiving() {
        #expect(Pairing.normalise(" K7M2 q9xa ") == "k7m2q9xa")
        #expect(Pairing.normalise("k7m2-q9xa") == "k7m2q9xa")
    }

    @Test func requestsCarryThePhonesKeyAndGoSecurely() {
        let c = FerryComputer(id: "c1", name: "Mac", host: "mac.local", port: 7070, tlsPort: 7071, ca: Data([1]), token: "tok")
        let api = FerryAPI(c)
        let f = FerryFile(name: "Holiday #1 / 50%.jpg", size: 1, added: 0, kind: "image", from: "Mac", v: "1")
        #expect(api.fileURL(f).absoluteString == "https://mac.local:7071/f/Holiday%20%231%20%2F%2050%25.jpg")
        let r = api.request("/api/files/x", method: "DELETE")
        #expect(r.value(forHTTPHeaderField: "Authorization") == "Ferry tok")
        #expect(r.value(forHTTPHeaderField: "X-Ferry") == "1")
        #expect(api.request("/api/files").value(forHTTPHeaderField: "X-Ferry") == nil)
        // A computer paired without a secure address stays on the plain one.
        let plain = FerryComputer(id: "c2", name: "PC", host: "192.168.0.9", port: 7070, tlsPort: nil, ca: nil, token: "t")
        #expect(FerryAPI(plain).base.absoluteString == "http://192.168.0.9:7070")
    }

    @Test func theChosenComputerIsInUse() {
        let a = FerryComputer(id: "a", name: "A", host: "a.local", port: 7070, tlsPort: nil, ca: nil, token: "1")
        let b = FerryComputer(id: "b", name: "B", host: "b.local", port: 7070, tlsPort: nil, ca: nil, token: "2")
        #expect(Pairing.Saved(computers: [a, b], current: "b").active?.id == "b")
        #expect(Pairing.Saved(computers: [a, b], current: "gone").active?.id == "a")
        #expect(Pairing.Saved().active == nil)
    }

    @Test func describesWhatArrived() {
        #expect(describe(["image"]) == "1 photo")
        #expect(describe(["video", "video"]) == "2 videos")
        #expect(describe(["image", "video"]) == "2 files")
    }

    @Test func dayLabels() {
        let now = Date()
        #expect(FerryFile.dayLabel(now, now: now) == "Today")
        #expect(FerryFile.dayLabel(now.addingTimeInterval(-86_400), now: now) == "Yesterday")
    }
}
