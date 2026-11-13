#!/bin/sh
# Rewrites golden.json from sql-formatter run directly in bun, at the
# version the bundle is built from. Needs bun and network.
set -eu
VERSION=15.9.0
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$here/golden.mjs" "$tmp/golden.mjs"
cd "$tmp"
echo '{"private":true}' > package.json
bun add --exact "sql-formatter@$VERSION"
bun golden.mjs "$here/golden.json"
