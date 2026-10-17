import SwiftUI

@main
struct FerryApp: App {
    @State private var library = Library()

    var body: some Scene {
        WindowGroup {
            Group {
                if library.migrating {
                    ProgressView("Connecting…")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Theme.bg)
                } else if library.computer == nil {
                    PairView()
                } else {
                    LibraryView()
                        .task { library.start() }
                }
            }
            .environment(library)
            .tint(Theme.accent)
            .animation(.smooth, value: library.computer?.id)
            #if DEBUG
            .task { DebugLaunch.apply(library) }
            #endif
        }
    }
}

#if DEBUG
/// `-ferryQR <link>` pairs on launch as if the code had been scanned; `-ferryURL http://… -ferryCode …`
/// as if typed (Simulator screenshots, no camera there).
enum DebugLaunch {
    static func apply(_ library: Library) {
        let d = UserDefaults.standard
        // `-ferryLegacyURL http://… -ferryLegacyCode …`: a pairing as older versions kept it, to try moving it.
        if library.computers.isEmpty, let raw = d.string(forKey: "ferryLegacyURL"), let url = URL(string: raw),
           let code = d.string(forKey: "ferryLegacyCode") {
            Pairing.saveLegacy(FerryMac(name: "MacBook Pro", url: url, fallbackURL: nil, code: code))
            library.migrate()
            return
        }
        if let link = d.string(forKey: "ferryQR"), let scanned = Pairing.parse(link), !library.computers.contains(where: { $0.host == scanned.host && $0.port == scanned.port }) {
            Task {
                do { library.pair(try await FerryAPI.pair(scanned, device: Library.deviceName, kind: Library.deviceKind)) }
                catch { library.notice = (error as? FerryError)?.errorDescription ?? error.localizedDescription }
            }
            return
        }
        guard library.computer == nil, let raw = d.string(forKey: "ferryURL"), let url = URL(string: raw), let host = url.host(),
              let code = d.string(forKey: "ferryCode") else { return }
        Task {
            if let c = try? await FerryAPI.pair(host: host, port: url.port ?? 80, tlsPort: nil, fingerprint: nil, code: code,
                                                device: Library.deviceName, kind: Library.deviceKind) { library.pair(c) }
        }
    }
}
#endif
