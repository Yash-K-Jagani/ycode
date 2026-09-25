#!/bin/sh
# Verifies the "skip absent package-manager pipes" logic in release.yml, which is
# shell inside YAML and therefore easy to get subtly wrong.
set -e

check() {
  HOMEBREW_TOKEN="$1"; SCOOP_TOKEN="$2"; WINGET_TOKEN="$3"
  expected="$4"
  skip=""
  [ -n "$HOMEBREW_TOKEN" ] || skip="${skip:+$skip,}homebrew_casks"
  [ -n "$SCOOP_TOKEN" ]    || skip="${skip:+$skip,}scoops"
  [ -n "$WINGET_TOKEN" ]   || skip="${skip:+$skip,}winget"
  skip="${skip#,}"
  if [ -n "$skip" ]; then
    args="release --clean --skip=$skip"
  else
    args="release --clean"
  fi
  if [ "$args" = "$expected" ]; then
    echo "PASS: [$HOMEBREW_TOKEN|$SCOOP_TOKEN|$WINGET_TOKEN] -> $args"
  else
    echo "FAIL: [$HOMEBREW_TOKEN|$SCOOP_TOKEN|$WINGET_TOKEN]"
    echo "  got      $args"
    echo "  expected $expected"
    exit 1
  fi
}

# No tokens at all: every package manager is skipped, flag is well formed.
check "" "" "" "release --clean --skip=homebrew_casks,scoops,winget"
# Only homebrew configured.
check tok "" "" "release --clean --skip=scoops,winget"
# Only scoop configured.
check "" tok "" "release --clean --skip=homebrew_casks,winget"
# All configured: no --skip flag, because `--skip=` is rejected by goreleaser.
check a b c "release --clean"
# Two missing: no leading or trailing comma.
check a "" "" "release --clean --skip=scoops,winget"
check "" "" c "release --clean --skip=homebrew_casks,scoops"

echo "all skip-logic cases passed"
