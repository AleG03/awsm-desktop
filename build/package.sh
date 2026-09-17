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

# What the application reports about itself, which is not the same thing.
#
# The bundle's Info.plist needs a number whatever happens, so it falls back to
# 0.1.0. The version compiled in stays empty unless this is a real release, so
# that a build made by hand reports itself as a development build instead of
# claiming to be 0.1.0 and looking permanently out of date next to the
# releases.
ldversion="${VERSION:-}"

root="$(cd "$(dirname "$0")/.." && pwd)"
out="${OUT:-$root/dist}"
app="$out/$name.app"

# The deployment target silences a page of linker warnings about objects built
# for a newer macOS than the one being linked against.
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-14.0}"

echo "building $name $version"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"

binary="$app/Contents/MacOS/$name"

# ARCH says what to build for: arm64, amd64, or universal for one binary that
# runs on both. It defaults to this machine's, so a local build stays a local
# build and does not pay for a second architecture nobody here is going to run.
#
# The release builds universal. Two downloads meant choosing between them, and
# choosing wrong does not fail cleanly: macOS runs the Intel one under Rosetta
# and warns about it, which reads as something being wrong with the application.
#
# The architecture is passed to the compiler explicitly in every case rather
# than letting it be whatever the host happens to be, so this behaves the same
# on an Apple silicon Mac and on an Intel one.
arch="${ARCH:-$(go env GOARCH)}"

# slice builds one architecture: goarch, the name clang knows it by, output.
slice() {
	echo "  compiling for $2"
	(cd "$root" && GOARCH="$1" CGO_ENABLED=1 \
		CC="clang -arch $2" CXX="clang++ -arch $2" \
		go build -trimpath -ldflags "-s -w -X main.version=$ldversion" -o "$3" .)
}

case "$arch" in
arm64)
	slice arm64 arm64 "$binary"
	;;
amd64)
	slice amd64 x86_64 "$binary"
	;;
universal)
	parts="$out/slices"
	rm -rf "$parts"
	mkdir -p "$parts"
	slice arm64 arm64 "$parts/arm64"
	slice amd64 x86_64 "$parts/amd64"
	# lipo puts both into one file. Nothing is shared between them -- they are
	# two different sets of machine code -- so the result is the size of both.
	lipo -create "$parts/arm64" "$parts/amd64" -output "$binary"
	rm -rf "$parts"
	;;
*)
	echo "unknown architecture: $arch (want arm64, amd64 or universal)" >&2
	exit 1
	;;
esac


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
