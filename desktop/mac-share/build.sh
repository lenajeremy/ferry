#!/bin/zsh
# Builds the share extension and puts it inside a Ferry.app, then signs the app again so macOS
# accepts what's in it:  desktop/mac-share/build.sh desktop/build/bin/Ferry.app
set -e
app=${1:A}
[[ -d $app ]] || { echo "usage: $0 path/to/Ferry.app" >&2; exit 1; }
cd "${0:A:h}"
xcodegen generate --quiet
xcodebuild -project FerryShare.xcodeproj -scheme FerryShare -configuration Release -destination 'generic/platform=macOS' -derivedDataPath build build | grep -E "error:|BUILD FAILED" || true
test -d build/Build/Products/Release/FerryShare.appex
mkdir -p "$app/Contents/PlugIns"
rm -rf "$app/Contents/PlugIns/FerryShare.appex"
cp -R build/Build/Products/Release/FerryShare.appex "$app/Contents/PlugIns/"
codesign --force --sign - "$app"
