#!/usr/bin/env bash
# Builds "Agent Command.app" and a zip of it in dist/. Run scripts/build-ghostty.sh
# first. With --install, also copies the app to /Applications.
# It's signed with the identity in ACMD_SIGN_IDENTITY: the long-lived self-signed
# certificate for a release (scripts/create-signing-cert.sh), ad hoc otherwise.
# ACMD_SIGN_KEYCHAIN names the keychain holding it: a self-signed certificate isn't
# trusted, so codesign doesn't list it as an identity without one.
# ACMD_VERSION sets the version, which otherwise comes from git.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/dist"
app="$dist/Agent Command.app"
module="github.com/brettmostert/agent-command"
version="${ACMD_VERSION:-$(git -C "$root" describe --tags --always --dirty)}"
sha="$(git -C "$root" rev-parse --short HEAD)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

(cd "$root/app" && swift build -c release)
bin="$(cd "$root/app" && swift build -c release --show-bin-path)"

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin/AgentCommand" "$app/Contents/MacOS/Agent Command"
(cd "$root" && CGO_ENABLED=1 go build -trimpath -tags libghostty \
	-ldflags "-X $module/internal/version.Version=$version -X $module/internal/version.Commit=$sha -X $module/internal/version.Date=$date" \
	-o "$app/Contents/MacOS/agent-command" ./cmd/agent-command)
# Built outside the app: make can't take a path with a space.
make -C "$root" speech-helper SPEECH_OUT="$dist/acmd-speech"
cp "$dist/acmd-speech" "$app/Contents/MacOS/acmd-speech"
cp -R "$root/app/Sources/AgentCommand/Resources/Fonts" "$app/Contents/Resources/Fonts"
cp "$root/packaging/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
mkdir -p "$app/Contents/Resources/ghostty"
cp -R "$root/app/vendor/ghostty-resources/themes" "$app/Contents/Resources/ghostty/themes"

cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Agent Command</string>
	<key>CFBundleDisplayName</key><string>Agent Command</string>
	<key>CFBundleIdentifier</key><string>com.brettmostert.agent-command</string>
	<key>CFBundleExecutable</key><string>Agent Command</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${version#v}</string>
	<key>LSMinimumSystemVersion</key><string>14.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Inner executables first: signing with --deep is deprecated.
identity="${ACMD_SIGN_IDENTITY:--}"
sign=(codesign --force --sign "$identity")
if [[ -n "${ACMD_SIGN_KEYCHAIN:-}" ]]; then
	sign+=(--keychain "$ACMD_SIGN_KEYCHAIN")
fi
"${sign[@]}" "$app/Contents/MacOS/agent-command" "$app/Contents/MacOS/acmd-speech"
"${sign[@]}" "$app"
(cd "$dist" && rm -f AgentCommand.zip && ditto -c -k --keepParent "Agent Command.app" AgentCommand.zip)
echo "build-app: built $app"

if [[ "${1:-}" == "--install" ]]; then
	rm -rf "/Applications/Agent Command.app"
	cp -R "$app" /Applications/
	echo "build-app: installed /Applications/Agent Command.app"
	echo "build-app: open the app, which offers to add its hooks on first launch"
fi
