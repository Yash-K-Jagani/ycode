#!/bin/sh
# ycode installer (Linux/macOS). Usage:
#   curl -fsSL https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.sh | bash
set -e
REPO="Yash-K-Jagani/ycode"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "unsupported arch: $ARCH"; exit 1 ;;
esac
case "$OS" in
  linux) GOS="Linux" ;;
  darwin) GOS="Darwin" ;;
  *) echo "unsupported os: $OS (use install.ps1 on Windows)"; exit 1 ;;
esac
TAG="${YCODE_VERSION:-latest}"
if [ "$TAG" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download/ycode_${GOS}_${ARCH}.tar.gz"
else
  URL="https://github.com/$REPO/releases/download/$TAG/ycode_${GOS}_${ARCH}.tar.gz"
fi
echo "downloading $URL"
mkdir -p "$BIN_DIR"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$URL" -o "$tmp/ycode.tar.gz"
tar -xzf "$tmp/ycode.tar.gz" -C "$tmp"
install -m 755 "$tmp/ycode" "$BIN_DIR/ycode"
echo "installed to $BIN_DIR/ycode (ensure it is on PATH)"
"$BIN_DIR/ycode" version
