# ycode — Plan & Architecture

> AI coding harness in Go for local Ollama models + free cloud APIs (Gemini, OpenRouter, Groq, and more).
> Fast, private, extensible. Runs great on modest hardware (target: RTX 2050, 16 GB RAM).

---

## 1. Vision

ycode is a terminal-first AI coding harness (like opencode) written in Go:

- **Single binary** — run `ycode` in any terminal to open the TUI.
- **Multi-provider** — local models (Ollama, auto-detected) plus free-tier cloud APIs.
- **Session continuity** — every session is persisted; resume any previous session.
- **Private by design** — encrypted local audit logs, Zero-Data-Leak mode, local-only RAG.
- **Hackable** — skills, plugins, middleware hooks, MCP servers, custom agents.

### Design constraints (for RTX 2050 / 16 GB RAM)

- Default local model recommendations are quantized GGUF (Q4_K_M) via Ollama.
- Lazy loading of heavy components (vector DB, scanners) — start sub-1s, grow on demand.
- Streaming-first UI — never buffer a whole response in memory.
- Context auto-management trims history aggressively to fit small context windows.

---

## 2. Tech Stack

| Layer | Choice | Why |
|---|---|---|
| Language | **Go 1.22+** | Single static binary, fast startup, great concurrency |
| TUI | **Bubble Tea + Bubbles + Lipgloss** | opencode-class terminal UX in Go |
| Syntax highlighting | **Chroma** | Go-native, multi-language |
| Diff viewer | **go-diff** | Patience diff for review/build modes |
| Markdown renderer | **Glamour** | Beautiful chat rendering in-terminal |
| Config | **Viper + YAML** | Multi-format config, env override, hot reload |
| Local model runtime | **Ollama (via its HTTP API)** | Auto-detect running models; recommend quantized ones |
| Cloud API access | **OpenAI-compatible client** | One client covers OpenRouter, Groq, Ollama, Gemini (via adapter), others |
| API fallback | **Custom router** | Dynamic API fallbacks with latency & cost scoring |
| Semantic cache | **SQLite + sqlite-vec** (embedded) | Zero-dependency vector similarity for caching & RAG |
| Vector DB | **SQLite + sqlite-vec (embedded)** | Local RAG sandbox without external services |
| Encryption (audit logs) | **age / NaCl (x/crypto)** | Encrypted local audit logs at rest |
| Git | **go-git** | Repo awareness, branch/session integration |
| GitHub | **go-github** | PR review, issue creation, repo download |
| Shell exec | **os/exec** (sandboxed wrapper) | Natural-language-to-shell with allowlists |
| MCP | **JSON-RPC client** | Model Context Protocol servers |
| Plugin system | **Go plugin + WASM (wazero)** | Hot-reload sandboxed plugins |
| Webhooks / HTTP | **net/http + chi** | Local API server & webhooks |
| Database (project state) | **SQLite (modernc.org/sqlite, pure Go)** | Cross-platform, zero CGO |
| Logging | **slog** | Structured logs |
| CLI framework | **cobra** | `ycode` root command + subcommands |
| Versioning / releases | **goreleaser** | Cross-platform builds (Linux/macOS/Windows) |
| CI | **GitHub Actions** | Test, lint, release on tag |
| Linting | **golangci-lint** | Clean codebase guarantee |

**Explicit non-choices:** no Node/Electron runtime, no external DB server, no Python dependencies in the core binary.

---

## 3. Codebase Structure

