import Foundation
import Observation
import UIKit
import Photos
import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// Everything the app shows: the computers this phone is paired with, the current one's files (kept
/// live), what's being sent, and messages.
@Observable
final class Library {
    enum Status: Equatable { case connecting, connected, offline(String) }

    private(set) var saved = Pairing.load()
    /// The computer in use.
    var computer: FerryComputer? { saved.active }
    var computers: [FerryComputer] { saved.computers }
    private(set) var files: [FerryFile] = []
    private(set) var loaded = false
    private(set) var status: Status = .connecting
    private(set) var uploads: [Upload] = []
    /// Moving the pairing an older version kept to a key of this phone's own.
    private(set) var migrating = false
    /// Shown on the pairing screen: why this phone isn't connected any more.
    var notice: String?
    var toast: String?

    private(set) var api: FerryAPI?
    /// What the paired computer calls its bin.
    private(set) var trash = "Trash"
    private var live: Task<Void, Never>?
    private var sentHere: Set<String> = []

    static var deviceName: String { UIDevice.current.name }
    static var deviceKind: String { UIDevice.current.userInterfaceIdiom == .pad ? "iPad" : "iPhone" }

    init() {
        if saved.computers.isEmpty, Pairing.legacy() != nil { migrate() }
    }

    // MARK: Computers

    /// Adds (or refreshes) a paired computer and switches to it.
    func pair(_ computer: FerryComputer) {
        saved.computers.removeAll { $0.id == computer.id }
        saved.computers.append(computer)
        saved.current = computer.id
        Pairing.save(saved)
        notice = nil
        switched()
    }

    func use(_ id: String) {
        guard id != computer?.id, saved.computers.contains(where: { $0.id == id }) else { return }
        saved.current = id
        Pairing.save(saved)
        switched()
    }

    /// This phone's key for a computer it may be pairing with again (found by address or id).
    func previousKey(host: String, port: Int, id: String? = nil) -> String? {
        saved.computers.first { ($0.host == host && $0.port == port) || $0.id == id }?.token
    }

    /// Forgets a computer on this phone (and tells it, so it drops this phone from its list too).
    /// Forgetting the current one moves to the next, if any.
    func forget(_ id: String, tell: Bool = true) {
        if tell, let c = saved.computers.first(where: { $0.id == id }) {
            Task.detached { await FerryAPI(c).unpairSelf() }
        }
        saved.computers.removeAll { $0.id == id }
        if saved.current == id { saved.current = saved.computers.first?.id }
        Pairing.save(saved)
        switched()
    }

    private func switched() {
        live?.cancel()
        api = nil
        files = []
        loaded = false
        status = .connecting
        if computer != nil { start() }
    }

    /// Older versions kept the computer's code itself: trade it for a key of this phone's own.
    func migrate() {
        guard let old = Pairing.legacy(), !migrating else { return }
        migrating = true
        Task {
            do {
                let c = try await FerryAPI.migrate(old, device: Self.deviceName, kind: Self.deviceKind)
                Pairing.clearLegacy()
                pair(c)
            } catch FerryError.wrongCode {
                Pairing.clearLegacy()
                notice = "\(old.name) has a new code. Scan it to connect again."
            } catch {
                notice = "Couldn't reach \(old.name) to finish connecting. Open Ferry there, then come back to this app."
            }
            migrating = false
        }
    }

    // MARK: Connecting

