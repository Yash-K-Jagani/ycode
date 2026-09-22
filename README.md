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

> Status: v0.10.0. Milestones M1–M6 implemented (see `plan.md`).

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

**Option A — install script (once a GitHub release exists):**

```sh
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.sh | bash
# Windows PowerShell
irm https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.ps1 | iex
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
go build -o ycode ./cmd/ycode     # ./ycode.exe on Windows
go install ./cmd/ycode            # install as the `ycode` command
```

Verify: `ycode version` → `ycode 0.10.0`.

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

---

## 5. Modes

| Mode | Tools | Purpose |
|---|---|---|
| `build` | all (read/write/edit/bash/git/…) | Implement, edit, run tests |
| `plan` | read-only subset | Research + numbered plan ending in `AWAITING APPROVAL` |
| `chat` | none | Plain conversation (file tasks nudge you to `/build`) |
| `thinking` | none | Visible `<scratchpad>` reasoning, then the answer |

Switch with `Tab` or `/plan` `/build` `/chat` `/thinking`. The agent flavor comes from `/agent` (`builder`/`planner`/`reviewer`).

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
ycode                    # TUI in current directory
ycode status             # provider health, models, cost today
ycode version
ycode run "task" [--mode build|plan|chat] [--agent builder]
ycode serve [--addr 127.0.0.1:8471]   # local HTTP API (api/openapi.yaml)
ycode batch add|list|run|clear        # offline job queue
ycode store list|search|install|remove|verify|update # curated skill/plugin store
ycode ci [--post]                   # diff review + tests, --post comments on the PR
ycode daemon                          # interval automations → queue → run
ycode audit [--date YYYY-MM-DD|list]  # decrypted local audit log
```

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

Build mode tools (plan gets the read-only subset): `read` `write` `edit`
`grep` `glob` `bash` (denylist, 60s, 32KB cap) `git` (secret-scanning commit
gate) `github` (clone/PRs/issues, `owner/repo` shorthand) `browser`
`testgen` (Go/Rust/Node/Deno/Bun/Java/C#/PHP/Ruby/Python, name filter + extra args) `security` `tree`
`todo` `memory` `patch` `run` (execute code: python/js/ts/go/bash/powershell/ruby/php/java/rust) `db` (mongo/postgres/mysql) `notebook` `api` (REST) `vscode` `scaffold` (react/express/fastapi) — plus dynamic `mcp__*` and `plugin__*` tools.
Chat code blocks get Chroma syntax highlighting; prompts carry the detected
project language.

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
  amd64/arm64. Tag to release: `git tag v0.10.0 && git push origin v0.10.0`.
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