```
ycode/
├── cmd/
│   └── ycode/
│       └── main.go               # Entry point; wires DI, starts TUI
├── internal/
│   ├── tui/                      # Bubble Tea TUI
│   │   ├── app.go                # Root model, mode switching
│   │   ├── components/           # Chat pane, editor, plan view, diff viewer,
│   │   │                         # status bar, model picker, session picker
│   │   ├── keymap.go             # opencode-compatible keybindings
│   │   ├── slash.go              # "/" command registry & completion
│   │   └── theme/                # Theme manager + built-in themes
│   ├── modes/                    # Plan / Build / Chat / Thinking
│   │   ├── plan.go
│   │   ├── build.go
│   │   ├── chat.go
│   │   └── thinking.go           # Visible reasoning / scratchpad mode
│   ├── config/                   # Viper config load, validate, hot-reload
│   ├── providers/                # LLM provider abstraction
│   │   ├── provider.go           # Interface: Stream, Complete, Embed, ListModels
│   │   ├── ollama/               # Local model detection & auto-setup
│   │   ├── gemini/
│   │   ├── openrouter/
│   │   ├── groq/
│   │   └── registry/             # Model registry, 1-click install/versioning
│   ├── router/                   # Provider selection, latency optimizer,
│   │                             # dynamic API fallbacks, unified token counter
│   ├── tools/                    # Function/tool calling
│   │   ├── bash.go               # Sandboxed shell
│   │   ├── read.go, write.go, edit.go, grep.go, glob.go
│   │   ├── git.go, github.go
│   │   ├── browser.go            # Headless fetch/read of URLs
│   │   ├── testgen.go            # Auto test generation & execution
│   │   ├── security.go           # Vulnerability scanning
│   │   └── refactor.go           # Codebase-wide refactoring w/ safety nets
│   ├── agents/                   # Agent definitions (planner, builder, reviewer…)
│   ├── mcp/                      # MCP client (server connect, list, call)
│   ├── hooks/                    # Pre/post processing hooks, middleware chain
│   ├── plugins/                  # Plugin loader (Go + WASM), hot-reload
│   ├── skills/                   # Downloadable skills (skills list/install/run)
│   ├── sessions/                 # Session store (SQLite), resume, list, fork
│   ├── context/                  # Context window auto-management,
│   │                             # repo-aware injection, file tree indexing
│   ├── rag/                      # Local RAG sandbox: ingest, chunk, embed, query
│   ├── cache/                    # Semantic caching layer (sqlite-vec)
│   ├── cost/                     # Dynamic cost tracker (per provider/model)
│   ├── audit/                    # Encrypted local audit logs, privacy guardrails,
│   │                             # prompt injection & safety scanners
│   ├── security/                 # Zero-data-leak mode, secret redaction
│   ├── prompts/                  # Prompt template library + versioning
│   ├── lora/                     # Local model optimization & quantization mgr,
│   │                             # adapter/LoRA management
│   ├── automation/               # Scheduled & event-driven automations
│   ├── webhooks/                 # Outbound webhooks, client lib endpoints
│   ├── batch/                    # Offline batch queue
│   ├── debug/                    # Explainability & model debugging tools
│   └── store/                    # SQLite schema, migrations (pure Go driver)
├── pkg/                          # Public, reusable libraries
│   ├── ycodeclient/              # Cross-platform client library (Go SDK)
│   └── apitypes/                 # Shared request/response types
├── api/                          # Optional local HTTP API spec (OpenAPI yaml)
├── configs/
│   ├── ycode.yaml                # Default config (providers, hooks, guardrails)
│   └── themes/                   # Built-in theme files
├── docs/
│   ├── commands.md               # All "/" commands documented
│   ├── providers.md              # Adding a new provider/API
│   ├── plugins.md                # Plugin authoring
│   ├── skills.md                 # Skill format & publishing
│   └── shortcuts.md              # Full keybinding reference
├── scripts/
│   ├── install.sh
│   └── install.ps1
├── .github/workflows/            # CI: test, lint, release
├── .goreleaser.yaml
├── .golangci.yml
├── go.mod
├── README.md
├── LICENSE
└── plan.md                       # ← this file
```

---

## 4. Modes