    /// Keeps the list live: connects, follows the computer's updates, and tries again a moment after
    /// losing it. A computer that has removed this phone is forgotten.
    func start() {
        live?.cancel()
        guard let computer else { return }
        live = Task {
            while !Task.isCancelled {
                if status != .connected { status = .connecting }
                do {
                    let api = FerryAPI(computer)
                    trash = try await api.info().trash ?? "Trash"
                    self.api = api
                    for try await list in api.events() {
                        status = .connected
                        apply(list)
                    }
                    status = .offline("Lost the connection to \(computer.name)")
                } catch FerryError.removed {
                    removed(computer)
                    return
                } catch {
                    if Task.isCancelled { return }
                    status = .offline((error as? FerryError)?.errorDescription ?? error.localizedDescription)
                }
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    private func removed(_ c: FerryComputer) {
        let message = "This iPhone was removed from \(c.name). Scan its code to connect again."
        forget(c.id, tell: false)
        if computer == nil { notice = message } else { toast = message }
    }

    func stop() { live?.cancel() }

    func refresh() async {
        guard let api, let list = try? await api.files() else { return }
        apply(list)
    }

    private func apply(_ list: [FerryFile]) {
        if loaded {
            let known = Set(files.map(\.name))
            let arrived = list.filter { !known.contains($0.name) && !sentHere.contains($0.name) && $0.from != "iPhone" }
            if !arrived.isEmpty { toast = "\(describe(arrived.map(\.kind))) from \(arrived[0].from)" }
        }
        files = list
        loaded = true
    }

    // MARK: Sending

    @Observable
    final class Upload: Identifiable {
        enum State: Equatable { case preparing, waiting, sending, done, failed(String) }
        let id = UUID()
        var name: String
        var size: Int64 = 0
        var progress = 0.0
        var state: State = .preparing
        var source: URL?
        var task: Task<Void, Never>?
        var kind: String { FerryFile.kindOf(name) }
        init(name: String) { self.name = name }
    }

    var sending: Bool { uploads.contains { [.preparing, .waiting, .sending].contains($0.state) } }

    /// Photos and videos from the picker, as their originals (HEIC, HEVC, Live Photo stills).
    func send(_ items: [PhotosPickerItem]) {
        for item in items {
            let isVideo = item.supportedContentTypes.contains { $0.conforms(to: .movie) }
            let upload = Upload(name: isVideo ? "video" : "photo")
            uploads.append(upload)
            Task {
                do {
                    let url: URL? = isVideo ? try await item.loadTransferable(type: PickedMovie.self)?.url
                                            : try await item.loadTransferable(type: PickedImage.self)?.url
                    guard let url else { throw FerryError.server(0, "Couldn't read that from Photos") }
                    ready(upload, url)
                } catch {
                    upload.state = .failed(error.localizedDescription)
                }
            }
        }
    }

    /// Files from the Files app (security-scoped: copied first).
    func send(files urls: [URL]) {
        for url in urls {
            let upload = Upload(name: url.lastPathComponent)
            uploads.append(upload)
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            do {
                ready(upload, try copyToTemp(url))
            } catch {
                upload.state = .failed("Couldn't read \(url.lastPathComponent)")
            }
        }
    }

    private func ready(_ upload: Upload, _ url: URL) {
        upload.name = url.lastPathComponent
        upload.source = url
        upload.size = (try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize).map(Int64.init) ?? 0
        upload.state = .waiting
        pump()
    }

    private func pump() {
        while uploads.filter({ $0.state == .sending }).count < 2, let next = uploads.first(where: { $0.state == .waiting }) {
            transfer(next)
        }
    }

    private func transfer(_ upload: Upload) {
        guard let api, let source = upload.source else {
            upload.state = .failed("Not connected to your computer")
            return
        }
        upload.state = .sending
        upload.progress = 0
        // Keeps going for a while if the app is put away mid-send.
        let background = UIApplication.shared.beginBackgroundTask(withName: "Sending to Ferry")
        upload.task = Task {
            defer { UIApplication.shared.endBackgroundTask(background) }
            do {
                let saved = try await api.upload(source, name: upload.name) { p in
                    Task { @MainActor in if upload.state == .sending { upload.progress = p } }
                }
                sentHere.insert(saved.name)
                upload.progress = 1
                upload.state = .done
                try? FileManager.default.removeItem(at: source.deletingLastPathComponent())
            } catch is CancellationError {
                uploads.removeAll { $0.id == upload.id }
            } catch {
                upload.state = .failed((error as? FerryError)?.errorDescription ?? error.localizedDescription)
            }
            pump()
            tidyUploads()
        }
    }

    func retry(_ upload: Upload) {
        guard upload.source != nil else { return }
        upload.state = .waiting
        pump()
    }

    func cancel(_ upload: Upload) {
        if let task = upload.task, upload.state == .sending { task.cancel() } else { uploads.removeAll { $0.id == upload.id } }
    }

    func clearUploads() { uploads.removeAll { !$0.isBusy } }

    private func tidyUploads() {
        guard !sending, !uploads.contains(where: { if case .failed = $0.state { true } else { false } }) else { return }
        Task {
            try? await Task.sleep(for: .seconds(2.2))
            if !sending { withAnimation(.smooth) { uploads.removeAll { $0.state == .done } } }
        }
    }

    // MARK: Saving, sharing, deleting

    /// Saves photos and videos to the library, as they are (originals, with their dates and places).
    func saveToPhotos(_ chosen: [FerryFile]) async {
        let media = chosen.filter(\.isMedia)
        guard let api, !media.isEmpty else { return }
        guard await PHPhotoLibrary.requestAuthorization(for: .addOnly) == .authorized else {
            toast = "Ferry needs permission to add to Photos (Settings › Ferry)"
            return
        }
        var saved = 0
        for f in media {
            do {
                let url = try await api.download(f)
                try await Self.addToPhotos(url, video: f.kind == "video")
                saved += 1
            } catch {
                toast = "Couldn't save \(f.name)"
            }
        }
        if saved > 0 { toast = "Saved \(describe(media.prefix(saved).map(\.kind))) to Photos" }
    }

    nonisolated static func addToPhotos(_ url: URL, video: Bool) async throws {
        try await PHPhotoLibrary.shared().performChanges {
            let options = PHAssetResourceCreationOptions()
            options.originalFilename = url.lastPathComponent
            PHAssetCreationRequest.forAsset().addResource(with: video ? .video : .photo, fileURL: url, options: options)
        }
    }

    /// Puts a photo on the clipboard, to paste into Messages, Notes, anywhere.
    func copy(_ f: FerryFile) async {
        guard let api, f.kind == "image" else { return }
        do {
            let url = try await api.download(f)
            guard let image = UIImage(contentsOfFile: url.path) else { throw FerryError.server(0, "") }
            UIPasteboard.general.image = image
            toast = "Copied. Paste it anywhere"
        } catch {
            toast = "Couldn't copy \(f.name)"
        }
    }

    func downloads(_ chosen: [FerryFile]) async -> [URL] {
        guard let api else { return [] }
        var urls: [URL] = []
        for f in chosen {
            if let url = try? await api.download(f) { urls.append(url) }
        }
        if urls.count < chosen.count { toast = "Couldn't get \(chosen.count - urls.count) from your computer" }
        return urls
    }

    func delete(_ chosen: [FerryFile]) async {
        guard let api else { return }
        var failed = 0
        for f in chosen {
            do { try await api.delete(f) } catch { failed += 1 }
        }
        toast = failed > 0 ? "Couldn't delete \(failed)" : "Moved \(describe(chosen.map(\.kind))) to the \(trash) on \(computer?.name ?? "your computer")"
        await refresh()
    }
}

extension Library.Upload {
    var isBusy: Bool { [.preparing, .waiting, .sending].contains(state) }
}

extension FerryFile {
    static func kindOf(_ name: String) -> String {
        guard let type = UTType(filenameExtension: (name as NSString).pathExtension) else { return "file" }
        if type.conforms(to: .image) { return "image" }
        if type.conforms(to: .movie) || type.conforms(to: .video) { return "video" }
        return "file"
    }
}

/// Copies a file somewhere the app owns, in a folder of its own so its name is kept.
nonisolated func copyToTemp(_ url: URL) throws -> URL {
    let dir = FileManager.default.temporaryDirectory.appendingPathComponent("send-\(UUID().uuidString)", isDirectory: true)
    try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    let dest = dir.appendingPathComponent(url.lastPathComponent)
    try FileManager.default.copyItem(at: url, to: dest)
    return dest
}

/// A photo from the picker, as its original file.
nonisolated struct PickedImage: Transferable {
    let url: URL
    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .image) { PickedImage(url: try copyToTemp($0.file)) }
    }
}

/// A video from the picker, as its original file.
nonisolated struct PickedMovie: Transferable {
    let url: URL
    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .movie) { PickedMovie(url: try copyToTemp($0.file)) }
    }
}
