# ycode

**ycode** is a terminal-first AI coding harness written in Go — a fast, private,
single-binary assistant that lives in your terminal. It talks to **local Ollama
models** (auto-detected, works offline) plus **free-tier cloud APIs** (Gemini,
OpenRouter, Groq), remembers every session, and can read, write, test, review,
and refactor code through a real agent tool loop.

- **Single static binary** — no Node, no Python, no database server.
- **Private by design** — env-only API keys, encrypted redacted audit logs,
  secret-blocking commits, and a Zero-Data-Leak mode for local-only operation.
- **Hackable** — skills, script plugins, MCP servers, YAML hooks, automations,
  a local HTTP API + Go SDK, and headless/CI modes.

> Status: v0.11.0. Milestones M1–M6 implemented (see `plan.md`).

---

## Table of contents

1. [Requirements](#1-requirements)
2. [Download & install](#2-download--install)
3. [Quickstart](#3-quickstart)
4. [The TUI](#4-the-tui)
5. [Modes](#5-modes)
6. [Slash commands](#6-slash-commands)
7. [CLI reference](#7-cli-reference)
8. [Providers & models](#8-providers--models)
9. [Agent tools](#9-agent-tools)
10. [RAG, cache & router intelligence](#10-rag-cache--router-intelligence)
11. [Skills, plugins, MCP, hooks, prompts](#11-skills-plugins-mcp-hooks-prompts)
12. [Security & privacy](#12-security--privacy)
13. [Configuration reference](#13-configuration-reference)
14. [Project structure](#14-project-structure)
15. [Data layout](#15-data-layout)
16. [Development](#16-development)
17. [Releases & CI](#17-releases--ci)
18. [Troubleshooting](#18-troubleshooting)
19. [License](#19-license)

---

## 1. Requirements

| Need | Details |
|---|---|
| OS | Linux, macOS, or Windows (amd64/arm64) |
| Go (build from source only) | Go 1.27+ |
| Ollama (for local models) | Running at `http://localhost:11434` — override with `OLLAMA_HOST`. Pull at least one chat model (`ollama pull qwen2.5-coder:3b`) and, for RAG/embeddings, `ollama pull nomic-embed-text` |
| Cloud APIs (optional) | Keys via env: `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `GH_TOKEN`/`GITHUB_TOKEN` |

---

## 2. Download & install

**Option A — install script (recommended):**

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.sh | bash
# Windows PowerShell
irm https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.ps1 | iex
```

Both scripts download the release archive **and its `checksums.txt`**, verify
the SHA256, and refuse to install on mismatch. Pin a version or change the
destination with env vars:

```sh
YCODE_VERSION=v0.11.0 BIN_DIR=/usr/local/bin ./scripts/install.sh
```

**Option B — go install (adds the `ycode` command):**

```sh
go install github.com/Yash-K-Jagani/ycode/cmd/ycode@latest
# ensure $(go env GOPATH)/bin is on PATH
```

**Option C — build from source:**

```sh
git clone https://github.com/Yash-K-Jagani/ycode.git
cd ycode
./scripts/build.sh              # injects version/commit/date from git
go build -o ycode ./cmd/ycode    # plain build, reports 0.0.0-dev
go install ./cmd/ycode           # install as the `ycode` command
```

Verify: `ycode version` → `ycode 0.11.0 (commit abc1234, built 2026-09-25…, linux/amd64)`.
`ycode --version` prints the same thing.

---

## 2b. Setup

The first time you run `ycode`, it runs a short setup wizard — pick a provider,
supply a key if the provider is cloud-hosted, then choose a model. Run it again
any time with `ycode setup`.

API keys are read with the terminal in raw mode, so they are not echoed to the
screen or captured in scrollback, and are stored in your **OS keyring** rather
than in `config.yaml`.

Non-interactive contexts (CI, pipes, editor task runners) skip the wizard and
print the exact commands to run instead, so nothing ever blocks on a prompt:

```
$ ycode            # in a pipeline
ycode is not configured yet. Non-interactive session detected.
Fix it with one of:
  ollama pull qwen2.5-coder:7b        # then: ycode config set model <name>
  set GEMINI_API_KEY=<your key>       # or: ycode config set GEMINI_API_KEY <key>
  ycode setup                         # interactive wizard
```

If Ollama is running but has no models, the wizard offers to pull a
recommended coding model for you. If it cannot reach Ollama at all, it tells
you to run `ollama serve` and retries the probe.

---

## 3. Quickstart

```sh
ycode                 # open the TUI in the current project
```

1. Type `/connect` → pick **ollama** → accept the default host → pick an installed model.
2. Hit `Tab` (or type `/build`) to enter **build mode**.
3. Ask: `read go.mod and tell me the module name` — the model calls tools itself.
4. `/models` lists everything; `/help` lists all commands; `Ctrl+D` quits.

No local models yet? `ollama pull qwen2.5-coder:3b`, then `/models 1`.
No Ollama at all? `/connect` → gemini/openrouter/groq → paste a key → pick a model.

---

## 4. The TUI

- **Chat pane** (markdown, code blocks on tinted panels, command chips), **input box** with a mode chip (`▸ build · builder`), keystroke command palette, status bar.
- **Right sidebar** (`Ctrl+B`): folder, model, session tokens in/out, context meter, session + daily spend, live task
list. Big tasks are auto-broken into `todo` steps shown here as they complete.
- Type `/` for the **command palette**: filters as you type, `↑↓` to move, `Tab`/`Enter` to complete, `Enter` again to run, `Esc` to dismiss.
- `Ctrl+O` cycles installed Ollama models; `Ctrl+N` new session; `Tab`/`Shift+Tab` cycle modes; `Ctrl+C` cancels; `Ctrl+D` quits. Full list: `docs/shortcuts.md`.
- Every turn streams token-by-token; tool calls show as `🔧` lines; turn footers show token/cost/RAG notes.
- Codebase-aware: every build/plan turn sees the repo tree, README head, `AGENTS.md` rules, and git branch/status — plus `edit` tolerates `12: ` line prefixes and suggests close matches on miss.

---

## 5. Modes

| Mode | Tools | Purpose |
|---|---|---|
| `build` | all (read/write/edit/bash/git/…) | Implement, edit, run tests |
| `plan` | read-only subset | Clarifying questions when vague, then numbered plan ending in `AWAITING APPROVAL` |
| `chat` | none | Plain conversation (file tasks nudge you to `/build`) |
| `thinking` | none | Visible `<scratchpad>` reasoning, then the answer |

Switch with `Tab` or `/plan` `/build` `/chat` `/thinking`. The agent flavor comes from `/agent`
(`builder`/`planner`/`reviewer`). Flow: plan in plan mode, switch to build, say **build it** to implement.

---

## 6. Slash commands

| Command | What it does |
|---|---|
| `/help` | Command list |
| `/exit` | Quit (state saved) |
| `/new` | Fresh session |
| `/models` | Numbered model list; `/models <n>`, `/models <provider> <model>`, fuzzy names, `/models install <ollama-model>` (`ollama pull`) |
| `/model` | Alias of `/models` |
| `/variants` | Installed Ollama variants + VRAM/quant guidance |
| `/sessions` | List (auto-titled); resume, `fork <id>`, `search <q>`, `prune [N] [--yes]` |
| `/export [file]` | Save transcript as markdown |
| `/status` | Mode, tokens, cost today, cache hits, RAG index, latency |
| `/connect` | Interactive window: provider → API key/host → model picker |
| `/doctor` | Health check: tools, Ollama, RAG, cache, model advice |
| `/agent [name]` | Pick builder/planner/reviewer |
| `/init` | Scaffold `.ycode/` + `AGENTS.md` in the project |
| `/editor [path]` | Open `$EDITOR` without leaving the TUI |
| `/plan` `/build` `/chat` `/thinking` | Switch mode |
| `/review [path]` | AI review of `git diff` (summary → file:line findings → fixes) |
| `/test [path] [filter]` | Run project tests (Go/Rust/Node/Deno/Bun/Java/C#/PHP/Ruby/Python); `--watch` re-runs on save |
| `/refactor <goal>` | Checkpoint branch + armed instruction with revert directions |
| `/rag [ingest [path]│<query>]` | Local RAG index / search |
| `/mcps [list│add│remove│tools]` | MCP servers (`mcp__<server>__<tool>` tools in build) |
| `/skills [list│install│run│export│import]` | Skills (git URL, `owner/repo`, or local dir; zip share) |
| `/store [list│search│install│update]` | Curated skill/plugin store |
| `/hooks` | Show configured hook commands |
| `/prompts [list│show│run│save│versions]` | Versioned prompt library (4 builtins, `{{input}}` templates) |
| `/plugins [install│reload]` | Script plugins (git URL, `owner/repo`, or local dir) |

---

## 7. CLI reference

```
ycode                    # TUI in current directory (runs setup on first use)
ycode setup              # re-run the provider/key/model wizard
ycode config             # print current config (secrets shown as set/unset)
ycode config get <key>   # print one value
ycode config set <key> <value>   # write one value; keys go to the OS keyring
ycode status             # provider health, models, cost today
ycode version            # version + commit + date + os/arch
ycode doctor [--fix]     # environment self-check
ycode run "task" [--mode build|plan|chat] [--agent builder]
ycode serve [--addr 127.0.0.1:8471]   # local HTTP API (api/openapi.yaml)
ycode batch add|list|run|clear        # offline job queue
ycode store list|search|install|remove|verify|update # curated skill/plugin store
ycode ci [--post]                   # diff review + tests, --post comments on the PR
ycode daemon                          # interval automations → queue → run
ycode audit [--date YYYY-MM-DD|list]  # decrypted local audit log
```

Config keys: `active_provider`, `active_model`, `ollama_host`, `theme`,
`zero_data_leak`, `gemini_api_key`, `openrouter_api_key`, `groq_api_key`.
The three `*_api_key` values are written to the **OS keyring**, not to
`config.yaml`, so `ycode config` output is safe to paste into an issue.

API: `GET /healthz`, `GET /v1/models`, `GET /v1/status`, `POST /v1/chat`
`{prompt, mode?, agent?, workdir?}`. Go SDK: `pkg/ycodeclient`
(`New(base).Chat/Models/Status`). Outbound webhooks (`turn_complete`,
`turn_error`, `session_start`) via `~/.ycode/webhooks.yaml`.

---

## 8. Providers & models

One OpenAI-compatible streaming path + native Ollama (`/api/chat` NDJSON,
`/api/tags`, `/api/embed`). Keys are read from the environment only — never
written to disk; `/connect` activates a pasted key for the session and prints
the `export` line to persist it yourself.

On primary failure, configured cloud providers are retried as fallbacks
(gemini → openrouter → groq defaults); per-provider latency lives in
`~/.ycode/router.json` and shows in `/status`. Details: `docs/providers.md`.

---

## 9. Agent tools

Build mode tools (plan gets the read-only subset): `read` (multi-path) `write` `create` `add` `edit` `remove` `changes`
`grep` `glob` `bash` (denylist, 60s, 32KB cap) `git` (secret-scanning commit
gate) `github` (clone/PRs/issues, `owner/repo` shorthand) `browser`
`testgen` (Go/Rust/Node/Deno/Bun/Java/C#/PHP/Ruby/Python, name filter + extra args) `security` `tree`
`todo` `memory` `patch` `run` (execute code: python/js/ts/go/bash/powershell/ruby/php/java/rust) `delete` (guarded) `summary` `db` (mongo/postgres/mysql) `notebook` `api` (REST) `vscode` `scaffold` (react/express/fastapi) `models` (gguf) — plus dynamic `mcp__*` and `plugin__*` tools.
Chat code blocks get Chroma syntax highlighting with line numbers; write/edit results show red/green diffs. File reads/writes show path cards; shell commands their own tint.

Models emit `<tool:name>{json}</tool:name>` (tolerant parser, schema-error
retries, repeat-guard with cached results, 8-round cap). Small local models
work; 3b+ coders follow instructions far better than 1–2b ones.

---

## 10. RAG, cache & router intelligence

- **Local RAG**: `/rag ingest [path]` chunks the repo (40-line windows) and
  embeds via Ollama (`nomic-embed-text`); top-4 chunks auto-inject into
  build/plan turns; `/rag <query>` searches manually. JSON index per project.
- **Semantic cache**: exact + 0.985-cosine hits per provider+model, 7-day TTL,
  chat/thinking only (never tool turns). Hits reply instantly with `⚡`.
- **Router**: latency stats + cloud fallbacks (both single-shot and agent-loop
  turns, clean stream reset on retry).

---

## 11. Skills, plugins, MCP, hooks, prompts

- **Skills** (`~/.ycode/skills/<name>/SKILL.md`): install from git/`owner/repo`/dir,
  `run` injects into the next turn, `export`/`import` zip-shares. Docs: `docs/skills.md`.
- **Plugins** (`~/.ycode/plugins/<name>/plugin.json` + command): run as tools,
  stdin `$YCODE_PLUGIN_ARGS`, hot-reload watcher + `/plugins reload`. Docs: `docs/plugins.md`.
- **MCP**: stdio JSON-RPC (`initialize`, `tools/list`, `tools/call`), config in
  `~/.ycode/mcp.json`, `/mcps add <name> <cmd> [args]`.
- **Hooks** (`~/.ycode/hooks.yaml`, project `.ycode/hooks.yaml`):
  `on_request/on_response/pre_tool/post_tool/on_error` shell commands with
  `$YCODE_*` env; failing `pre_tool` blocks the call.
- **Prompts**: builtins `review/explain/commit/testplan`, user saves with
  timestamped snapshots, `/prompts run` arms a template.

---

## 12. Security & privacy

- Env-only keys; NaCl-encrypted (`keyring.key`, 0600), secret-redacted,
  append-only audit logs (`ycode audit` to read).
- `git commit` via the agent is blocked on secret patterns (override `force`).
- Tool results/URLs are injection-scanned with an untrusted-data note.
- **`zero_data_leak: true`** (`~/.ycode/config.yaml`): cloud providers and
  fallbacks hard-blocked, `browser`/`github` refused, red `[LOCAL ONLY]` badge.
- Full model: `docs/security.md`.

---

## 13. Configuration reference

`~/.ycode/config.yaml` (user) + `<project>/.ycode/config.yaml` (overrides;
see `configs/ycode.yaml` for defaults):

```yaml
ollama_host: http://localhost:11434
theme: dark
active_provider: ollama
active_model: ""
zero_data_leak: false
gemini_key_env: GEMINI_API_KEY
openrouter_key_env: OPENROUTER_API_KEY
groq_key_env: GROQ_API_KEY
```

Other files: `mcp.json`, `hooks.yaml`, `webhooks.yaml`,
`automations.yaml` (`automations: [{name, prompt, mode, interval_minutes}]`),
`prompts/`, `skills/`, `plugins/` — all under `~/.ycode/`.

---

## 14. Project structure

```
cmd/ycode/main.go            # CLI: TUI + run/serve/batch/ci/daemon/audit/version
internal/tui/                # Bubble Tea UI: app, palette, connect modal, render, slash
internal/modes/              # plan/build/chat/thinking prompts + tool allow-lists
internal/providers/          # ollama, gemini, openrouter, groq, openaicompat, registry
internal/router/             # selection, latency stats, cloud fallbacks
internal/agent/              # <tool:> loop: parse, exec, repeat-guard, injection notes
internal/tools/              # 15 tools (read..patch) + registries
internal/{agents,sessions,context,cache,cost,embed,rag}   # smarts & state
internal/{mcp,hooks,plugins,skills,prompts}               # integrations
internal/{audit,security}    # encrypted logs, scanners, redaction
internal/{headless,batch,automation,serve,webhooks}       # ecosystem runtime
# (sessions, cost, latency, batch, memory live in SQLite; RAG/cache/vectors stay files)
pkg/{apitypes,ycodeclient}/  # shared types + Go SDK
api/openapi.yaml  configs/  docs/  scripts/  .github/workflows/
```

---

## 15. Data layout

```
~/.ycode/
  config.yaml  keyring.key  ycode.db (sessions, cost, latency, batch, memory)
  cost.json  router.json  batch.json  memory.json  (legacy, auto-imported once)
  cache.json  mcp.json  hooks.yaml  webhooks.yaml store.yaml
  automations.yaml  audit/  sessions/ (legacy)  rag/  skills/  plugins/  prompts/
<project>/.ycode/  config.yaml  hooks.yaml  automations.yaml  todos.json
```

---

## 16. Development

```sh
go build ./...   # build
go vet ./...     # vet
go test ./...    # full suite (~16 packages; RAG live test needs nomic-embed-text)
golangci-lint run ./...   # lint (v2 config in .golangci.yml)
```

Conventions: small focused packages, table-less unit tests per package,
`gofmt` clean, no CGO (pure-Go SQLite path), Windows-first paths verified.

---

## 17. Releases & CI

- `.goreleaser.yaml`: `ycode_<os>_<arch>.tar.gz` for linux/windows/darwin ×
  amd64/arm64. Tag to release: `git tag v0.11.0 && git push origin v0.11.0`.
- `.github/workflows/ci.yml`: build matrix (Go × OS) + `go vet`/`go test` +
  golangci-lint + `ycode ci` review on PRs.

---

## 18. Troubleshooting

| Symptom | Fix |
|---|---|
| `/models` empty / ollama errors | Start Ollama; `ollama pull qwen2.5-coder:3b`; check `OLLAMA_HOST` |
| Model asks for paths | Use `/build` (chat has no tools); `/rag ingest` helps big repos |
| Raw `<tool:>` tags in answers / round-limit notes | Rebuild (`go install ./cmd/ycode`); try a 3b+ coder model |
| Plan mode calls nothing | Rebuild — old binaries predate plan tool docs |
| Cloud 4xx/rate limits | Check key env, `/status` latency/failures; fallbacks engage automatically |
| `golangci-lint-action` fails | Use golangci-lint v2 config (`version: "2"`, `formatters:` for gofmt) |
| `ycode` resolves to another program | `where ycode`; ensure `go/bin` wins or uninstall the clash |

Run `/doctor` inside the TUI for a guided health check.

---

## 19. License

MIT — see `LICENSE`. Plan/architecture source of truth: `plan.md`.
