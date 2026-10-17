import Network
import SwiftUI
import VisionKit

/// Computers running Ferry on this Wi-Fi, found by Bonjour (_ferry._tcp).
@Observable
final class Discovery {
    nonisolated struct Found: Identifiable, Hashable, Sendable {
        let name: String
        let host: String
        let port: Int
        let tlsPort: Int?
        let fingerprint: String?
        let computerID: String?
        var id: String { name }
    }

    private(set) var found: [Found] = []
    private var browser: NWBrowser?

    func start() {
        guard browser == nil else { return }
        let b = NWBrowser(for: .bonjourWithTXTRecord(type: "_ferry._tcp", domain: nil), using: .tcp)
        b.browseResultsChangedHandler = { [weak self] results, _ in
            let found = results.compactMap(Discovery.found).sorted { $0.name < $1.name }
            // Started on the main queue, so this is the main actor.
            MainActor.assumeIsolated { self?.found = found }
        }
        b.start(queue: .main)
        browser = b
    }

    func stop() {
        browser?.cancel()
        browser = nil
    }

    private nonisolated static func found(_ result: NWBrowser.Result) -> Found? {
        guard case let .service(name, _, _, _) = result.endpoint,
              case let .bonjour(txt) = result.metadata,
              let host = txt["host"], let port = txt["port"].flatMap(Int.init) else { return nil }
        return Found(name: name, host: host, port: port, tlsPort: txt["tls"].flatMap(Int.init), fingerprint: txt["fp"], computerID: txt["id"])
    }
}

/// The first screen (and "Add a computer"): find the computer, then scan its code (or type it).
struct PairView: View {
    /// Shown as a sheet over the library, with a close button.
    var adding = false
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var discovery = Discovery()
    @State private var scanning = false
    @State private var typing: Discovery.Found?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                VStack(alignment: .leading, spacing: 14) {
                    Image("Logo")
                        .resizable()
                        .frame(width: 64, height: 64)
                        .clipShape(RoundedRectangle(cornerRadius: 15, style: .continuous))
                    Text(adding ? "Add a computer" : "Connect to your computer")
                        .font(.system(size: 32, weight: .bold))
                        .foregroundStyle(Theme.ink)
                    Text("Open Ferry on your Mac or PC, then scan the code on it. Photos, videos and files go both ways after that, encrypted.")
                        .font(.system(size: 17))
                        .foregroundStyle(Theme.ink2)
                        .fixedSize(horizontal: false, vertical: true)
                    if let notice = library.notice {
                        Label(notice, systemImage: "exclamationmark.circle")
                            .font(.system(size: 15, weight: .medium))
                            .foregroundStyle(Theme.danger)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.top, adding ? 8 : 36)

                Button {
                    scanning = true
                } label: {
                    Label("Scan the code", systemImage: "qrcode.viewfinder")
                }
                .buttonStyle(InkButtonStyle())

                VStack(alignment: .leading, spacing: 10) {
                    Text("ON THIS WI-FI")
                        .font(.system(size: 12, weight: .semibold))
                        .tracking(0.8)
                        .foregroundStyle(Theme.ink3)
                        .padding(.leading, 4)
                    VStack(spacing: 0) {
                        if discovery.found.isEmpty {
                            HStack(spacing: 12) {
                                ProgressView()
                                Text("Looking for Ferry on this Wi-Fi…")
                                    .font(.system(size: 16))
                                    .foregroundStyle(Theme.ink2)
                                Spacer()
                            }
                            .padding(16)
                        }
                        ForEach(Array(discovery.found.enumerated()), id: \.element) { i, mac in
                            let paired = library.computers.contains { $0.id == mac.computerID }
                            if i > 0 { Divider().padding(.leading, 60) }
                            Button {
                                if paired, let id = mac.computerID { library.use(id); if adding { dismiss() } } else { typing = mac }
                            } label: {
                                HStack(spacing: 14) {
                                    Image(systemName: "laptopcomputer")
                                        .font(.system(size: 17))
                                        .foregroundStyle(Theme.ink)
                                        .frame(width: 32)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(mac.name).font(.system(size: 17, weight: .medium)).foregroundStyle(Theme.ink)
                                        Text(paired ? (mac.computerID == library.computer?.id ? "Connected" : "Paired · switch to it") : "Type its code")
                                            .font(.system(size: 14)).foregroundStyle(Theme.ink2)
                                    }
                                    Spacer()
                                    Image(systemName: "chevron.right")
                                        .font(.system(size: 13, weight: .semibold))
                                        .foregroundStyle(Theme.ink3)
                                }
                                .padding(.horizontal, 16)
                                .padding(.vertical, 13)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                    Text("Not showing up? Check Ferry is running on your computer and that both are on the same Wi-Fi. Scanning the code always works.")
                        .font(.system(size: 13))
                        .foregroundStyle(Theme.ink3)
                        .padding(.horizontal, 4)
                }
            }
            .padding(.horizontal, 20)
            .padding(.bottom, 40)
        }
        .scrollBounceBehavior(.basedOnSize)
        .background(Theme.bg)
        .safeAreaInset(edge: .top) {
            if adding {
                HStack {
                    Button("Close", systemImage: "xmark") { dismiss() }
                        .labelStyle(.iconOnly)
                        .font(.system(size: 17, weight: .semibold))
                        .frame(width: 44, height: 44)
                        .glassEffect(.regular.interactive(), in: .circle)
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.top, 12)
            }
        }
        .onChange(of: library.computer?.id) { if adding { dismiss() } }
        .onAppear { discovery.start() }
        .onDisappear { discovery.stop() }
        .sheet(isPresented: $scanning) { ScanSheet() }
        .sheet(item: $typing) { CodeSheet(mac: $0) }
    }
}

