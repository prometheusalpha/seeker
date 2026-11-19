#!/usr/bin/env bash
# Build Seeker.app signed with the local "Telepath Local Signing" cert and
# install it into ~/Applications. The identity and bundle ID stay the same across
# builds, so macOS keeps the Keychain items and permissions already granted.
set -euo pipefail

cd "$(dirname "$0")"

go tool mygo build --skip-dmg

app=$(find build -name 'Seeker.app' -type d -print -quit)
[ -n "$app" ] || { echo "Seeker.app not found under build/" >&2; exit 1; }

codesign --verify --deep --strict "$app"

osascript -e 'tell application "Seeker" to quit' 2>/dev/null || true
dest="$HOME/Applications/Seeker.app"
mkdir -p "$HOME/Applications"
rm -rf "$dest"
ditto "$app" "$dest"
echo "Installed $dest"
