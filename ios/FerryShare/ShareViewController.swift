import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// "Ferry" in the share sheet: sends what's shared straight to the computer in use.
final class ShareViewController: UIViewController {
    private let model = ShareModel()

    override func viewDidLoad() {
        super.viewDidLoad()
        let host = UIHostingController(rootView: ShareCard(model: model) { [weak self] in self?.finish() })
        addChild(host)
        host.view.frame = view.bounds
        host.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        host.view.backgroundColor = .clear
        view.addSubview(host.view)
        host.didMove(toParent: self)
        let providers = (extensionContext?.inputItems as? [NSExtensionItem] ?? []).flatMap { $0.attachments ?? [] }
        Task { await model.send(providers) }
    }

    private func finish() {
        model.cancel()
        extensionContext?.completeRequest(returningItems: nil)
    }
}

@Observable
final class ShareModel {
    enum Phase: Equatable { case sending, done, failed(String) }
    var phase: Phase = .sending
    var macName = "your computer"
    var total = 0
    var sent = 0
    var progress = 0.0
    private var task: Task<Void, Never>?

    func send(_ providers: [NSItemProvider]) async {
        guard let computer = Pairing.load().active else {
            phase = .failed(Pairing.legacy() == nil ? "Open Ferry first and connect it to your computer."
                                                    : "Open Ferry once to finish connecting to your computer.")
            return
        }
        macName = computer.name
        total = providers.count
        let work = Task {
            do {
                let api = FerryAPI(computer)
                _ = try await api.info()
                for (i, provider) in providers.enumerated() {
                    try Task.checkCancellation()
                    let file = try await load(provider)
                    _ = try await api.upload(file, name: file.lastPathComponent) { p in
                        Task { @MainActor in self.progress = (Double(i) + p) / Double(max(self.total, 1)) }
                    }
                    try? FileManager.default.removeItem(at: file.deletingLastPathComponent())
                    sent = i + 1
                }
                progress = 1
                phase = .done
            } catch is CancellationError {
            } catch {
                phase = .failed((error as? FerryError)?.errorDescription ?? error.localizedDescription)
            }
        }
        task = work
        await work.value
    }

    func cancel() { task?.cancel() }

    /// The shared item as a file of its own, with a sensible name.
    private func load(_ provider: NSItemProvider) async throws -> URL {
        let type = provider.registeredContentTypes.first { $0.conforms(to: .movie) }
            ?? provider.registeredContentTypes.first { $0.conforms(to: .image) }
            ?? provider.registeredContentTypes.first { $0.conforms(to: .data) }
            ?? .data
        let name = suggestedName(provider.suggestedName, type: type)
        return try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadFileRepresentation(for: type, openInPlace: false) { url, _, error in
                guard let url else {
                    continuation.resume(throwing: error ?? FerryError.server(0, "Couldn't read what was shared"))
                    return
                }
                do {
                    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("share-\(UUID().uuidString)", isDirectory: true)
                    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                    let dest = dir.appendingPathComponent(url.pathExtension.isEmpty ? name : url.lastPathComponent)
                    try FileManager.default.copyItem(at: url, to: dest)
                    continuation.resume(returning: dest)
                } catch {
                    continuation.resume(throwing: error)
                }
            }
        }
    }
}

private struct ShareCard: View {
    let model: ShareModel
    let close: () -> Void

    var body: some View {
        VStack {
            Spacer()
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 12) {
                    Image(systemName: icon)
                        .font(.system(size: 20, weight: .semibold))
                        .foregroundStyle(tint)
                        .frame(width: 40, height: 40)
                        .background(tint.opacity(0.14), in: Circle())
                        .contentTransition(.symbolEffect(.replace))
                    VStack(alignment: .leading, spacing: 2) {
                        Text(title).font(.system(size: 17, weight: .semibold))
                        Text(subtitle).font(.system(size: 14)).foregroundStyle(.secondary).lineLimit(2)
                    }
                    Spacer()
                }
                if model.phase == .sending {
                    ProgressView(value: model.progress).tint(Color(UIColor(hex: 0xEE5229)))
                }
                Button(model.phase == .sending ? "Cancel" : "Done", action: close)
                    .font(.system(size: 16, weight: .semibold))
                    .frame(maxWidth: .infinity, minHeight: 46)
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                    .buttonStyle(.plain)
            }
            .padding(18)
            .background(Color(.systemBackground), in: RoundedRectangle(cornerRadius: 24, style: .continuous))
            .shadow(color: .black.opacity(0.2), radius: 30, y: 10)
            .padding(14)
        }
        .task(id: model.phase) {
            if model.phase == .done {
                try? await Task.sleep(for: .seconds(1.2))
                close()
            }
        }
    }

    private var title: String {
        switch model.phase {
        case .sending: model.total > 1 ? "Sending \(min(model.sent + 1, model.total)) of \(model.total)" : "Sending…"
        case .done: "Sent to \(model.macName)"
        case .failed: "Couldn't send"
        }
    }

    private var subtitle: String {
        switch model.phase {
        case .sending: "To \(model.macName), with Ferry"
        case .done: model.total == 1 ? "It's in the Ferry folder" : "They're in the Ferry folder"
        case let .failed(reason): reason
        }
    }

    private var icon: String {
        switch model.phase {
        case .sending: "arrow.up"
        case .done: "checkmark"
        case .failed: "exclamationmark"
        }
    }

    private var tint: Color {
        switch model.phase {
        case .failed: .red
        case .done: .green
        case .sending: Color(UIColor(hex: 0xEE5229))
        }
    }
}

extension UIColor {
    convenience init(hex: UInt32) {
        self.init(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
    }
}
