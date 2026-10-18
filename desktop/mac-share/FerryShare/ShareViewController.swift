import Cocoa
import SwiftUI
import UniformTypeIdentifiers

// Share › Ferry on the Mac (Jeremiah, 2026-10-18: "pick share, I want Ferry to show up there so it
// shares automatically to the phone"). It starts sending as soon as it opens: each file goes to
// the Ferry running on this Mac, the same as dropping it on Ferry's page, so it's on your phone.
// If Ferry isn't running, it opens it first. It closes itself once everything's sent.

final class ShareViewController: NSViewController {
    private let model = ShareModel()
    private var started = false

    override var nibName: NSNib.Name? { nil }

    override func loadView() {
        let host = NSHostingView(rootView: ShareView(model: model, close: { [weak self] in self?.close() }))
        host.frame = NSRect(x: 0, y: 0, width: 360, height: 104)
        view = host
        preferredContentSize = host.fittingSize
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        guard !started else { return }
        started = true
        let items = extensionContext?.inputItems as? [NSExtensionItem] ?? []
        Task { @MainActor in
            await model.send(items)
            if model.state == .sent {
                try? await Task.sleep(nanoseconds: 1_100_000_000)
                extensionContext?.completeRequest(returningItems: nil)
            }
        }
    }

    private func close() {
        model.cancel()
        if model.state == .sent {
            extensionContext?.completeRequest(returningItems: nil)
        } else {
            extensionContext?.cancelRequest(withError: NSError(domain: NSCocoaErrorDomain, code: NSUserCancelledError))
        }
    }
}

// MARK: - Sending

@MainActor
final class ShareModel: ObservableObject {
    enum State: Equatable { case preparing, opening, sending, sent, failed(String) }

    @Published var state: State = .preparing
    @Published var total = 0
    @Published var done = 0
    @Published var current = ""
    /// The file being sent, 0…1.
    @Published var fraction = 0.0

    /// Ferry on this Mac (the port it always uses).
    static let ferry = URL(string: "http://127.0.0.1:7070")!

    private var task: URLSessionUploadTask?
    private var watching: NSKeyValueObservation?

    var progress: Double { total == 0 ? 0 : min((Double(done) + fraction) / Double(total), 1) }

    func send(_ items: [NSExtensionItem]) async {
        let providers = items.flatMap { $0.attachments ?? [] }
        total = providers.count
        guard total > 0 else { return fail("There's nothing here Ferry can send.") }
        guard await ferryIsRunning() else {
            return fail("Ferry isn't running on this Mac. Open Ferry, then share again.")
        }
        state = .sending
        var folders = 0
        for provider in providers {
            guard state == .sending else { return }
            do {
                guard let file = try await outgoing(provider) else {
                    folders += 1
                    continue
                }
                current = file.name
                fraction = 0
                try await upload(file)
                done += 1
            } catch {
                if state == .sending { fail("Couldn't send \(current.isEmpty ? "that" : current). \(error.localizedDescription)") }
                return
            }
        }
        if done == 0 {
            fail(folders > 0 ? "Ferry sends files, photos and videos, not folders." : "There's nothing here Ferry can send.")
        } else {
            fraction = 0
            state = .sent
        }
    }

    func cancel() {
        task?.cancel()
        if state != .sent { state = .failed("") }
    }

    private func fail(_ message: String) { state = .failed(message) }

    /// Ferry answers; if it doesn't, open the Ferry app this came with and wait for it to start.
    private func ferryIsRunning() async -> Bool {
        if await ping() { return true }
        state = .opening
        let app = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let config = NSWorkspace.OpenConfiguration()
        config.activates = false
        NSWorkspace.shared.openApplication(at: app, configuration: config) { _, _ in }
        for _ in 0..<40 {
            try? await Task.sleep(nanoseconds: 500_000_000)
            if await ping() { return true }
        }
        return false
    }

    private func ping() async -> Bool {
        var request = URLRequest(url: Self.ferry.appendingPathComponent("api/info"))
        request.timeoutInterval = 1.5
        return await withCheckedContinuation { done in
            URLSession.shared.dataTask(with: request) { _, response, _ in
                done.resume(returning: (response as? HTTPURLResponse)?.statusCode == 200)
            }.resume()
        }
    }

    // MARK: Files

    private struct Outgoing {
        let url: URL
        let name: String
        /// A file the share handed over (read with its permission), or our own copy to delete after.
        let shared: Bool
    }

