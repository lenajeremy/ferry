import AVKit
import QuickLook
import SwiftUI
import UniformTypeIdentifiers

/// One file at a time, full screen: swipe between them, pinch photos, play videos, preview the rest.
struct ViewerView: View {
    let start: FerryFile
    @Environment(Library.self) private var library
    @Environment(\.dismiss) private var dismiss
    @State private var current: String = ""
    @State private var chrome = true
    @State private var confirmDelete = false
    @State private var sharing: ShareItems?
    @State private var previewing: URL?
    @State private var working: String?

    private var file: FerryFile? { library.files.first { $0.name == current } }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            TabView(selection: $current) {
                ForEach(library.files) { f in
                    Page(file: f, api: library.api, preview: open)
                        .tag(f.name)
                        .ignoresSafeArea()
                }
            }
            .tabViewStyle(.page(indexDisplayMode: .never))
            .ignoresSafeArea()
            .onTapGesture { withAnimation(.easeOut(duration: 0.2)) { chrome.toggle() } }

            if chrome, let file {
                VStack(spacing: 0) {
                    top(file)
                    Spacer()
                    bottom(file)
                }
                .transition(.opacity)
            }
        }
        .statusBarHidden(!chrome)
        .preferredColorScheme(.dark)
        .onAppear { current = start.name }
        .onChange(of: library.files) { _, files in
            // Deleted here or on the Mac: move to a neighbour, or close.
            if !files.contains(where: { $0.name == current }) {
                if let next = files.first { current = next.name } else { dismiss() }
            }
        }
        .sheet(item: $sharing) { ShareSheet(items: $0.urls) }
        .quickLookPreview($previewing)
        .confirmationDialog("Delete \(file?.name ?? "this file")?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Move to the \(library.trash) on \(library.computer?.name ?? "your computer")", role: .destructive) {
                guard let file else { return }
                let i = library.files.firstIndex(of: file) ?? 0
                let neighbour = library.files.indices.contains(i + 1) ? library.files[i + 1] : (i > 0 ? library.files[i - 1] : nil)
                Task {
                    await library.delete([file])
                    if let neighbour { current = neighbour.name } else { dismiss() }
                }
            }
        }
        .overlay(alignment: .top) {
            if let toast = library.toast {
                Text(toast)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(.black)
                    .padding(.horizontal, 18).padding(.vertical, 12)
                    .background(.white, in: Capsule())
                    .padding(.top, 60)
                    .task(id: toast) {
                        try? await Task.sleep(for: .seconds(2.4))
                        library.toast = nil
                    }
            }
        }
    }

    private func top(_ f: FerryFile) -> some View {
        HStack(spacing: 12) {
            Button { dismiss() } label: {
                Image(systemName: "xmark").font(.system(size: 15, weight: .semibold)).frame(width: 40, height: 40)
            }
            .buttonStyle(.glass)
            .buttonBorderShape(.circle)
            .accessibilityLabel("Close")
            VStack(spacing: 2) {
                Text(f.name).font(.system(size: 15, weight: .semibold)).lineLimit(1).truncationMode(.middle)
                Text("From \(f.from) · \(f.sizeLabel) · \(FerryFile.dayLabel(f.addedDate)), \(f.addedDate.formatted(date: .omitted, time: .shortened))")
                    .font(.system(size: 12.5)).foregroundStyle(.white.opacity(0.6)).lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            Color.clear.frame(width: 40, height: 40)
        }
        .foregroundStyle(.white)
        .padding(.horizontal, 12)
        .padding(.top, 4)
        .background(LinearGradient(colors: [.black.opacity(0.55), .clear], startPoint: .top, endPoint: .bottom).ignoresSafeArea())
    }

    private func bottom(_ f: FerryFile) -> some View {
        HStack(spacing: 12) {
            if f.isMedia {
                Button {
                    run("Saving…") { await library.saveToPhotos([f]) }
                } label: {
                    Label("Save", systemImage: "square.and.arrow.down").font(.system(size: 16, weight: .semibold)).padding(.horizontal, 8).frame(height: 44)
                }
                .buttonStyle(.glassProminent)
                .tint(.white)
                .foregroundStyle(.black)
            } else {
                Button { open(f) } label: {
                    Label("Open", systemImage: "eye").font(.system(size: 16, weight: .semibold)).padding(.horizontal, 8).frame(height: 44)
                }
                .buttonStyle(.glassProminent)
                .tint(.white)
                .foregroundStyle(.black)
            }
            if f.kind == "image" {
                Button {
                    run("Copying…") { await library.copy(f) }
                } label: {
                    Image(systemName: "doc.on.doc").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
                }
                .buttonStyle(.glass)
                .buttonBorderShape(.circle)
                .accessibilityLabel("Copy")
            }
            Button {
                run("Getting it…") {
                    let urls = await library.downloads([f])
                    if !urls.isEmpty { sharing = ShareItems(urls: urls) }
                }
            } label: {
                Image(systemName: "square.and.arrow.up").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
            }
            .buttonStyle(.glass)
            .buttonBorderShape(.circle)
            .accessibilityLabel("Share")
            Button { confirmDelete = true } label: {
                Image(systemName: "trash").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
            }
            .buttonStyle(.glass)
            .buttonBorderShape(.circle)
            .foregroundStyle(Color(red: 1, green: 0.54, blue: 0.5))
            .accessibilityLabel("Delete")
        }
        .disabled(working != nil)
        .overlay(alignment: .top) {
            if let working { Text(working).font(.system(size: 14, weight: .medium)).foregroundStyle(.white.opacity(0.75)).offset(y: -30) }
        }
        .foregroundStyle(.white)
        .padding(.bottom, 12)
        .frame(maxWidth: .infinity)
        .background(LinearGradient(colors: [.clear, .black.opacity(0.55)], startPoint: .top, endPoint: .bottom).ignoresSafeArea())
    }

    private func open(_ f: FerryFile) {
        run("Getting it…") {
            if let url = await library.downloads([f]).first { previewing = url }
        }
    }

    private func run(_ label: String, _ work: @escaping () async -> Void) {
        working = label
        Task {
            await work()
            working = nil
        }
    }
}

