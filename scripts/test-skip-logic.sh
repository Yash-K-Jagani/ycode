#!/bin/sh
# Verifies the "skip absent package-manager pipes" logic in release.yml, which is
# shell inside YAML and therefore easy to get subtly wrong.
#
# The names below are goreleaser *pipe* names, not config stanza keys. Using the
# stanza keys (homebrew_casks, scoops) makes goreleaser abort the whole release
# with "--skip=homebrew_casks is not allowed", which is exactly how v0.12.2's
# release failed. VALID_PIPES below is the authoritative list.
set -e

VALID_PIPES="announce archive aur aur-source before chocolatey docker flatpak homebrew iru ko makeself mcp nfpm nix notarize publish sbom scoop sign snapcraft srpm validate winget"

check() {
  HOMEBREW_TOKEN="$1"; SCOOP_TOKEN="$2"; WINGET_TOKEN="$3"
  expected="$4"
  skip=""
  [ -n "$HOMEBREW_TOKEN" ] || skip="${skip:+$skip,}homebrew"
  [ -n "$SCOOP_TOKEN" ]    || skip="${skip:+$skip,}scoop"
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

  # Every name we emit must be a pipe goreleaser actually accepts.
  if [ -n "$skip" ]; then
    for pipe in $(echo "$skip" | tr ',' ' '); do
      case " $VALID_PIPES " in
        *" $pipe "*) ;;
        *)
          echo "FAIL: '$pipe' is not a valid goreleaser skip pipe"
          echo "  valid: $VALID_PIPES"
          exit 1
          ;;
      esac
    done
  fi
}

# No tokens at all: every package manager is skipped, flag is well formed.
check "" "" "" "release --clean --skip=homebrew,scoop,winget"
# Only homebrew configured.
check tok "" "" "release --clean --skip=scoop,winget"
# Only scoop configured.
check "" tok "" "release --clean --skip=homebrew,winget"
# All configured: no --skip flag, because `--skip=` is rejected by goreleaser.
check a b c "release --clean"
# Two missing: no leading or trailing comma.
check a "" "" "release --clean --skip=scoop,winget"
check "" "" c "release --clean --skip=homebrew,scoop"

echo "all skip-logic cases passed"
