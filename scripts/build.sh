#!/bin/sh
# Build a ycode binary with version metadata injected.
#
#   ./scripts/build.sh                 # version from git describe
#   VERSION=v0.12.0 ./scripts/build.sh # override
#   OUT=./dist ./scripts/build.sh     # override output dir
set -e

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
DATE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
OUT="${OUT:-./dist}"
BIN="${BIN:-ycode}"

mkdir -p "$OUT"

# Version lives in package main, so the linker symbol is main.Version.
# Using the full import path here silently injects nothing.
LDFLAGS="-s -w"
LDFLAGS="$LDFLAGS -X main.Version=$VERSION"
LDFLAGS="$LDFLAGS -X main.Commit=$COMMIT"
LDFLAGS="$LDFLAGS -X main.Date=$DATE"

echo "building $BIN $VERSION ($COMMIT) -> $OUT/$BIN"
# shellcheck disable=SC2086
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$BIN" ./cmd/ycode

"$OUT/$BIN" version
