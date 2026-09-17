#!/bin/sh
# Assemble awsm.app from the Go binary.
#
# A bundle rather than a bare executable, for three reasons: LSUIElement is what
# keeps the app out of the Dock, the login item registers a bundle and not a
# path, and macOS attributes permissions and preferences to a bundle identifier.
#
# No Xcode involved. The signature is ad-hoc, which is enough to run locally and
# not enough to distribute: that would need a Developer ID and notarisation.
set -eu

name="awsm"
identifier="io.github.aleg03.awsm.desktop"
version="${VERSION:-0.1.0}"

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${OUT:-$root/dist}"
app="$out/$name.app"

# The deployment target silences a page of linker warnings about objects built
# for a newer macOS than the one being linked against.
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-14.0}"

echo "building $name $version"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

# ARCH names the architecture to build for, as Go spells it. It defaults to
# this machine's, so a local build stays a local build; the release sets it and
# runs this twice, because a Mac is one or the other and a download for the
# wrong one does not start at all.
#
# The architecture is passed to the compiler explicitly in both cases rather
# than letting one of them be whatever the host happens to be, so this behaves
# the same on an Apple silicon Mac and on an Intel one.
arch="${ARCH:-$(go env GOARCH)}"
case "$arch" in
arm64) machine=arm64 ;;
amd64) machine=x86_64 ;;
*)
	echo "unknown architecture: $arch (want arm64 or amd64)" >&2
	exit 1
	;;
esac

echo "  for $machine"
(cd "$root" && GOARCH="$arch" CGO_ENABLED=1 \
	CC="clang -arch $machine" CXX="clang++ -arch $machine" \
	go build -trimpath -ldflags "-s -w" -o "$app/Contents/MacOS/$name" .)

# The icon is drawn from the same geometry as the status bar mark rather than
# checked in, so there is no binary in the repository and every size is rendered
# rather than resampled. iconutil ships with the Command Line Tools.
iconset="$out/$name.iconset"
rm -rf "$iconset"
(cd "$root" && go run ./build/icon "$iconset")
iconutil -c icns -o "$app/Contents/Resources/$name.icns" "$iconset"
rm -rf "$iconset"

cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>$name</string>
	<key>CFBundleDisplayName</key>
	<string>awsm</string>
	<key>CFBundleIdentifier</key>
	<string>$identifier</string>
	<key>CFBundleExecutable</key>
	<string>$name</string>
	<key>CFBundleIconFile</key>
	<string>$name</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>$version</string>
	<key>CFBundleVersion</key>
	<string>$version</string>
	<key>LSMinimumSystemVersion</key>
	<string>13.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<!-- The app lives in the status bar: no Dock icon, no app switcher entry. -->
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
PLIST

codesign --force --sign - --timestamp=none "$app" >/dev/null 2>&1 ||
	echo "warning: ad-hoc signing failed; the app will still run"

echo "built $app"
