#!/usr/bin/env bash
# Run the development app signed with the local "Telepath Local Signing" cert.
# mygo dev signs ad hoc when no Apple Development identity exists, and an ad hoc
# signature changes with every build, so macOS asks for the Keychain items
# again each time. This identity stays the same, so "Always Allow" sticks.
set -euo pipefail

cd "$(dirname "$0")"

exec go tool mygo dev -sign "Telepath Local Signing" "$@"
