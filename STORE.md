# Getting Ferry into the stores

Everything Ferry needs for TestFlight, the App Store and Google Play: what's ready, what to paste where, and the steps only the account holder can do.

## Ready

| | Where |
|---|---|
| iPhone build for App Store Connect | `ios/upload.sh` archives it and uploads it (Release, version 1.0.0; the build number counts up on its own) |
| Privacy manifests (no tracking, nothing collected) | `ios/Ferry/PrivacyInfo.xcprivacy`, `ios/FerryShare/PrivacyInfo.xcprivacy` |
| Export compliance | `ITSAppUsesNonExemptEncryption = NO`: Ferry only uses the encryption built into iOS and Android (TLS) |
| Android App Bundle, signed with the upload key | `android/app/build/outputs/bundle/release/app-release.aab` (`./gradlew bundleRelease`) |
| Upload key | `~/.ferry-signing/` (outside the repo). **Back up `ferry-upload.jks` and `upload.properties` in your password manager.** Google can reset a lost upload key, but it takes days |
| Privacy policy | https://ferry-chi.vercel.app/privacy (the same text as `PRIVACY.md`) |
| Website | https://ferry-chi.vercel.app |
| Support page | https://github.com/lenajeremy/ferry/issues |

## Your part

### TestFlight (iPhone)

1. In [App Store Connect](https://appstoreconnect.apple.com) › Apps › **+** › New App: platform iOS, bundle ID **app.ferry.ios**, SKU `ferry-ios`, and a name (it must be unique on the App Store; "Ferry" alone is likely taken, so try one from the list below).
2. Either sign Xcode in to the team's Apple account (Xcode › Settings › Accounts), or create an App Store Connect API key (Users and Access › Integrations › App Store Connect API, role App Manager) and keep its `.p8` file, key ID and issuer ID.
3. Run `ios/upload.sh` (with the key: `ASC_KEY_PATH=… ASC_KEY_ID=… ASC_ISSUER_ID=… ios/upload.sh`).
4. When the build appears in TestFlight, add yourself as an internal tester. Internal testing needs no review. External testers need a short Beta App Review, using the notes below.

### Google Play (Android)

1. A [Google Play developer account](https://play.google.com/console) (a one-time fee). Personal accounts created after November 2023 must run a closed test with at least 12 testers for 14 days before going public.
2. Create app › name, default language, App, Free.
3. Set up the app content: privacy policy URL (above), no ads, content rating questionnaire (no objectionable content → Everyone), target audience (13+ is simplest), data safety (answers below).
4. Testing › Internal testing › Create release › upload `app-release.aab`, and let Google manage the app signing key (Play App Signing).
5. Add testers by email and share the opt-in link.

The package name is **app.ferry** and the iPhone bundle ID is **app.ferry.ios**. If Google or Apple say either is taken, change it (`android/app/build.gradle.kts`, `ios/project.yml`) before the first upload: it can't change afterwards.

## Store text

**Name ideas** (30 characters at most): Ferry: Send Files Anywhere · Ferry – Phone to Computer · Ferry File Transfer

**Subtitle / short description:** Photos and files, phone to computer

**Description:**

> Ferry sends photos, videos and files between your phones and computers over Wi‑Fi, encrypted. Mac, Windows and Linux, iPhone and Android: every pairing works.
>
> • From your phone: pick photos and videos, or share from any app. They land on your computer in seconds, as the originals.
> • From your computer: drop files on Ferry or paste a screenshot. They're on your phone straight away.
> • Straight into your photo library, or copy a photo and paste it anywhere.
> • Pair once by scanning the code on your computer. Pair as many phones and computers as you like, and switch between them in a tap.
> • Private: no account and no cloud. Files go straight from one device to the other, encrypted, and only devices you've paired can see them.
>
> Ferry needs the free Ferry app on your computer: download it from github.com/lenajeremy/ferry.

**Keywords (App Store, 100 characters):** file transfer,send files,share photos,windows,pc,linux,wifi,phone to computer,copy,send video

Keep "AirDrop" out of both store listings: Apple rejects other products' names in metadata, and Google can too. The website can keep the comparison.

**Category:** Utilities (App Store) · Tools (Google Play)

**Age rating:** 4+ / Everyone

## Privacy answers

- **App Store › App Privacy:** Data Not Collected.
- **Google Play › Data safety:** no data collected and no data shared; data is encrypted in transit: yes; account deletion: not applicable (no accounts).

## Notes for app review

> Ferry moves files between this phone and the user's own computer on the same network. To try it, install Ferry on a Mac or PC from https://github.com/lenajeremy/ferry/releases (Ferry-mac.dmg or Ferry-windows-x64.exe), open it, and tap "Scan the code" in the app to scan the code on the computer's screen. No account or login is needed. The camera is used only to scan that code; the local network permission finds the computer on the Wi‑Fi.

## Still to make

- **App Store screenshots:** 6.9" iPhone (1320 × 2868), three to ten. They're needed for the App Store listing, not for TestFlight.
- **Google Play graphics:** at least two phone screenshots, a 512 × 512 icon and a 1024 × 500 feature graphic. Internal testing doesn't need them; a public listing does.

## Later, for the computers

- **Mac:** sign `Ferry.app` with the team's Developer ID Application certificate (already in this Mac's keychain) and notarize it. Gatekeeper then opens it without the warning (`xcrun notarytool` with the same App Store Connect API key).
- **Windows:** a code-signing certificate stops SmartScreen's warning.
- **Linux:** Snap Store or Flathub listings, if you want them.
