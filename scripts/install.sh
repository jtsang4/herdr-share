#!/bin/sh
# Installs the herdr-share CLI from GitHub releases and the Herdr mod.
#
#   curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh | sh
#
# Environment:
#   HERDR_SHARE_VERSION  a release tag (default: the latest release)
#   HERDR_SHARE_BIN_DIR  where to put the binary (default: ~/.local/bin)
#   HERDR_SHARE_NO_MOD=1 do not install the Herdr mod
#   HERDR_SHARE_NO_BIND=1 install the mod without binding a key (keys.json untouched)
#   HERDR_SHARE_RELAY    set this relay URL after installing
set -eu

REPO=jtsang4/herdr-share
BIN_DIR=${HERDR_SHARE_BIN_DIR:-"$HOME/.local/bin"}

say() { printf '%s\n' "$*"; }
die() { printf 'herdr-share install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need curl
need tar
need uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $os in
darwin | linux) ;;
*) die "unsupported OS $os (on Windows, download the zip from https://github.com/$REPO/releases)" ;;
esac
arch=$(uname -m)
case $arch in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture $arch" ;;
esac

tag=${HERDR_SHARE_VERSION:-}
if [ -z "$tag" ]; then
	tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$tag" ] || die "could not find the latest release"
fi
version=${tag#v}
archive="herdr-share_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
say "Downloading herdr-share $tag ($os/$arch)…"
curl -fsSL -o "$tmp/$archive" "$base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"

want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || die "no checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
	got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" herdr-share
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/herdr-share" "$BIN_DIR/herdr-share"
say "Installed $BIN_DIR/herdr-share"
case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) say "Note: $BIN_DIR is not on your PATH." ;;
esac

if [ "${HERDR_SHARE_NO_MOD:-}" != 1 ]; then
	if [ "${HERDR_SHARE_NO_BIND:-}" = 1 ]; then
		"$BIN_DIR/herdr-share" mod install --no-bind
	else
		"$BIN_DIR/herdr-share" mod install
	fi
fi
if [ -n "${HERDR_SHARE_RELAY:-}" ]; then
	"$BIN_DIR/herdr-share" config set relay "$HERDR_SHARE_RELAY"
fi
say "Done. Check your setup with: herdr-share doctor"
