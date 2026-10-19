#!/bin/sh
# Runs inside the Linux build container: builds Ferry and packages it as a .deb and a .tar.gz in /out.
set -e
arch=$(dpkg --print-architecture)            # amd64 or arm64
label=$([ "$arch" = amd64 ] && echo x64 || echo arm64)
cd /src/desktop
version=$(sed -n 's/.*"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
go build -tags desktop,production,webkit2_41 -trimpath -ldflags "-s -w" -o /tmp/Ferry .

# .deb: /usr/lib/ferry/Ferry, a menu entry and icons.
pkg=/tmp/deb && rm -rf $pkg
install -D -m 755 /tmp/Ferry $pkg/usr/lib/ferry/Ferry
install -D -m 644 linux/app.ferry.Ferry.desktop $pkg/usr/share/applications/app.ferry.Ferry.desktop
install -D -m 644 build/appicon.png $pkg/usr/share/icons/hicolor/1024x1024/apps/app.ferry.Ferry.png
mkdir -p $pkg/usr/bin && ln -s ../lib/ferry/Ferry $pkg/usr/bin/ferry-app
mkdir -p $pkg/DEBIAN
cat > $pkg/DEBIAN/control <<CONTROL
Package: ferry
Version: $version
Architecture: $arch
Maintainer: Ferry <hello@ferry.app>
Depends: libgtk-3-0 | libgtk-3-0t64, libwebkit2gtk-4.1-0
Section: net
Priority: optional
Description: Photos, videos and files between your phone and your computer
 Ferry sends photos, videos and any other file between your phone and this
 computer over your own Wi-Fi. No cloud, no account.
CONTROL
dpkg-deb --root-owner-group --build $pkg /out/Ferry-linux-$label.deb >/dev/null

# .tar.gz for everything else: the program, its icon and menu entry.
t=/tmp/tar/Ferry && rm -rf /tmp/tar && mkdir -p $t
cp /tmp/Ferry $t/Ferry && cp build/appicon.png $t/ferry.png && cp linux/app.ferry.Ferry.desktop $t/
tar -C /tmp/tar -czf /out/Ferry-linux-$label.tar.gz Ferry
echo "built Ferry-linux-$label"