/// One file in the viewer.
private struct Page: View {
    let file: FerryFile
    let api: FerryAPI?
    let preview: (FerryFile) -> Void
    @State private var image: UIImage?
    @State private var player: AVPlayer?

    var body: some View {
        Group {
            if let api {
                switch file.kind {
                case "image":
                    if let image { ZoomableImage(image: image) } else { placeholder(api) }
                case "video":
                    if let player {
                        VideoPlayer(player: player).onDisappear { player.pause() }
                    } else {
                        placeholder(api)
                    }
                default:
                    document(api)
                }
            }
        }
        .task(id: file.v) {
            guard let api else { return }
            if file.kind == "image" {
                // The original, sized for zooming; the Mac's large preview if the phone can't read it (RAW).
                image = await ImageStore.shared.image(api.fileURL(file), headers: api.authHeaders, maxPixel: 3000)
                if image == nil { image = await ImageStore.shared.image(api.thumbURL(file, large: true), headers: api.authHeaders, maxPixel: 3000) }
            } else if file.kind == "video" {
                let asset = SecureStream.asset(api.fileURL(file), headers: api.authHeaders)
                player = AVPlayer(playerItem: AVPlayerItem(asset: asset))
            }
        }
    }

    private func placeholder(_ api: FerryAPI) -> some View {
        RemoteImage(url: api.thumbURL(file), headers: api.authHeaders, fill: false) { Color.black }
            .overlay { ProgressView().tint(.white) }
    }

