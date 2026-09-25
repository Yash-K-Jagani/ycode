# Store

Curated index of installable skills and plugins. The index lives at
`store/index.yaml` in this repo (curated by PR) and is cached locally:

```sh
ycode store update          # fetch curated index (~/.ycode/store.yaml)
ycode store list            # all entries
ycode store search review   # filter
ycode store install reviewer
ycode store verify          # check installed packages vs records
ycode store remove hello
```

Same in the TUI: `/store list|search|install|remove|verify|update`.

Index entries:

```yaml
- name: reviewer
  kind: skill               # skill|plugin
  source: https://github.com/Yash-K-Jagani/ycode.git
  subdir: store/examples/reviewer
  ref: main                 # git branch/tag, optional
  description: Strict code reviewer skill.
```

- Sources: github.com/gitlab.com git URLs (cloned shallow, `--branch ref`
  when set) or local dirs; `..` subdirs rejected.
- Install reuses the normal managers, so skills land in `~/.ycode/skills/`
  and plugins in `~/.ycode/plugins/` (plugin tools auto-register).
- Integrity: index entries may pin `sha256:` (tree hash); mismatches abort
  the install and clean up. Every install writes `.store.json` provenance;
  `verify` recomputes hashes (`ok`, `CHANGED`, or `no record`).
  Hashing canonicalizes line endings (CRLF/CR → LF) so identical checkouts
  verify on every OS regardless of git autocrlf settings.
- Trust: entries are installed on your explicit request only; sources are
  shown before fetching. Signatures are a future step — review
  a source before installing it.
