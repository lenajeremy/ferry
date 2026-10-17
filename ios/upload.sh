#!/bin/zsh
# Archives Ferry for the App Store and uploads it to App Store Connect, where it shows up in TestFlight.
# Needs either Xcode signed in to the team's Apple account (Xcode › Settings › Accounts), or an App Store
# Connect API key: ASC_KEY_PATH=/path/AuthKey_XXXX.p8 ASC_KEY_ID=XXXX ASC_ISSUER_ID=uuid ./upload.sh
# The app must exist in App Store Connect first (bundle ID app.ferry.ios).
set -e
cd "${0:A:h}"
# App Store Connect only takes builds from released Xcodes (or release candidates), never betas.
if [[ -z $DEVELOPER_DIR && -d /Applications/Xcode.app ]]; then export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer; fi
echo "Building with $(xcodebuild -version | head -1)"
xcodegen generate -q
auth=()
if [[ -n $ASC_KEY_PATH ]]; then
  auth=(-authenticationKeyPath "$ASC_KEY_PATH" -authenticationKeyID "$ASC_KEY_ID" -authenticationKeyIssuerID "$ASC_ISSUER_ID")
fi
rm -rf build/Ferry.xcarchive
xcodebuild -project Ferry.xcodeproj -scheme Ferry -configuration Release -destination 'generic/platform=iOS' \
  -archivePath build/Ferry.xcarchive -allowProvisioningUpdates $auth archive | grep -E "error|ARCHIVE" || true
[[ -d build/Ferry.xcarchive ]] || { echo "The archive failed."; exit 1; }
out=$(xcodebuild -exportArchive -archivePath build/Ferry.xcarchive -exportOptionsPlist ExportOptions.plist \
  -exportPath build/export -allowProvisioningUpdates $auth 2>&1) || true
echo "$out" | grep -E "error|EXPORT|Upload|upload" || true
if ! echo "$out" | grep -q "EXPORT SUCCEEDED"; then
  echo "Not uploaded (the reason is above). Signing needs Xcode signed in to the team, or an App Store Connect API key (see the top)."
  exit 1
fi
echo "Uploaded. It shows in App Store Connect › TestFlight once Apple has processed it (usually 5–15 minutes)."