| Mode | Behavior |
|---|---|
| **Plan** | Read-only agent: analyzes the repo, produces a step-by-step plan for approval. No writes until approved. |
| **Build** | Full agent loop: reads files, writes/edits code, runs tests, iterates. |
| **Chat** | Plain conversation with optional @file / @folder context attachments. |
| **Thinking** | Deep-reasoning mode: expanded scratchpad, chain-of-thought visible, better for hard debugging/architecture questions. |

Mode is switchable mid-session via keybinding or `/plan`, `/build`, `/chat`, `/thinking` (aliases allowed).

---

## 5. Slash Commands

All commands live in the `/` palette (inherited style from opencode):

`/agent` `/connect` `/debug` `/different` `/editor` `/exit` `/help` `/init` `/mcps` `/models` `/move` `/new` `/review` `/sessions` `/skills` `/status` `/theme` `/variants`

| Command | Purpose |
|---|---|
| `/agent` | Pick or configure an agent (planner, builder, reviewer, custom) |
| `/connect` | Add/verify a provider API key (Gemini, OpenRouter, Groq, Ollama, custom) |
| `/debug` | Model debugging: dump last request/response, token usage, tool traces |
| `/different` | Compare answers from multiple models/providers side-by-side |
| `/editor` | Open current file in $EDITOR |
| `/exit` | Quit ycode (state saved) |
| `/help` | Command & shortcut help |
| `/init` | Initialize ycode in a project (config, AGENTS.md, hooks) |
| `/mcps` | List/add/remove MCP servers |
| `/models` | Browse model registry; one-click install/swap with context migration |
| `/move` | Move the session working directory |
| `/new` | Start a fresh session |
| `/review` | AI code review of current diff/PR with summaries |
| `/sessions` | List past sessions; pick one to continue |
| `/skills` | Browse/download/run skills |
| `/status` | Provider health, cost today, latency, cache hit rate |
| `/theme` | Switch UI theme |
| `/variants` | Switch model variant (e.g., quantized sizes) without losing context |

Plus mode-switch commands (`/plan`, `/build`, `/chat`, `/thinking`) and `/export`, `/undo`, `/redo`.

---

## 6. Key Features → Implementation Map

