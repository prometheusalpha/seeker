#!/bin/sh
# Rebuilds sql-formatter.js: sql-formatter at the version recall pins in
# node_modules, bundled with its grammar for every dialect into one script
# goja can run. Needs bun and network.
set -eu
VERSION=15.9.0
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$here/entry.js" "$tmp/entry.js"
cd "$tmp"
echo '{"private":true}' > package.json
bun add --exact "sql-formatter@$VERSION"
# iife: goja has no module loader. Whether it runs the syntax the library
# ships is proved by the tests, not assumed.
bun build entry.js --target=browser --format=iife --minify --outfile="$here/sql-formatter.js"
