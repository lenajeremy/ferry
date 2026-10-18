#!/bin/zsh
# Builds everything Ferry offers for download into dist/:
#   the apps   Ferry-mac.dmg, Ferry-windows-{x64,arm64}.exe, Ferry-linux-{x64,arm64}.{deb,tar.gz}, Ferry-android.apk
#   the CLI    ferry-cli-{macos,linux,windows}-{arm64,amd64} (the same Ferry, run from a terminal)
# The Linux apps need Docker (they're built on Ubuntu 22.04); the Android app needs JDK 21 and the Android SDK.
set -e
cd "${0:A:h}"
rm -rf dist && mkdir -p dist
wails=$(go env GOPATH)/bin/wails

echo "• command-line Ferry"
for target in darwin/arm64:macos-arm64 darwin/amd64:macos-amd64 linux/amd64:linux-amd64 linux/arm64:linux-arm64 \
              windows/amd64:windows-amd64.exe windows/arm64:windows-arm64.exe; do
  platform=${target%%:*}
  GOOS=${platform%/*} GOARCH=${platform#*/} CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/ferry-cli-${target#*:} .
done

echo "• Mac app"
(cd desktop && $wails build -platform darwin/universal -trimpath -s -clean >/dev/null)
# Share › Ferry: the share extension goes inside the app (needs Xcode and XcodeGen).
desktop/mac-share/build.sh desktop/build/bin/Ferry.app
stage=$(mktemp -d) && cp -R desktop/build/bin/Ferry.app $stage/ && ln -s /Applications $stage/Applications
hdiutil create -quiet -volname Ferry -srcfolder $stage -ov -format UDZO dist/Ferry-mac.dmg && rm -rf $stage

echo "• Windows app"
for arch in x64:amd64 arm64:arm64; do
  (cd desktop && $wails build -platform windows/${arch#*:} -trimpath -s -o Ferry-${arch%%:*}.exe >/dev/null)
  cp desktop/build/bin/Ferry-${arch%%:*}.exe dist/Ferry-windows-${arch%%:*}.exe
done

if docker info >/dev/null 2>&1; then
  echo "• Linux app"
  for arch in amd64 arm64; do
    docker build -q --platform linux/$arch -t ferry-linux-build:$arch desktop/linux >/dev/null
    docker run --rm --platform linux/$arch -v "$PWD":/src -v "$(go env GOMODCACHE)":/root/go/pkg/mod -v "$PWD/dist":/out \
      ferry-linux-build:$arch sh /src/desktop/linux/build.sh 2>/dev/null | tail -1
  done
else
  echo "• Linux app skipped: start Docker (OrbStack) to build it"
fi

if [[ -x android/gradlew ]]; then
  echo "• Android app"
  (cd android && JAVA_HOME=${JAVA_HOME:-$(/usr/libexec/java_home -v 21)} ./gradlew -q assembleRelease)
  cp android/app/build/outputs/apk/release/app-release.apk dist/Ferry-android.apk
fi
ls -lh dist | awk 'NR>1 {print $5, $9}'