| Feature | Implementation |
|---|---|
| Git & GitHub integration | `internal/tools/git.go`, `github.go`; `/review` diffs; PR creation; repo download from a link |
| Terminal access | Sandboxed `bash` tool with allowlist/denylist, dry-run preview for dangerous commands |
| Function & tool calling | Native tool-use loop (Ollama + OpenAI-compatible); JSON schema tools |
| Audit logs & privacy guardrails | `internal/audit/` — every request/response/tool call logged, encrypted at rest (age/NaCl); redaction filters |
| Zero-Data-Leak mode | Config flag: local-only routing, blocks cloud providers, disables telemetry entirely |
| Dynamic cost tracker | Token counting per provider/model + pricing table; `/status` dashboard; budget limits with hard stops |
| Latency optimizer | `internal/router/` — measures per-provider TTFT/tokens-per-sec; routes to fastest eligible model |
| Dynamic API fallbacks | On error/rate-limit/timeout: retry on next provider in ranked list; automatic + configurable |
| Semantic caching | sqlite-vec embedding similarity cache with TTL; shows hit savings in `/status` |
| Database support | SQLite everywhere (sessions, cache, RAG, audit index); migrations on startup |
| Encrypted local audit logs | Per-user key derived at first run; logs unreadable without key |
| Language-specialized support | Language servers detection; per-language tool prompts; Chroma highlighting; framework-aware context |
| Repo download from link | `/connect`-style flow: clone GitHub/GitLab/HTTP repo, index it, open a session on it |
| Skills download & run | Skill = markdown instructions + optional scripts; fetched from git/registry; `/skills` to manage |
| Browser support | `browser` tool (fetch URL, read text, extract main content); safe URL allowlist in config |
| Unified token counter | Provider-agnostic tokenizer estimates + exact provider counts; shown per message |
| Middleware hooks & custom hooks | YAML/Go hook points: `on_request`, `on_response`, `pre_tool`, `post_tool`, `on_error` |
| Local RAG sandbox | `internal/rag/` — ingest codebase/docs into sqlite-vec; `@rag:<query>` in chat |
| Built-in vector DB | sqlite-vec embedded — no external service |
| Local model optimization & quantization manager | Recommends GGUF quants per GPU VRAM (e.g., 4 GB → Q4_K_M 7B); pulls via Ollama; `/variants` |
| LoRA / adapter management | One-click pull/apply Ollama adapters; per-session adapter pinning |
| Prompt versioning & library | `internal/prompts/` — versioned templates, diff between versions, share via git |
| Explainability & debugging | `/debug` traces: raw prompts, tool calls, token breakdowns, router decisions |
| Custom plugin system (pre/post-processing) | WASM plugins (wazero) + Go plugins; hot-reload on file change |
| Cross-platform client libraries & webhooks | `pkg/ycodeclient` Go SDK + local HTTP API; outbound webhooks on session events |
| Repo-aware context injection | File-tree index, symbol index, recent-changes weighting; auto-injected into system prompt |
| Automatic test generation & execution | `testgen` tool: generate tests for edited files, run them, feed failures back to agent |
| Codebase-wide refactoring w/ safety nets | Multi-file edit plan → dry-run diff → approval → apply with git branch + auto-revert checkpoint |
| Code explanation & docs generation | Chat/tool to explain symbols, generate docstrings/README updates |
| Security & vulnerability scanning | `security` tool: pattern scan + AI review; blocks committing flagged secrets |
| Pair programming mode w/ Git integration | Shared session state; commits attributed per agent action; branch-per-session |
| Multi-language & framework abstraction | Detected via `internal/context/`; prompts adapt (React, Go, Rust, Python, etc.) |
| Customizable coding style & linting | Config: style guide text injected into prompts; runs project linters via tools |
| CI/CD pipeline integration | `ycode ci` subcommand: run review/test-gen in CI; posts summary as PR comment |
| Collaborative code review w/ AI summaries | `/review` on PRs/diffs; summary + per-file findings exported |
| Natural language → shell & IaC | Bash tool with NL confirmation preview; terraform/k8s yaml generation helpers |
| Model registry | `internal/providers/registry/` — catalog of free/local models, one-click install, version pinning |
| Offline batch queue | `internal/batch/` — queue jobs when offline; runs when a provider is reachable |
| Context window auto-management | Token budget tracker; auto-compacts/summarizes history; pins critical context |
| Prompt template library & versioning | Same as prompt library; community templates downloadable as skills |
| Prompt injection & safety scanners | Scanner middleware strips/detects injection patterns in tool outputs and URLs |
| Scheduled & event-driven automations | Cron/file-watch triggers → run agents/skills (e.g., nightly dependency review) |
| Plugin ecosystem w/ hot-reload | Plugin dir watched; reload without restart; signed plugin manifest |
| One-click model swapping w/ context migration | `/models` swap keeps session context; re-encodes/summarizes if context differs |
| Session resume | All sessions in SQLite; `/sessions` picker; crash recovery |
| Local model auto-detection | On startup probes `http://localhost:11434/api/tags`; lists installed + suggests pulls |
| Detect & optimize for RTX 2050 | Hardware probe (VRAM) → default to Q4 quants, small embed model, conservative concurrency |

---

## 7. Data & Storage Layout

```
~/.ycode/
├── config.yaml                   # User config (API keys via env vars or keyring)
├── keyring.key                   # Audit-log encryption key (chmod 600)
├── ycode.db                      # Sessions, cache, RAG, cost, audit index (SQLite)
├── audit/                        # Encrypted audit log files (append-only)
├── sessions/                     # Session transcripts (sqlite-backed)
├── plugins/                      # Installed plugins (WASM)
├── skills/                       # Downloaded skills
├── prompts/                      # Prompt template library
└── models/                       # Ollama model recommendations cache

<project>/.ycode/
├── config.yaml                   # Project overrides
├── agents.md                     # Project agent instructions (repo-aware context)
└── hooks.yaml                    # Project-level hooks
```

