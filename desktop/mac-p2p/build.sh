#!/bin/zsh
# Builds ferry-p2p (Ferry on Apple's peer-to-peer Wi-Fi, main.swift) for Apple silicon and Intel Macs, into
# the file named, and signs it ad hoc. Ferry looks for it beside its own program.
#   desktop/mac-p2p/build.sh dist/ferry-p2p
set -e
out=${1:?usage: build.sh <output file>}
here=${0:A:h}
tmp=$(mktemp -d)
for arch in arm64 x86_64; do
  swiftc -O -swift-version 5 -target $arch-apple-macos12 -o $tmp/$arch $here/main.swift
done
lipo -create -output $out $tmp/arm64 $tmp/x86_64
codesign --force --sign - --identifier app.ferry.p2p $out
rm -rf $tmp