/// Scans the QR code on Ferry's computer page, which carries the address, the code and the fingerprint
/// of the computer's certificate.
struct ScanSheet: View {
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            ZStack(alignment: .bottom) {
                if DataScannerViewController.isSupported && DataScannerViewController.isAvailable {
                    QRScanner { text in Task { await found(text) } }
                        .ignoresSafeArea()
                } else {
                    ContentUnavailableView("The camera isn't available", systemImage: "camera",
                                           description: Text("Allow Ferry to use the camera in Settings, or type the code instead."))
                }
                Text(busy ? "Connecting…" : problem ?? "Point the camera at the code on your computer's screen")
                    .font(.system(size: 15, weight: .medium))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 18)
                    .padding(.vertical, 12)
                    .background(.black.opacity(0.6), in: Capsule())
                    .padding(.bottom, 40)
            }
            .navigationTitle("Scan the code")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .topBarLeading) { Button("Close", systemImage: "xmark") { dismiss() } } }
        }
    }

    private func found(_ text: String) async {
        guard !busy, let scanned = Pairing.parse(text) else {
            if !busy { problem = "That's not Ferry's code" }
            return
        }
        busy = true
        do {
            library.pair(try await FerryAPI.pair(scanned, device: Library.deviceName, kind: Library.deviceKind,
                                                 previous: library.previousKey(host: scanned.host, port: scanned.port)))
            dismiss()
        } catch {
            problem = (error as? FerryError)?.errorDescription ?? error.localizedDescription
        }
        busy = false
    }
}

struct QRScanner: UIViewControllerRepresentable {
    let onCode: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], qualityLevel: .balanced,
                                           isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        try? vc.startScanning()
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(onCode) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let onCode: (String) -> Void
        private var last = ""
        init(_ onCode: @escaping (String) -> Void) { self.onCode = onCode }

        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            for item in items {
                if case let .barcode(code) = item, let text = code.payloadStringValue, text != last {
                    last = text
                    onCode(text)
                }
            }
        }
    }
}

/// Typing the code shown under the QR code, for a Mac found on the Wi-Fi.
struct CodeSheet: View {
    let mac: Discovery.Found
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var code = ""
    @State private var problem: String?
    @State private var busy = false
    @FocusState private var focused: Bool

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 18) {
                Text("Type the code under the QR code on \(mac.name)'s Ferry page.")
                    .font(.system(size: 16))
                    .foregroundStyle(Theme.ink2)
                TextField("xxxx xxxx", text: $code)
                    .font(.system(size: 28, weight: .semibold, design: .monospaced))
                    .multilineTextAlignment(.center)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.asciiCapable)
                    .focused($focused)
                    .padding(.vertical, 16)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    .onSubmit { Task { await connect() } }
                if let problem {
                    Text(problem).font(.system(size: 15)).foregroundStyle(Theme.danger)
                }
                Button {
                    Task { await connect() }
                } label: {
                    if busy { ProgressView().tint(Theme.bg) } else { Text("Connect") }
                }
                .buttonStyle(InkButtonStyle())
                .disabled(Pairing.normalise(code).count < 8 || busy)
                Spacer()
            }
            .padding(20)
            .background(Theme.bg)
            .navigationTitle(mac.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .topBarLeading) { Button("Close", systemImage: "xmark") { dismiss() } } }
            .onAppear { focused = true }
        }
        .presentationDetents([.medium])
    }

    private func connect() async {
        busy = true
        problem = nil
        do {
            library.pair(try await FerryAPI.pair(host: mac.host, port: mac.port, tlsPort: mac.tlsPort, fingerprint: mac.fingerprint,
                                                 code: code, device: Library.deviceName, kind: Library.deviceKind,
                                                 previous: library.previousKey(host: mac.host, port: mac.port, id: mac.computerID)))
            dismiss()
        } catch {
            problem = (error as? FerryError)?.errorDescription ?? error.localizedDescription
        }
        busy = false
    }
}
