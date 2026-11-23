#!/bin/bash
# Builds a universal (Apple silicon + Intel) InternetMonitor.app into ./dist, bundling the icon and the Linux agent
# binaries (used by the one-click SSH install). Also writes dist/InternetMonitor-macOS-universal.zip for releases.
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${VERSION:-1.1.1}"
APP=dist/InternetMonitor.app
[ -f agent/dist/imonitord-linux-arm64 ] || (cd agent && make release)
(cd app && swift build -c release --arch arm64 --arch x86_64 --product InternetMonitor)
BIN=$(cd app && swift build -c release --arch arm64 --arch x86_64 --show-bin-path)/InternetMonitor
rm -rf "$APP"; mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/InternetMonitor"
cp assets/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
for f in agent/dist/imonitord-linux-*; do [ -f "$f" ] && cp "$f" "$APP/Contents/Resources/"; done
cp LICENSE "$APP/Contents/Resources/LICENSE.txt"
cat > "$APP/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Internet Monitor</string>
<key>CFBundleDisplayName</key><string>Internet Monitor</string>
<key>CFBundleIdentifier</key><string>com.wasim.internetmonitor</string>
<key>CFBundleExecutable</key><string>InternetMonitor</string>
<key>CFBundleIconFile</key><string>AppIcon</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$VERSION</string>
<key>CFBundleVersion</key><string>$VERSION</string>
<key>LSMinimumSystemVersion</key><string>14.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>NSHumanReadableCopyright</key><string>MIT License. Developed by Wasim Osman.</string>
<key>NSLocalNetworkUsageDescription</key><string>Internet Monitor scans your local network for machines to monitor and talks to the monitoring agents on them.</string>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/><key>NSAllowsArbitraryLoads</key><true/></dict>
</dict></plist>
PL
codesign --force --deep -s - "$APP" >/dev/null
echo "archs: $(lipo -archs "$APP/Contents/MacOS/InternetMonitor")"
(cd dist && rm -f InternetMonitor-macOS-universal.zip && ditto -c -k --keepParent InternetMonitor.app InternetMonitor-macOS-universal.zip)
echo "Built $APP and dist/InternetMonitor-macOS-universal.zip"
