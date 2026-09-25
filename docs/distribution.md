# Distribution setup

The archives, `checksums.txt`, and the Sigstore signature publish on every tag
with no configuration. The three package-manager manifests need one-time
setup, because each targets a repository or registry that must exist first.

Until a token is configured, the release workflow **skips only the affected
pipe** — a missing tap never fails a release.

## Status

| Channel | Install command | State |
|---|---|---|
| Install script | `curl … install.sh \| bash` | Working |
| `go install` | `go install github.com/Yash-K-Jagani/ycode/cmd/ycode@latest` | Working |
| Self-upgrade | `ycode upgrade --apply` | Working |
| Homebrew | `brew install --cask ycode` | Awaiting tap + token |
| Scoop | `scoop bucket add ycode … && scoop install ycode` | Awaiting bucket + token |
| winget | `winget install Yash-K-Jagani.ycode` | Blocked on publisher registration |

## 1. Homebrew

Create an empty tap, then a PAT with **no scopes** (Homebrew only needs repo
write), and store it:

```sh
# on GitHub: New repository -> name "homebrew-ycode", public, add a README
brew tap-new Yash-K-Jagani/ycode
```

```sh
# in ycode/.github/workflows/release.yml, add HOMEBREW_TOKEN as an Actions secret
```

Users then run:

```sh
brew tap Yash-K-Jagani/ycode
brew install --cask ycode
```

The cask includes a `post` hook that strips the macOS quarantine attribute.
The binaries are not codesigned or notarized, so without it Gatekeeper blocks
every run. If you later sign and notarize the macOS build, delete that hook.

## 2. Scoop

Create an empty bucket repository. Manifests are written to the **repository
root** — `scoop bucket list` reports zero manifests if they are nested in a
subdirectory, so `directory` is intentionally empty.

The bucket cannot be named `ycode`, because that name is already taken by the
main repo on the same owner. The config targets `ycode-scoop`:

```sh
# on GitHub: New repository -> name "ycode-scoop", public, add a README
```

Then create a PAT with **no scopes** (Scoop manifests only need repo write),
and set `SCOOP_TOKEN` in Actions secrets. The stanza already points at
`Yash-K-Jagani/ycode-scoop`.

Users then run:

```sh
scoop bucket add ycode https://github.com/Yash-K-Jagani/ycode-scoop
scoop install ycode
```

## 3. winget — has an extra prerequisite

winget does **not** accept manifests for an unregistered publisher. The
`Yash-K-Jagani` publisher must be added to
[`microsoft/winget-pkgs`](https://github.com/microsoft/winget-pkgs) first:

```sh
wingetcreate newaccount
```

That command authenticates with Azure DevOps and submits a publisher
registration tied to your GitHub identity. It is manual, takes a few days, and
cannot be automated. Until it is approved, the generated manifest PR will be
rejected by the winget-pkgs bot.

No token is needed for winget; the release workflow's `GITHUB_TOKEN` opens the
PR. That is why the skip logic gates winget on `WINGET_TOKEN` — set it to any
non-empty placeholder once the publisher is registered, or drop winget from the
skip list.

Also worth knowing:

- winget-pkgs CI enforces hard limits: `short_description` ≤ 80 characters,
  `description` ≤ 101, package identifier ≤ 50, and at most 3 tags.
  `internal/distribution` mirrors the metadata in `.goreleaser.yaml` and
  asserts every limit, so a value that would be rejected fails a unit test
  instead of a pull request.
- Manifests land as **draft PRs**, so a version is not installable until the
  winget-pkgs maintainers merge it. Expect days, not seconds.
- winget does not run unsigned binaries, but it *is* the only channel that
  surfaces Microsoft's SmartScreen reputation to users. A download-count-zero
  release will often trigger a "not recognized" warning until users report it.

## Verifying a release

```sh
# 1. The signature binds the checksum to this repo's release workflow.
cosign verify-blob \
  --certificate-identity 'https://github.com/Yash-K-Jagani/ycode/.github/workflows/release.yml@refs/tags/v0.12.2' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --bundle checksums.txt.sigstore.json checksums.txt

# 2. The archive matches the signed checksum.
sha256sum --check --ignore-missing checksums.txt
```

The install scripts and `ycode upgrade` both perform step 2 automatically and
refuse to install on mismatch or if no checksum is published.
