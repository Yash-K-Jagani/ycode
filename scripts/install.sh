#!/bin/sh
# ycode installer (Linux/macOS). Usage:
#   curl -fsSL https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.sh | bash
#
# Env overrides:
#   YCODE_VERSION=v0.11.0   pin a specific release tag (default: latest)
#   BIN_DIR=/usr/local/bin  install location (default: $HOME/.local/bin)
#   YCODE_SKIP_CHECKSUM=1   skip SHA256 verification (not recommended)
set -e

REPO="Yash-K-Jagani/ycode"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
TAG="${YCODE_VERSION:-latest}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) echo "error: unsupported arch: $ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux | darwin) ;;
  *) echo "error: unsupported os: $OS (use install.ps1 on Windows)" >&2; exit 1 ;;
esac

if [ "$TAG" = "latest" ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$TAG"
fi

ASSET="ycode_${OS}_${ARCH}.tar.gz"
URL="$BASE/$ASSET"
SUM_URL="$BASE/checksums.txt"

command -v curl >/dev/null 2>&1 || {
  echo "error: curl is required" >&2
  exit 1
}

echo "ycode installer"
echo "  os/arch : $OS/$ARCH"
echo "  version : $TAG"
echo "  dest    : $BIN_DIR/ycode"
echo

mkdir -p "$BIN_DIR"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "downloading $URL"
curl -fsSL "$URL" -o "$tmp/ycode.tar.gz"

# Verify SHA256 against the release checksums before executing anything.
if [ "${YCODE_SKIP_CHECKSUM:-}" = "1" ]; then
  echo "warning: checksum verification skipped (YCODE_SKIP_CHECKSUM=1)" >&2
elif curl -fsSL "$SUM_URL" -o "$tmp/checksums.txt"; then
  EXPECTED=$(awk -v a="$ASSET" '$2 == a || $2 == "*"a {print $1; exit}' "$tmp/checksums.txt")
  if [ -z "$EXPECTED" ]; then
    echo "error: no checksum published for $ASSET - refusing to install" >&2
    exit 1
  fi
  ACTUAL=$(sha256sum "$tmp/ycode.tar.gz" | awk '{print $1}')
  if [ "$EXPECTED" != "$ACTUAL" ]; then
    echo "error: checksum mismatch for $ASSET" >&2
    echo "  expected $EXPECTED" >&2
    echo "  actual   $ACTUAL" >&2
    echo "refusing to install. The download may be corrupted or tampered with." >&2
    exit 1
  fi
  echo "checksum verified"
else
  echo "error: could not download checksums.txt - refusing to install" >&2
  echo "  (set YCODE_SKIP_CHECKSUM=1 to bypass at your own risk)" >&2
  exit 1
fi

tar -xzf "$tmp/ycode.tar.gz" -C "$tmp"
install -m 755 "$tmp/ycode" "$BIN_DIR/ycode"

echo
echo "installed to $BIN_DIR/ycode"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "note: $BIN_DIR is not on your PATH - add it to use 'ycode' directly" ;;
esac
"$BIN_DIR/ycode" version