---

## 8. Shortcuts (opencode-compatible)

| Key | Action |
|---|---|
| `Ctrl+C` | Cancel current stream / quit if idle |
| `Ctrl+D` | Exit |
| `Ctrl+L` | Clear screen (keep session) |
| `Ctrl+N` | New session |
| `Ctrl+O` | Switch model |
| `Ctrl+P` | Command palette (`/` menu) |
| `Ctrl+R` | History search |
| `Ctrl+T` | Toggle thinking mode |
| `Ctrl+U` | Clear input |
| `Ctrl+Y` | Redo last edit |
| `Ctrl+Z` | Undo last edit |
| `Tab` / `Shift+Tab` | Cycle mode: Plan → Build → Chat → Thinking |
| `Esc` | Stop generation / close panel |
| `↑` / `↓` | Input history |
| `PageUp/PageDown` | Scroll chat |
| `@` in input | Attach file/folder/symbol |
| `!` in input | Shell command preview |

---

## 9. Security & Privacy Model

1. **Keys never stored in plain text** — env vars or OS keyring; config stores key names only.
2. **Zero-Data-Leak mode** — routing layer hard-blocks any non-local provider; UI badge shows "LOCAL ONLY".
3. **Tool sandbox** — bash tool runs in project dir with configurable allowlist; network access disabled by default.
4. **Prompt injection scanning** — all tool outputs and fetched URLs scanned before entering the model context.
5. **Encrypted audit logs** — age-encrypted, append-only; export requires the user key.
6. **Redaction** — secrets/keys detected in prompts are redacted in logs and optionally before sending.

---

## 10. Milestones

| Phase | Deliverable |
|---|---|
| **M1 — Core** | `ycode` binary opens TUI; Chat mode; Ollama + Gemini + OpenRouter + Groq streaming; `/help` `/exit` `/models` `/new` `/sessions`; session persistence; opencode shortcuts; shows "ycode" splash |
| **M2 — Agent loop** | Build mode; tools (read/write/edit/grep/glob/bash); Plan & Thinking modes; `/agent` `/init` `/editor` `/status`; context auto-management; cost tracker |
| **M3 — Integrations** | Git/GitHub tools; `/review`; repo clone from link; MCP support (`/mcps`); hooks; skills download (`/skills`); browser tool |
| **M4 — Intelligence** | Local RAG sandbox; semantic cache; router with latency optimizer + API fallbacks; security scanner; test generation; refactoring with safety nets |
| **M5 — Ecosystem** | Plugin system (WASM, hot-reload); prompt library + versioning; webhooks + client SDK; automations; batch queue; CI integration; model registry with one-click installs; LoRA management |
| **M6 — Hardening** | Audit encryption polish; Zero-Data-Leak audits; cross-platform release pipeline (goreleaser); docs; community skills/plugins store |

---

## 11. GitHub & Release Strategy

- **Repo**: `github.com/<org>/ycode` — every change pushed via PR + CI (lint, test, build).
- **Branches**: `main` (stable), `develop` (integration), feature branches `feat/*`, `fix/*`.
- **Releases**: goreleaser on tag → binaries for Linux/macOS/Windows (amd64 + arm64).
- **Install methods** (documented in README):
  1. `curl -fsSL https://raw.githubusercontent.com/<org>/ycode/main/scripts/install.sh | bash`
  2. `go install github.com/<org>/ycode/cmd/ycode@latest`
  3. `brew install <org>/tap/ycode`
  4. Scoop (Windows): `scoop install ycode`
  5. Download binary from GitHub Releases
- **CI/CD**: GitHub Actions — test matrix (Go versions × OS), golangci-lint, goreleaser release, nightly builds.

---

*This plan is the source of truth for implementation. Update it as decisions change.*
