#!/bin/sh
# Stages the ripgrep binary Find in files spawns, at resources/<os>-<arch>/rg,
# where `mygo build` copies it into the app's Resources. Official release
# archive, pinned, checked against its published sha256. The machine's own
# platform only: a build is for the machine it runs on.
set -eu

VERSION=15.2.0
cd "$(dirname "$0")/.."

case "$(uname -s)-$(uname -m)" in
	Darwin-arm64) target=aarch64-apple-darwin; dir=darwin-arm64 ;;
	Darwin-x86_64) target=x86_64-apple-darwin; dir=darwin-amd64 ;;
	Linux-x86_64) target=x86_64-unknown-linux-musl; dir=linux-amd64 ;;
	Linux-aarch64) target=aarch64-unknown-linux-gnu; dir=linux-arm64 ;;
	*) echo "fetch-rg: no ripgrep build for $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac

out=resources/$dir
if [ -x "$out/rg" ] && [ "$(cat "$out/VERSION" 2>/dev/null)" = "$VERSION" ]; then
	exit 0
fi

base=ripgrep-$VERSION-$target
url=https://github.com/BurntSushi/ripgrep/releases/download/$VERSION/$base.tar.gz
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "fetch-rg: downloading $url"
curl -fsSL "$url" -o "$work/rg.tar.gz"
curl -fsSL "$url.sha256" -o "$work/rg.sha256"
expected=$(cut -d' ' -f1 "$work/rg.sha256")
actual=$(shasum -a 256 "$work/rg.tar.gz" | cut -d' ' -f1)
if [ "$expected" != "$actual" ]; then
	echo "fetch-rg: sha256 mismatch (expected $expected, got $actual)" >&2
	exit 1
fi

tar -xzf "$work/rg.tar.gz" -C "$work" "$base/rg"
mkdir -p "$out"
mv "$work/$base/rg" "$out/rg"
chmod +x "$out/rg"
echo "$VERSION" > "$out/VERSION"