    /// A file shared from Finder, or a copy of a photo or video shared as data (Photos does this).
    /// Nil for a folder.
    private func outgoing(_ provider: NSItemProvider) async throws -> Outgoing? {
        if provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
            let url: URL? = try await withCheckedThrowingContinuation { done in
                provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, error in
                    if let error { return done.resume(throwing: error) }
                    if let url = item as? URL { return done.resume(returning: url) }
                    if let data = item as? Data { return done.resume(returning: URL(dataRepresentation: data, relativeTo: nil)) }
                    done.resume(returning: nil)
                }
            }
            guard let url else { return nil }
            if (try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true { return nil }
            return Outgoing(url: url, name: url.lastPathComponent, shared: true)
        }
        guard let type = [UTType.movie, .image, .data].first(where: { provider.hasItemConformingToTypeIdentifier($0.identifier) }) else {
            return nil
        }
        return try await withCheckedThrowingContinuation { done in
            // The file only lasts until this returns, so keep a copy while it sends.
            _ = provider.loadFileRepresentation(forTypeIdentifier: type.identifier) { url, error in
                guard let url else { return done.resume(throwing: error ?? CocoaError(.fileReadUnknown)) }
                let ext = url.pathExtension
                var name = provider.suggestedName ?? url.deletingPathExtension().lastPathComponent
                if !ext.isEmpty, !name.lowercased().hasSuffix("." + ext.lowercased()) { name += "." + ext }
                let copy = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathExtension(ext)
                do {
                    try FileManager.default.copyItem(at: url, to: copy)
                    done.resume(returning: Outgoing(url: copy, name: name, shared: false))
                } catch {
                    done.resume(throwing: error)
                }
            }
        }
    }

    /// The same as dropping it on Ferry's page: PUT /api/upload, with Ferry's own header.
    private func upload(_ file: Outgoing) async throws {
        var parts = URLComponents(url: Self.ferry.appendingPathComponent("api/upload"), resolvingAgainstBaseURL: false)!
        let safe = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~"))
        parts.percentEncodedQuery = "name=" + (file.name.addingPercentEncoding(withAllowedCharacters: safe) ?? "file")
        var request = URLRequest(url: parts.url!)
        request.httpMethod = "PUT"
        request.setValue("1", forHTTPHeaderField: "X-Ferry")
        request.timeoutInterval = 600

        let reading = file.shared && file.url.startAccessingSecurityScopedResource()
        defer {
            if reading { file.url.stopAccessingSecurityScopedResource() }
            if !file.shared { try? FileManager.default.removeItem(at: file.url) }
        }
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Void, Error>) in
            let task = URLSession.shared.uploadTask(with: request, fromFile: file.url) { data, response, error in
                if let error { return done.resume(throwing: error) }
                let status = (response as? HTTPURLResponse)?.statusCode ?? 0
                guard status == 200 else {
                    let reason = data.flatMap { String(data: $0, encoding: .utf8) }?.trimmingCharacters(in: .whitespacesAndNewlines)
                    return done.resume(throwing: NSError(domain: "Ferry", code: status,
                                                         userInfo: [NSLocalizedDescriptionKey: reason?.isEmpty == false ? reason! : "Ferry said no (\(status))."]))
                }
                done.resume()
            }
            watching = task.progress.observe(\.fractionCompleted) { [weak self] p, _ in
                let f = p.fractionCompleted
                Task { @MainActor in self?.fraction = f }
            }
            self.task = task
            task.resume()
        }
        watching = nil
        task = nil
    }
}

// MARK: - The sheet

struct ShareView: View {
    @ObservedObject var model: ShareModel
    let close: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            Image(nsImage: Self.icon)
                .resizable()
                .frame(width: 44, height: 44)
            VStack(alignment: .leading, spacing: 6) {
                Text(title).font(.headline)
                if !detail.isEmpty {
                    Text(detail)
                        .font(.subheadline)
                        .foregroundColor(.secondary)
                        .lineLimit(2)
                        .truncationMode(.middle)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if model.state == .sending || model.state == .opening || model.state == .preparing {
                    ProgressView(value: model.state == .sending ? model.progress : nil)
                        .progressViewStyle(LinearProgressViewStyle())
                        .padding(.top, 2)
                }
                HStack {
                    Spacer()
                    Button(model.state == .sent ? "Done" : (isFailed ? "Close" : "Cancel"), action: close)
                        .keyboardShortcut(model.state == .sent ? .defaultAction : .cancelAction)
                }
                .padding(.top, 4)
            }
        }
        .padding(18)
        .frame(width: 360)
    }

    private var isFailed: Bool { if case .failed = model.state { return true } else { return false } }

    private var title: String {
        switch model.state {
        case .preparing: "Getting ready…"
        case .opening: "Opening Ferry…"
        case .sending: "Sending to your phone"
        case .sent: "Sent to your phone"
        case .failed: "Couldn't send"
        }
    }

    private var detail: String {
        switch model.state {
        case .preparing, .opening: model.total == 1 ? "1 item" : "\(model.total) items"
        case .sending: model.total > 1 ? "\(model.current) · \(model.done + 1) of \(model.total)" : model.current
        case .sent: (model.done == 1 ? model.current : "\(model.done) files") + " · open Ferry on your phone to see it"
        case .failed(let message): message
        }
    }

    /// Ferry's own icon (the app this extension lives in).
    private static let icon: NSImage = {
        let app = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return NSWorkspace.shared.icon(forFile: app.path)
    }()
}
