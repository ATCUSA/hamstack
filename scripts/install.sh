#!/bin/sh
# Installs hamstack from a GitHub release into ~/.local/bin. No root, nothing else is changed.
#
#   curl -fsSL https://raw.githubusercontent.com/ATCUSA/hamstack/main/scripts/install.sh | sh
#
# Environment: HAMSTACK_VERSION=v1.2.3 installs that release instead of the latest;
#              HAMSTACK_DEST=DIR installs there instead of ~/.local/bin.
# The download is checked against the release's SHA256SUMS file before it is installed.
set -eu

REPO="ATCUSA/hamstack"
DEST="${HAMSTACK_DEST:-$HOME/.local/bin}"

die() { echo "hamstack install: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "hamstack runs on Linux (it manages systemd user services); this is $(uname -s)"
case "$(uname -m)" in
  x86_64|amd64)  asset=hamstack-linux-amd64 ;;
  aarch64|arm64) asset=hamstack-linux-arm64 ;;
  armv7l|armv7)  asset=hamstack-linux-armv7 ;;
  *) die "no build for $(uname -m): build from source (see the README)" ;;
esac

if [ -n "${HAMSTACK_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$HAMSTACK_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q --https-only -O "$2" "$1"; }
else
  die "need curl or wget"
fi
command -v sha256sum >/dev/null 2>&1 || die "need sha256sum (coreutils)"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "downloading $asset from $base"
fetch "$base/$asset" "$tmp/$asset" || die "could not download $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download the checksums"

want=$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
[ "$want" = "$got" ] || die "checksum mismatch for $asset (wanted $want, got $got): not installing"

mkdir -p "$DEST"
install -m 0755 "$tmp/$asset" "$DEST/hamstack"
echo "installed $DEST/hamstack ($("$DEST/hamstack" version))"

case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "note: $DEST is not on your PATH; add it (for example in ~/.profile) and open a new shell." ;;
esac
echo "next: run  hamstack"