    private func document(_ api: FerryAPI) -> some View {
        VStack(spacing: 14) {
            if file.kind == "pdf" {
                RemoteImage(url: api.thumbURL(file, large: true), headers: api.authHeaders, maxPixel: 1600, fill: false) { EmptyView() }
                    .frame(maxHeight: 460)
                    .padding(.horizontal, 30)
            } else {
                Text(file.ext)
                    .font(.system(size: 17, weight: .bold, design: .monospaced))
                    .foregroundStyle(Theme.accent)
                    .padding(.horizontal, 14).padding(.vertical, 10)
                    .background(Theme.accent.opacity(0.16), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
            Text(file.name).font(.system(size: 18, weight: .semibold)).foregroundStyle(.white).multilineTextAlignment(.center)
            Text(file.sizeLabel).font(.system(size: 15)).foregroundStyle(.white.opacity(0.6))
            Button("Open") { preview(file) }
                .font(.system(size: 16, weight: .semibold))
                .buttonStyle(.glass)
                .padding(.top, 6)
        }
        .padding(30)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// Plays a video from a computer's secure address. The player makes its own connections and wouldn't
/// trust Ferry's certificate, so it asks for "ferry-https://…" and this hands it the bytes, fetched
/// through the app's own connection (which trusts only that computer), a range at a time.
nonisolated final class SecureStream: NSObject, AVAssetResourceLoaderDelegate, @unchecked Sendable {
    static let shared = SecureStream()
    private static let scheme = "ferry-https"
    private let queue = DispatchQueue(label: "ferry.stream")
    private let lock = NSLock()
    private var headers: [String: [String: String]] = [:]
    private var running: [ObjectIdentifier: Task<Void, Never>] = [:]

    static func asset(_ url: URL, headers: [String: String]) -> AVURLAsset {
        guard url.scheme == "https", var c = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            return AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": headers])
        }
        c.scheme = scheme
        let custom = c.url!
        shared.lock.withLock { shared.headers[custom.absoluteString] = headers }
        let asset = AVURLAsset(url: custom)
        asset.resourceLoader.setDelegate(shared, queue: shared.queue)
        return asset
    }

    func resourceLoader(_ resourceLoader: AVAssetResourceLoader,
                        shouldWaitForLoadingOfRequestedResource loading: AVAssetResourceLoadingRequest) -> Bool {
        guard let url = loading.request.url, var c = URLComponents(url: url, resolvingAgainstBaseURL: false),
              c.scheme == Self.scheme else { return false }
        c.scheme = "https"
        var r = URLRequest(url: c.url!)
        lock.withLock { headers[url.absoluteString] }?.forEach { r.setValue($1, forHTTPHeaderField: $0) }
        if let d = loading.dataRequest {
            let start = d.currentOffset != 0 ? d.currentOffset : d.requestedOffset
            let range = d.requestsAllDataToEndOfResource ? "bytes=\(start)-" : "bytes=\(start)-\(d.requestedOffset + Int64(d.requestedLength) - 1)"
            r.setValue(range, forHTTPHeaderField: "Range")
        }
        let task = Task {
            do {
                let (bytes, response) = try await FerryAPI.session.bytes(for: r)
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                    throw URLError(.badServerResponse)
                }
                if let info = loading.contentInformationRequest {
                    info.contentType = UTType(mimeType: http.mimeType ?? "")?.identifier ?? UTType.mpeg4Movie.identifier
                    let total = (http.value(forHTTPHeaderField: "Content-Range")?.split(separator: "/").last).flatMap { Int64($0) }
                    info.contentLength = total ?? http.expectedContentLength
                    info.isByteRangeAccessSupported = true
                }
                var chunk = Data()
                chunk.reserveCapacity(1 << 18)
                for try await byte in bytes {
                    chunk.append(byte)
                    if chunk.count == 1 << 18 {
                        loading.dataRequest?.respond(with: chunk)
                        chunk.removeAll(keepingCapacity: true)
                    }
                }
                if !chunk.isEmpty { loading.dataRequest?.respond(with: chunk) }
                loading.finishLoading()
            } catch {
                if !Task.isCancelled { loading.finishLoading(with: error) }
            }
            _ = lock.withLock { running.removeValue(forKey: ObjectIdentifier(loading)) }
        }
        lock.withLock { running[ObjectIdentifier(loading)] = task }
        return true
    }

    func resourceLoader(_ resourceLoader: AVAssetResourceLoader, didCancel loading: AVAssetResourceLoadingRequest) {
        lock.withLock { running.removeValue(forKey: ObjectIdentifier(loading)) }?.cancel()
    }
}
