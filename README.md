# Ferry

Share photos, videos and any other file between your computer (macOS, Windows or Linux) and your phone, both ways, over your own Wi-Fi. No cloud, no account, no AirDrop.

- **On the computer,** Ferry is a small app for macOS, Windows and Linux (`desktop/`): open it and it shows Ferry's page in its own window. Drop files on it (or paste a screenshot) and they appear on your phone. The same Ferry also comes as a command-line program you use in any browser.
- **On the phone,** use the Ferry app for **iPhone** (`ios/`) or **Android** (`android/`), or just the phone's browser.
- Everything sent lands in a **Ferry** folder in your home folder (`~/Ferry`), and anything you put there yourself is shared too.

The website is in `site/`: the home page, `download.html` (it offers the file for the visitor's system) and the screenshots in `site/img/`. Every download link points at the GitHub release `lenajeremy/ferry` v1.0.0 (`RELEASE` in `site/site.js`).

## The desktop app

`desktop/` is Ferry in a window (Go with [Wails](https://wails.io) v2, using the system's own web view: about 12 MB). It starts Ferry and shows its page; if Ferry already runs on this computer (at login or in a terminal), it shows that one instead. On a Mac it adds **File › Send Files…** (⌘O) and **Open the Ferry Folder** (⇧⌘O). Quitting the app stops the Ferry it started.

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
cd desktop
wails build -platform darwin/universal -trimpath -s       # build/bin/Ferry.app
wails build -platform windows/amd64 -trimpath -s          # build/bin/Ferry.exe (cross-built from the Mac)
```

Linux builds need GTK and WebKitGTK, so they're made in Docker on Ubuntu 22.04 (`desktop/linux/`, used by `release.sh`); the result runs on Ubuntu 22.04+ and Debian 12+. `FERRY_PORT`, `FERRY_DATA`, `FERRY_DIR`, `FERRY_NAME` and `FERRY_BONJOUR=0` let a copy of the app run beside your real Ferry, for screenshots.

## Start it

### macOS

Ferry starts on its own when you log in (a launch agent, `~/Library/LaunchAgents/app.ferry.plist`) and restarts if it ever quits. Open its page at **http://localhost:7070**, or double-click **Start Ferry.command**. Its log is `~/Library/Logs/Ferry.log`.

```sh
launchctl kickstart -k gui/$(id -u)/app.ferry    # restart it (after rebuilding: go build -o ferry .)
launchctl bootout gui/$(id -u)/app.ferry         # stop it until the next login
rm ~/Library/LaunchAgents/app.ferry.plist        # …and for good (with the bootout)
```

### Windows

Open the app (`Ferry-windows-x64.exe`), or run the command-line `ferry-cli-windows-amd64.exe`. The first time, Windows Firewall asks whether Ferry may use the network: allow it on **private** networks, or phones can't reach it. To start it at sign-in:

```bat
schtasks /create /tn Ferry /sc onlogon /tr "\"%USERPROFILE%\ferry\ferry.exe\" -open=false"
```

Deleted files go to the Recycle Bin. Ferry's data is in `%AppData%\Ferry`.

### Linux

Install the app (`sudo apt install ./Ferry-linux-x64.deb`, or unpack the `.tar.gz`), or use the command-line version: `brew install lenajeremy/ferry/ferry && brew services start ferry` runs it from login. Without Homebrew, systemd does the same:

```ini
# ~/.config/systemd/user/ferry.service
[Unit]
Description=Ferry
[Service]
ExecStart=%h/ferry/ferry -open=false
Restart=on-failure
[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now ferry
```

Deleted files go to the Trash (with `gio`, or by the freedesktop Trash rules). Ferry's data is in `~/.config/Ferry`. Thumbnails of JPEG, PNG and GIF photos work out of the box; videos and HEIC need `ffmpeg` installed.

## Connect your phone (once)

- **With the app:** open Ferry and tap **Scan the code**, then point it at the QR code on the computer's page. The app also lists computers running Ferry on the Wi-Fi; pick yours and type its code.
- **In the browser:** point the camera at the QR code and open the link (or type the address and the code). Then Share › **Add to Home Screen**.

Pairing trades the code for a key of that device's own, so the phone stays connected. The computer's page lists every paired phone and browser (**Paired devices**), shows which are connected now, and can **Remove** any one of them. **New code** makes a fresh code; devices already paired stay connected, and the old code stops pairing anything new. A phone can be paired with several computers (home and work) and switch between them from the computer menu at the top left.

## The iPhone app

`ios/` is Ferry for iPhone (SwiftUI, iOS 26):

- **Photos** sends photos and videos as their originals (HEIC, HEVC, with their dates and places); **Files** sends anything from the Files app.
- **Save** puts photos and videos straight into Photos, one or many (Select). **Copy** puts a photo on the clipboard.
- **Share** and **Open** (Quick Look) for everything else; **Delete** moves it to the computer's Trash.
- **Ferry in the share sheet**: share from Photos or any app and it goes straight to the computer.
- **Encrypted** to every computer it pairs with by QR code, and **several computers** at once (the computer menu switches, adds and forgets). A computer that removes the phone is forgotten, with a note saying so.

```sh
cd ios
xcodegen generate
xcodebuild -project Ferry.xcodeproj -scheme Ferry -destination 'id=<iPhone UDID>' -allowProvisioningUpdates build
```

The app and its share extension share the pairing through a keychain group (`<team>.app.ferry.shared`), which the team's wildcard profile allows, so it builds without Xcode being signed in. In Debug: `-ferryQR <link>` pairs on launch as if the QR code had been scanned (the Simulator has no camera), `-ferryURL http://… -ferryCode <code>` as if typed, `-ferryLegacyURL … -ferryLegacyCode …` recreates an older pairing to try moving it to a key, `-ferrySend <path>` sends a file, `-ferryAdd YES` opens Add a Computer, and `-ferryOpen <name>` / `-ferrySelect 3` open the viewer or Select mode. Run the computer with `FERRY_LAN_IS_REMOTE=1` so the Simulator counts as a phone.

Videos from a secure address play through `SecureStream` (Ferry's own connection hands the player its bytes), since the player won't trust Ferry's certificate on its own.

## The Android app

`android/` is Ferry for Android (Kotlin, Jetpack Compose, Android 10 and later), with the same features: Photos (the system photo picker) and Files, **Save** to the gallery (Pictures/Ferry, Movies/Ferry), **Copy**, **Share**, **Open**, Delete, Select, and **Ferry in the share sheet**. It finds computers on the Wi-Fi with Android's network service discovery, talks to each one encrypted (pinned to that computer's own certificate authority, as on the iPhone), and keeps several computers: the name at the top opens **Your computers** to switch, add or forget.

```sh
cd android
export JAVA_HOME=$(/usr/libexec/java_home -v 21)   # or Homebrew's openjdk@21
./gradlew assembleRelease        # app/build/outputs/apk/release/app-release.apk (about 4.5 MB)
adb install -r app/build/outputs/apk/release/app-release.apk
```

Release builds are signed with the upload key in `~/.ferry-signing/` when it's there (kept out of the repo; back it up), else with the debug key. `./gradlew bundleRelease` makes the bundle for Google Play. Debug builds pair on launch with `--es ferryQR "<the QR link>"` (as if scanned) or `--es ferryUrl http://… --es ferryCode <code>` (as if typed), and take `--es ferryOpen <name>` / `--ei ferrySelect 3`. To test pairing for real, run the computer with `FERRY_LAN_IS_REMOTE=1` and reach it at its network address: the emulator's 10.0.2.2 arrives as the computer itself.

## Computer to computer

Each computer's page (in the app or a browser) has **Other computers**: the computers running Ferry on the same network, found by Bonjour. **Pair** asks for the other computer's code once; the two are then paired both ways. Then:

- **Send files** (or drop files on that computer in the list) puts them in its Ferry folder.
- **Their files** shows its Ferry folder; click a file to save a copy on this computer.
- **Forget** unpairs both ways. **Add one by its address…** pairs with a computer that doesn't show up (a network that blocks Bonjour), by the address shown under its QR code.

Computers talk to each other only over their secure addresses, each trusting just the other's certificate authority (checked against the fingerprint it announces, or fetched from it when typed by address). On a Mac they're found with `dns-sd`; on Windows and Linux, Ferry's own announcer listens for the others' announcements and asks the network every minute.

## Saving to Photos on the iPhone, in Safari

The app saves to Photos in one tap. Without it, Safari saves a photo by pressing and holding it in Ferry's viewer. For one-tap saving in Safari too, Ferry has a secure page (Safari only hands files to the share sheet, and lets a page copy images, over a secure connection). The phone page walks you through it (the **Save straight to Photos** card): download Ferry's certificate, install it in Settings, turn it on in Settings › General › About › Certificate Trust Settings, then use the secure page (port 7071) from then on.

The certificate is Ferry's own, made on your computer. It is limited to `.local` names, `localhost` and home network addresses, so it can't vouch for any website. Remove it any time in Settings › General › VPN & Device Management.

## Good to know

- **Copy** puts an image on the clipboard: in the apps, and in the browser on the computer (or on the phone's secure page).
- **Deleting** a file in Ferry (from any device) moves it to the computer's Trash or Recycle Bin.
- **Folders** can't be sent; compress them first.
- **Names:** phones hand over picked photos as `image.jpg` (iPhone) or a number (Android), so Ferry names those `Photo 2026-10-17 at 15.04.05.jpg`. Same names get a number (`notes 2.txt`).
- **Big videos:** keep Ferry open on the phone until the bar finishes.
- **Notifications:** on the computer's page, **Notify me when files arrive** shows a notification when the phone sends something while the page is in the background.

## Options

```
ferry -dir ~/Ferry        the shared folder (default ~/Ferry)
      -port 7070          the page
      -tls-port 7071      the secure page (0 turns it off)
      -name "Studio PC"   the name the phones see (default: the computer's name)
      -open=false         don't open the browser on start
      -data <folder>      Ferry's own data (default: Application Support, %AppData% or ~/.config)
      -bonjour=false      don't announce Ferry on the network (the apps find it that way)
```

## The app stores

`STORE.md` has everything for TestFlight, the App Store and Google Play: what's ready, the listing text, the privacy answers, notes for review, and the steps that need the developer accounts. `ios/upload.sh` archives the iPhone app and uploads it to App Store Connect. The privacy policy is `PRIVACY.md`.

## Who can get in

- This computer, by `localhost` or its own address.
- A device that paired with the 8-character code: by the QR link, the pairing page, or the apps (`POST /api/pair`). Pairing gives it a token of its own (an `Authorization: Ferry <token>` header in the apps, a cookie good for 400 days in a browser); the code itself never signs anything in. Tokens are kept hashed in `devices.json`. A wrong code waits a second, one guess at a time.
- **Encryption in the apps.** The QR link carries the fingerprint of the computer's own certificate authority (`f=`, base64url SHA-256, first 128 bits) and its secure port (`s=`). The app fetches the authority from `/ferry.cer`, checks it against the fingerprint, and sends the code only over TLS to the secure port, trusting nothing else for that computer from then on. Typing the code instead trusts the authority the computer hands out at that moment (checked against the fingerprint it announces on the network). Phone browsers stay on the plain address unless you set up the secure page.
- Changes (sending, deleting) also need Ferry's own request header and the page's own origin, so another website open in your browser can't use Ferry.
- Files are only ever served from the shared folder (no paths, no hidden files), and nothing in it can run as a page.

## Builds

```sh
./release.sh     # everything for download, into dist/
go test ./...
```

`release.sh` makes the apps (`Ferry-mac.dmg`, `Ferry-windows-{x64,arm64}.exe`, `Ferry-linux-{x64,arm64}.{deb,tar.gz}`, `Ferry-android.apk`) and the command-line builds (`ferry-cli-{macos,linux,windows}-{arm64,amd64}`). The Linux apps need Docker running (OrbStack); the Android app needs JDK 21.

The apps aren't signed by Apple or Microsoft yet, so the first launch shows a warning (macOS: System Settings › Privacy & Security › Open Anyway; Windows: More info › Run anyway).

### Homebrew

`packaging/homebrew.sh` writes a Homebrew tap for the files in `dist/` (with their checksums): `Formula/ferry.rb` (the command-line Ferry for macOS and Linux, with `brew services`) and `Casks/ferry-app.rb` (the Mac app). To make `brew install lenajeremy/ferry/ferry` work: upload `dist/` to the GitHub release `v1.0.0` of `lenajeremy/ferry`, and push `packaging/homebrew/` as the repo `lenajeremy/homebrew-ferry`.

## How it's built

Ferry itself is Go, standard library only: the `server` package, with the page embedded. The command-line program (`main.go`) and the desktop app (`desktop/`, which adds Wails) both run it.

| File | What it does |
|---|---|
| `main.go` | The command-line program: flags, the terminal banner and QR code |
| `server/run.go` | Starting Ferry: the folder, the pages, the secure page, the network announcement |
| `server/server.go` | Pages, pairing, uploads, downloads, thumbnails, live updates (server-sent events) |
| `server/store.go` | The shared folder: listing, saving uploads under free names, deleting to the Trash |
| `server/platform.go` | What differs by system: opening and revealing files, the Trash or Recycle Bin, names, the data folder |
| `server/thumbs.go`, `thumbs_portable.go` | Thumbnails: `sips` and Quick Look on a Mac; Go (JPEG, PNG, GIF, turned upright) and `ffmpeg` elsewhere |
| `server/mdns.go`, `server/sockopt_*.go` | Ferry's own Bonjour announcer for Windows and Linux (macOS uses its `dns-sd`) |
| `server/devices.go`, `server/pairing.go` | Paired devices and their tokens; pairing the apps, removing devices, new codes |
| `server/peers.go`, `server/discovery.go` | Other computers: finding them, pairing both ways, sending, their files |
| `server/certs.go` | Ferry's name-constrained certificate authority and the secure page's certificate |
| `server/host.go` | The computer's name, `.local` name and network addresses |
| `qr/` | A QR code encoder (byte mode, level M, versions 1–10) |
| `server/web/index.html` | The page, for the computer (browser or app) and the phone's browser |
| `desktop/` | The desktop app (Wails): the window, the Mac menu, the Linux packaging |
| `packaging/` | The Homebrew tap |
| `site/` | The website and downloads page |

To see the phone's page in the iOS Simulator, run with `FERRY_LAN_IS_REMOTE=1` and open `http://<the computer's IP>:7070/?k=<code>` in the Simulator's Safari; without it the Simulator counts as the computer itself. `FERRY_DEMO=1` shows a generic computer (`macbook-pro.local`, `~/Ferry`) for screenshots. `FERRY_MDNS=builtin` uses Ferry's own announcer on a Mac too (macOS's Local Network privacy may block it from a terminal). `FERRY_QR_PNG=<folder> go test ./qr -run WritePNGs` writes sample QR codes to check with any reader.
