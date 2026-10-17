import PhotosUI
import SwiftUI

/// The Mac's Ferry folder: send to it, see what's there, save or share what you want.
struct LibraryView: View {
    @Environment(Library.self) private var library
    @Environment(\.scenePhase) private var scenePhase
    @State private var picked: [PhotosPickerItem] = []
    @State private var pickingFiles = false
    @State private var viewing: FerryFile?
    @State private var selecting = false
    @State private var selected: Set<String> = []
    @State private var confirmDelete = false
    @State private var confirmDisconnect = false
    @State private var adding = false
    @State private var sharing: ShareItems?
    @State private var working: String?

    private let columns = [GridItem(.flexible(), spacing: 3), GridItem(.flexible(), spacing: 3), GridItem(.flexible(), spacing: 3)]

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if !selecting { sendButtons }
                    if library.loaded && library.files.isEmpty { empty }
                    ForEach(days, id: \.label) { day in
                        VStack(alignment: .leading, spacing: 8) {
                            Text(day.label)
                                .font(.system(size: 15, weight: .semibold))
                                .foregroundStyle(Theme.ink2)
                                .padding(.leading, 2)
                            LazyVGrid(columns: columns, spacing: 3) {
                                ForEach(day.files) { tile($0) }
                            }
                        }
                    }
                    if !library.loaded, library.status != .connected { loading }
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 120)
            }
            .background(Theme.bg)
            .refreshable { await library.refresh() }
            .navigationTitle(selecting ? (selected.isEmpty ? "Select files" : "\(selected.count) selected") : "Ferry")
            .navigationSubtitle(selecting ? "" : statusLine)
            .toolbar { toolbar }
            .safeAreaInset(edge: .bottom) { bottomBar }
            .overlay(alignment: .top) { toastView }
            .photosPicker(isPresented: $pickingPhotos, selection: $picked, maxSelectionCount: 100, selectionBehavior: .ordered,
                          matching: .any(of: [.images, .videos]), preferredItemEncoding: .current)
            .onChange(of: picked) { _, items in
                guard !items.isEmpty else { return }
                library.send(items)
                picked = []
            }
            .fileImporter(isPresented: $pickingFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
                if case let .success(urls) = result { library.send(files: urls) }
            }
            .fullScreenCover(item: $viewing) { ViewerView(start: $0) }
            .sheet(item: $sharing) { ShareSheet(items: $0.urls) }
            .confirmationDialog("Delete \(selected.count == 1 ? "this file" : "\(selected.count) files")?", isPresented: $confirmDelete,
                                titleVisibility: .visible) {
                Button("Move to the \(library.trash) on \(library.computer?.name ?? "your computer")", role: .destructive) {
                    let chosen = chosenFiles
                    Task { await library.delete(chosen); endSelecting() }
                }
            }
            .confirmationDialog("Forget \(library.computer?.name ?? "this computer")?", isPresented: $confirmDisconnect, titleVisibility: .visible) {
                Button("Forget", role: .destructive) { if let id = library.computer?.id { library.forget(id) } }
            } message: {
                Text("You'll need to scan its code again to reconnect.")
            }
            .sheet(isPresented: $adding) { PairView(adding: true).environment(library) }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active, library.status != .connected { library.start() }
            }
            #if DEBUG
            // `-ferryOpen <name>` opens the viewer, `-ferrySelect 3` selects that many (Simulator screenshots and tests).
            .onChange(of: library.loaded) { _, loaded in
                guard loaded else { return }
                let d = UserDefaults.standard
                if let name = d.string(forKey: "ferryOpen") { viewing = library.files.first { $0.name == name } }
                // `-ferryAdd YES` opens "Add a computer".
                if d.bool(forKey: "ferryAdd") { adding = true }
                // `-ferrySend <path>` sends that file, as if picked in Files.
                if let path = d.string(forKey: "ferrySend") { library.send(files: [URL(fileURLWithPath: path)]) }
                if d.integer(forKey: "ferrySelect") > 0 {
                    selecting = true
                    selected = Set(library.files.filter(\.isMedia).prefix(d.integer(forKey: "ferrySelect")).map(\.name))
                }
            }
            #endif
        }
    }

    @State private var pickingPhotos = false

    // MARK: Pieces

    private var statusLine: String {
        switch library.status {
        case .connecting: "Connecting to \(library.computer?.name ?? "your computer")…"
        case .connected: "Connected to \(library.computer?.name ?? "your computer")"
        case let .offline(reason): reason
        }
    }

    private var sendButtons: some View {
        HStack(spacing: 10) {
            Button { pickingPhotos = true } label: {
                Label("Photos", systemImage: "photo.on.rectangle.angled")
            }
            .buttonStyle(InkButtonStyle())
            Button { pickingFiles = true } label: {
                Label("Files", systemImage: "folder")
            }
            .buttonStyle(SoftButtonStyle())
            .frame(maxWidth: 140)
        }
        .padding(.top, 6)
    }

    private var empty: some View {
        VStack(spacing: 8) {
            Image(systemName: "arrow.up.arrow.down")
                .font(.system(size: 24, weight: .medium))
                .foregroundStyle(Theme.ink3)
                .frame(width: 58, height: 58)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                .padding(.bottom, 6)
            Text("Nothing here yet").font(.system(size: 19, weight: .semibold)).foregroundStyle(Theme.ink)
            Text("Send something from this iPhone, or drop files on Ferry's page on your computer.")
                .font(.system(size: 15))
                .foregroundStyle(Theme.ink2)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 60)
        .padding(.horizontal, 30)
    }

    private var loading: some View {
        VStack(spacing: 12) {
            if case let .offline(reason) = library.status {
                Image(systemName: "wifi.exclamationmark").font(.system(size: 26)).foregroundStyle(Theme.ink3)
                Text(reason).font(.system(size: 15)).foregroundStyle(Theme.ink2).multilineTextAlignment(.center)
                Button("Try again") { library.start() }.font(.system(size: 16, weight: .semibold)).tint(Theme.accent)
            } else {
                ProgressView()
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 70)
        .padding(.horizontal, 30)
    }

    private struct Day { let label: String; let files: [FerryFile] }
    private var days: [Day] {
        var out: [Day] = []
        for f in library.files {
            let label = FerryFile.dayLabel(f.addedDate)
            if out.last?.label == label { out[out.count - 1] = Day(label: label, files: out[out.count - 1].files + [f]) }
            else { out.append(Day(label: label, files: [f])) }
        }
        return out
    }

    private func tile(_ f: FerryFile) -> some View {
        let isSelected = selected.contains(f.name)
        return Button {
            if selecting {
                if isSelected { selected.remove(f.name) } else { selected.insert(f.name) }
            } else {
                viewing = f
            }
        } label: {
            Tile(file: f, api: library.api)
                .overlay {
                    if isSelected {
                        RoundedRectangle(cornerRadius: 10, style: .continuous)
                            .fill(.white.opacity(0.18))
                            .strokeBorder(Theme.accent, lineWidth: 3)
                    }
                }
                .overlay(alignment: .topTrailing) {
                    if selecting {
                        Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                            .font(.system(size: 22, weight: .semibold))
                            .symbolRenderingMode(.palette)
                            .foregroundStyle(.white, isSelected ? Theme.accent : .black.opacity(0.25))
                            .shadow(color: .black.opacity(0.3), radius: 2)
                            .padding(6)
                    }
                }
        }
        .buttonStyle(TileStyle())
        .contextMenu {
            if f.isMedia {
                Button("Save to Photos", systemImage: "square.and.arrow.down") { Task { await library.saveToPhotos([f]) } }
            }
            if f.kind == "image" {
                Button("Copy", systemImage: "doc.on.doc") { Task { await library.copy(f) } }
            }
            Button("Share…", systemImage: "square.and.arrow.up") { share([f]) }
            Button("Delete", systemImage: "trash", role: .destructive) {
                selected = [f.name]
                confirmDelete = true
            }
        }
        .accessibilityLabel("\(f.name), from \(f.from)")
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            if !library.files.isEmpty || selecting {
                Button(selecting ? "Done" : "Select") {
                    if selecting { endSelecting() } else { selecting = true }
                }
            }
        }
        if !selecting {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Section {
                        Picker("Computer", selection: Binding(get: { library.computer?.id ?? "" }, set: { library.use($0) })) {
                            ForEach(library.computers) { c in
                                Label(c.name, systemImage: "laptopcomputer").tag(c.id)
                            }
                        }
                        .pickerStyle(.inline)
                    } header: {
                        if library.status == .connected, library.computer?.secure == true {
                            Label("Connected · encrypted", systemImage: "lock.fill")
                        }
                    }
                    Button("Add a Computer", systemImage: "plus") { adding = true }
                    Button("Forget \(library.computer?.name ?? "This Computer")", systemImage: "laptopcomputer.slash", role: .destructive) {
                        confirmDisconnect = true
                    }
                } label: {
                    Image(systemName: library.status == .connected ? "laptopcomputer" : "laptopcomputer.trianglebadge.exclamationmark")
                        .foregroundStyle(Theme.ink)
                }
                .accessibilityLabel("Your computer")
            }
        }
    }

    @ViewBuilder
    private var bottomBar: some View {
        if selecting {
            selectBar
        } else if !library.uploads.isEmpty {
            UploadTray()
        }
    }

    private var chosenFiles: [FerryFile] { library.files.filter { selected.contains($0.name) } }

    private var selectBar: some View {
        let chosen = chosenFiles
        return HStack(spacing: 10) {
            if chosen.isEmpty {
                Button("Select all") { selected = Set(library.files.map(\.name)) }
                    .buttonStyle(SoftButtonStyle())
            } else {
                if chosen.contains(where: \.isMedia) {
                    Button {
                        run("Saving…") { await library.saveToPhotos(chosen); endSelecting() }
                    } label: { Label("Save", systemImage: "square.and.arrow.down") }
                        .buttonStyle(InkButtonStyle())
                }
                Button { share(chosen) } label: { Label("Share", systemImage: "square.and.arrow.up") }
                    .buttonStyle(SoftButtonStyle())
                Button(role: .destructive) { confirmDelete = true } label: {
                    Image(systemName: "trash").foregroundStyle(Theme.danger)
                }
                .buttonStyle(SoftButtonStyle())
                .frame(width: 64)
                .accessibilityLabel("Delete")
            }
        }
        .disabled(working != nil)
        .overlay { if let working { Text(working).font(.system(size: 15, weight: .semibold)).foregroundStyle(Theme.ink2) } }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(.bar)
    }

    @ViewBuilder
    private var toastView: some View {
        if let toast = library.toast {
            Text(toast)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(Theme.bg)
                .padding(.horizontal, 18)
                .padding(.vertical, 12)
                .background(Theme.ink, in: Capsule())
                .shadow(color: .black.opacity(0.18), radius: 16, y: 6)
                .padding(.top, 8)
                .transition(.move(edge: .top).combined(with: .opacity))
                .task(id: toast) {
                    try? await Task.sleep(for: .seconds(2.6))
                    withAnimation(.smooth) { library.toast = nil }
                }
                .onTapGesture { withAnimation(.smooth) { library.toast = nil } }
        }
    }

    private func endSelecting() {
        selecting = false
        selected = []
    }

    private func share(_ files: [FerryFile]) {
        run("Getting \(describe(files.map(\.kind)))…") {
            let urls = await library.downloads(files)
            if !urls.isEmpty { sharing = ShareItems(urls: urls) }
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

struct ShareItems: Identifiable {
    let id = UUID()
    let urls: [URL]
}

private struct TileStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.96 : 1)
            .animation(.easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

/// A square in the grid: the Mac's thumbnail, or a card naming the file.
struct Tile: View {
    let file: FerryFile
    let api: FerryAPI?

    var body: some View {
        Color.clear
            .aspectRatio(1, contentMode: .fit)
            .overlay {
                if file.kind == "image" || file.kind == "video" || file.kind == "pdf", let api {
                    RemoteImage(url: api.thumbURL(file), headers: api.authHeaders) { doc }
                } else {
                    doc
                }
            }
            .overlay(alignment: .bottomLeading) {
                if file.kind == "video" {
                    Image(systemName: "play.fill")
                        .font(.system(size: 9))
                        .foregroundStyle(.white)
                        .frame(width: 22, height: 22)
                        .background(.black.opacity(0.5), in: Circle())
                        .padding(6)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }

    private var doc: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(file.ext)
                .font(.system(size: 11, weight: .bold, design: .monospaced))
                .foregroundStyle(Theme.accent)
                .padding(.horizontal, 7)
                .padding(.vertical, 5)
                .background(Theme.accent.opacity(0.13), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            Spacer(minLength: 4)
            Text(file.name)
                .font(.system(size: 12.5))
                .foregroundStyle(Theme.ink)
                .lineLimit(3)
            Text(file.sizeLabel)
                .font(.system(size: 11.5))
                .foregroundStyle(Theme.ink3)
                .padding(.top, 2)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .padding(10)
        .background(Theme.surface)
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Theme.ink.opacity(0.07)))
    }
}

/// What's being sent: one line while it goes, each file when there are several.
struct UploadTray: View {
    @Environment(Library.self) private var library

    var body: some View {
        let ups = library.uploads
        let total = max(ups.reduce(Int64(0)) { $0 + $1.size }, 1)
        let sent = ups.reduce(0.0) { $0 + Double($1.size) * ($1.state == .done ? 1 : $1.progress) }
        let busy = library.sending
        let failed = ups.filter { if case .failed = $0.state { true } else { false } }
        let what = describe(ups.map(\.kind))
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(busy ? "Sending \(what)" : failed.isEmpty ? "Sent \(what)" : "\(failed.count) didn't send")
                    .font(.system(size: 16, weight: .semibold))
                    .foregroundStyle(Theme.ink)
                Spacer()
                if busy {
                    Text("\(Int(sent / Double(total) * 100))%").font(.system(size: 14).monospacedDigit()).foregroundStyle(Theme.ink2)
                } else if !failed.isEmpty {
                    Button("Clear") { library.clearUploads() }.font(.system(size: 15, weight: .semibold)).tint(Theme.accent)
                }
            }
            ProgressView(value: min(sent / Double(total), 1))
                .tint(!busy && failed.isEmpty ? Theme.ok : Theme.accent)
            if ups.count > 1 || !failed.isEmpty {
                ScrollView {
                    VStack(spacing: 6) {
                        ForEach(ups) { row($0) }
                    }
                }
                .frame(maxHeight: 150)
                .scrollBounceBehavior(.basedOnSize)
            }
        }
        .padding(16)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: .black.opacity(0.14), radius: 24, y: 8)
        .padding(.horizontal, 12)
        .padding(.bottom, 6)
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    private func row(_ u: Library.Upload) -> some View {
        HStack(spacing: 10) {
            Text(u.name).font(.system(size: 14)).foregroundStyle(Theme.ink).lineLimit(1).truncationMode(.middle)
            Spacer()
            switch u.state {
            case .preparing: Text("Getting it…").font(.system(size: 13)).foregroundStyle(Theme.ink3)
            case .waiting: Text("Waiting").font(.system(size: 13)).foregroundStyle(Theme.ink3)
            case .sending: Text("\(Int(u.progress * 100))%").font(.system(size: 13).monospacedDigit()).foregroundStyle(Theme.ink3)
            case .done: Image(systemName: "checkmark").font(.system(size: 12, weight: .bold)).foregroundStyle(Theme.ok)
            case let .failed(reason):
                Text(reason).font(.system(size: 13)).foregroundStyle(Theme.danger).lineLimit(1)
                Button { library.retry(u) } label: { Image(systemName: "arrow.clockwise") }
                    .font(.system(size: 13, weight: .semibold)).tint(Theme.ink2)
                    .accessibilityLabel("Try again")
            }
            if u.isBusy {
                Button { library.cancel(u) } label: { Image(systemName: "xmark") }
                    .font(.system(size: 12, weight: .semibold)).tint(Theme.ink3)
                    .accessibilityLabel("Cancel")
            }
        }
    }
}
