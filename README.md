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

> Status: v0.12.1. Milestones M1–M6 implemented (see `plan.md`).

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
13. [By the numbers](#13-by-the-numbers)
14. [Configuration reference](#14-configuration-reference)
15. [Project structure](#15-project-structure)
16. [Data layout](#16-data-layout)
17. [Development](#17-development)
18. [Releases & CI](#18-releases--ci)
19. [Troubleshooting](#19-troubleshooting)
20. [License](#20-license)

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
YCODE_VERSION=v0.12.1 BIN_DIR=/usr/local/bin ./scripts/install.sh
```

**Option B — package managers:**

```sh
brew tap Yash-K-Jagani/ycode && brew install --cask ycode   # macOS
scoop bucket add ycode https://github.com/Yash-K-Jagani/ycode-scoop
scoop install ycode                                         # Windows
winget install Yash-K-Jagani.ycode                          # Windows
```

See [docs/distribution.md](docs/distribution.md) for the state of each channel
and what it takes to enable the package managers.

**Option C — go install (adds the `ycode` command):**

```sh
go install github.com/Yash-K-Jagani/ycode/cmd/ycode@latest
# ensure $(go env GOPATH)/bin is on PATH
```

**Option D — build from source:**

```sh
git clone https://github.com/Yash-K-Jagani/ycode.git
cd ycode
./scripts/build.sh              # injects version/commit/date from git
go build -o ycode ./cmd/ycode    # plain build, reports 0.0.0-dev
go install ./cmd/ycode           # install as the `ycode` command
```

Verify: `ycode version` → `ycode 0.12.1 (commit abc1234, built 2026-09-25…, linux/amd64)`.
`ycode --version` prints the same thing.

**Upgrading:** `ycode upgrade` checks GitHub for a newer release, and
`ycode upgrade --apply` downloads it, verifies the published SHA256, and
replaces the running binary. On Windows the current executable is locked
against overwrite, so it is renamed to `ycode.exe.old` and cleaned up on the
next successful upgrade.

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
  ollama pull qwen2.5-coder:3b
  ycode config set active_model qwen2.5-coder:3b
  ycode setup                        # interactive wizard
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

- **Chat pane** (markdown, code blocks on tinted panels, command chips), **input box** with a mode chip (`▸ build · builder`, or `▸ goal · builder · goal 3/12 active` during a goal run), keystroke command palette, status bar.
- **Right sidebar** (`Ctrl+B`): folder, model, session tokens in/out, context meter, session + daily spend, the active goal with its iteration counter, and a live task
  list. Big tasks are auto-broken into `todo` steps shown here as they complete.
- Type `/` for the **command palette**: filters as you type, `↑↓` to move, `Tab`/`Enter` to complete, `Enter` again to run, `Esc` to dismiss.
- `Ctrl+O` cycles installed Ollama models; `Ctrl+N` new session; `Ctrl+P` command palette; `Ctrl+R` resume a session; `Ctrl+\` file tree; `Ctrl+=` compact mode; `Tab`/`Shift+Tab` cycle modes; `Ctrl+C` cancels; `Ctrl+D` quits. Full list: `docs/shortcuts.md`.
- Every turn streams token-by-token; tool calls show as `🔧` lines; turn footers show token/cost/RAG notes.
- **Long-running tools stream live.** `bash`, `run`, `testgen` and `notebook` show their output while they run, so a five-minute test suite is visibly progressing instead of looking like a hang. The live block is provisional and bounded — it is replaced wholesale on each update and dropped when the call finishes, because the complete output arrives then as a permanent card. Output is batched into the UI at ~16 Hz, so a build that prints ten thousand lines does not turn the terminal into a CPU spinner. Headless runs write the same chunks to their log, which is what keeps a CI job from looking hung.
- Codebase-aware: every build/plan/goal turn sees the repo tree, README head, `AGENTS.md` rules, and git branch/status — plus `edit` tolerates `12: ` line prefixes and suggests close matches on miss.

---

## 5. Modes

| Mode | Tools | Purpose |
|---|---|---|
| `build` | all (read/write/edit/bash/git/…) | Implement, edit, run tests |
| `goal` | all but `delete` | Work a goal unattended until it's met, blocked, or out of budget |
| `plan` | read-only subset | Clarifying questions when vague, then numbered plan ending in `AWAITING APPROVAL` |
| `chat` | none | Plain conversation (file tasks nudge you to `/build`) |
| `thinking` | none | Visible `<scratchpad>` reasoning, then the answer |

Switch with `Tab` or `/plan` `/goal` `/build` `/chat` `/thinking`. The agent flavor comes from `/agent`
(`builder`/`planner`/`reviewer`). Flow: plan in plan mode, switch to build, say **build it** to implement.

**Goal mode** is for handing over a whole objective instead of a single turn:
`/goal add a healthcheck endpoint` switches to goal mode and starts working
immediately. The agent turns the goal into acceptance criteria, records them in
the todo list, and works one step per iteration without asking you anything. It
ends the run itself with `GOAL MET` (evidence per criterion) or
`GOAL BLOCKED: <reason>`, or stops when the 12-iteration budget is spent. A
claimed `GOAL MET` is **checked, not believed** — it only counts if a tool that
changes something succeeded, nothing errored, and the task list is closed.
The sidebar and `/status` show the goal, the iteration counter and the task
list; `Esc` stops a run. Goals live in the session only — `/new` clears them.

Headless, `ycode run --mode goal "…"` runs the same loop and **exits with the
result**: `0` met, `2` blocked, `3` budget exhausted, `4` stalled, `5`
cancelled, `1` error (`--goal-iters` caps the budget). CI should treat
non-zero as "not done"; this is a change from earlier versions, which always
exited 0.

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
| `/budget [usd]` | Show the daily spend limit and today's spend, or set it (`/budget 1.50`, `/budget 0` for none). Turns are refused once the limit is reached |
| `/debug [turn\|full\|list\|clear]` | Trace the last turn: the prompt, tool calls with arguments and results, token breakdown, and every routing decision |
| `/move <dir>` | Work in a different directory. Moves no files — sessions live under `~/.ycode` — and rebuilds the tool registry so writes follow. Earlier paths in the conversation still refer to the old tree |
| `/different [models…] [full]` | Re-ask the last question to several models and show the answers side by side, with per-row token cost and latency. With no arguments, compares the active provider plus every other one you have a key for |
| `/connect` | Interactive window: provider → API key/host → model picker |
| `/doctor` | Health check: tools, Ollama, RAG, cache, model advice |
| `/agent [name]` | Pick builder/planner/reviewer |
| `/init` | Scaffold `.ycode/` + `AGENTS.md` in the project |
| `/editor [path]` | Open `$EDITOR` without leaving the TUI |
| `/plan` `/goal` `/build` `/chat` `/thinking` | Switch mode |
| `/goal [text]` | Goal mode: with text, set the goal and start working it unattended; without, show the current goal |
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
ycode upgrade [--apply]          # check for / install a newer release
ycode status             # provider health, models, cost today
ycode version            # version + commit + date + os/arch
ycode doctor [--fix]     # environment self-check
ycode run "task" [--mode build|plan|goal|chat] [--agent builder] [--goal-iters N]
                                          # goal mode: exit 0 met, 2 blocked, 3 budget, 4 stalled
ycode serve [--addr 127.0.0.1:8471]  # local HTTP API; prints an auth token
ycode batch add|list|run|clear        # offline job queue
ycode store list|search|install|remove|verify|update # curated skill/plugin store
ycode ci [--post]                   # diff review + tests, --post comments on the PR
ycode daemon                          # interval automations → queue → run
ycode audit [--date YYYY-MM-DD|list]  # decrypted local audit log
```

Config keys: `active_provider`, `active_model`, `ollama_host`, `theme`,
`zero_data_leak`, `daily_budget_usd`, plus one `<provider>_api_key` per
key-bearing provider
(`gemini_api_key`, `openrouter_api_key`, `groq_api_key`).
The `*_api_key` values are written to the **OS keyring**, never to
`config.yaml`, so `ycode config` output is safe to paste into an issue. Each
provider's key is stored separately — setting one never populates another.

**Headless exit codes** (`ycode run`, and its `ops` subcommand). Every failure
has its own code so a CI job can tell them apart without parsing the log:

| Code | Meaning | Fix |
| --- | --- | --- |
| 0 | Done | — |
| 2 | `blocked` — the model reported it could not continue | reword the goal |
| 3 | `budget_exhausted` — the iteration budget ran out | raise `--goal-iters` |
| 4 | `stalled` — a turn ran but made no tool calls | reword, or use a larger model |
| 5 | `cancelled` | — |
| 6 | `unverified` — ended with the goal still active | read the log |
| 7 | `spend_limited` — `daily_budget_usd` reached | raise the budget, or go local |

3 and 7 are both "stopped early, work still owed" and are deliberately distinct:
one means raise the iteration count, the other means raise the spend limit.
Sending a CI job to the wrong setting is worse than either error.

API: `GET /healthz`, `GET /v1/models`, `GET /v1/status`, `POST /v1/chat`
`{prompt, mode?, agent?, workdir?, goal_iters?}` (`mode: goal` runs the goal
loop and returns `goal_status`). **Auth:** `ycode serve` prints a token, stored
in `~/.ycode/api_token` (or `YCODE_API_TOKEN`); send it as
`Authorization: Bearer …`, `X-Ycode-Token: …`, or `?token=…`. It is *required*
for any non-loopback bind and optional on `127.0.0.1` — `/v1/chat` runs the
agent with shell and file tools, so an open unauthenticated port is remote code
execution. `YCODE_ALLOW_ANONYMOUS_API=1` restores anonymous access for scripts
predating this. Go SDK: `pkg/ycodeclient`
(`New(base).Chat/Models/Status/Health`; reads the token automatically).
Outbound webhooks (`turn_complete`, `turn_error`, `session_start`) via
`~/.ycode/webhooks.yaml`.

---

## 8. Providers & models

One OpenAI-compatible streaming path + native Ollama (`/api/chat` NDJSON,
`/api/tags`, `/api/embed`). Keys come from the environment or, if you paste
one during setup or `ycode config set`, from the **OS keyring** — never as
plaintext in `config.yaml`. `/connect` activates a pasted key for the session
and prints the `export` line to persist it yourself.

On primary failure, configured cloud providers are retried as fallbacks
(gemini → openrouter → groq defaults); per-provider latency lives in
`~/.ycode/router.json` and shows in `/status`. Details: `docs/providers.md`.

---

## 9. Agent tools

Build mode tools (plan gets the read-only subset, goal gets all of them except `delete`):
`read` (multi-path) `write` `create` `add` `edit` `remove` `changes`
`grep` `glob` `bash` (denylist, 60s, 32KB cap) `git` (secret-scanning commit
gate) `github` (clone/PRs/issues, `owner/repo` shorthand) `browser`
`testgen` (Go/Rust/Node/Deno/Bun/Java/C#/PHP/Ruby/Python, name filter + extra args) `security` `tree`
`todo` `memory` `patch` `run` (execute code: python/js/ts/go/bash/powershell/ruby/php/java/rust) `delete` (guarded) `summary` `db` (mongo/postgres/mysql) `notebook` `api` (REST) `vscode` `scaffold` (react/express/fastapi) `models` (gguf) `task` (delegate to a subagent) — plus dynamic `mcp__*` and `plugin__*` tools.
Chat code blocks get Chroma syntax highlighting with line numbers; write/edit results show red/green diffs. File reads/writes show path cards; shell commands their own tint.

Streaming tools (`bash`, `run`, `testgen`, `notebook`) additionally put their
output in the transcript on completion, capped at 4 KB of the tail — the end is
where a build log explains itself. Tools that do not stream are unchanged: a
`read` result is a file, and putting one in the transcript uninvited is noise.

Models emit `<tool:name>{json}</tool:name>` (tolerant parser, schema-error
retries, repeat-guard with cached results, 6-round cap in plan, 8 in build, 16
in goal). Small local models
work; 3b+ coders follow instructions far better than 1–2b ones.

**Native tool calling.** When the provider supports it and the mode has tools,
ycode sends the tool schemas in the request and takes the model's calls back as
structured data, instead of asking it to write a `<tool:...>` tag into prose. The
schemas move out of the system prompt where they were being reproduced from
memory, and long arguments stop being truncated or hallucinated — which is where
small models lose tool calls most often.

It works on **all four providers**, including local Ollama, which is where it
matters most: small models are the ones that follow a schema poorly when it is
prose, and the ones most likely to truncate a long tag. Ollama's dialect differs
from OpenAI's in three ways that each break a direct reuse of the message type —
tool calls live in `message.tool_calls` rather than a parts array, `arguments` is
an object rather than a string, and there are no call ids at all — so it gets its
own translation rather than a shared one.

It is chosen at runtime by capability, not by config: Gemini follows schemas
natively, some OpenAI-compatible servers do not, and Ollama's support depends on
the model. Anything that does not support it uses the text protocol, so nothing
is lost by a provider not implementing it. A tool-less turn (chat mode) sends no
schemas at all — offering tools the model was never going to call just spends
prompt tokens on every message.

Three details are easy to get wrong and are covered by tests:

- A native turn whose prose *mentions* a `<tool:...>` tag must not execute it as
  well. Models do both in one message, so the two protocols are kept exclusive
  per turn: a provider reporting calls means tags are not parsed.
- The assistant's calls must be carried into the next request with the same ids.
  Without them the results refer to nothing, and the provider either rejects the
  conversation or assumes the tools ran itself. Ollama has no ids, so provenance
  is tracked separately from the id rather than derived from it.
- Native calls must be refused when the mode forbids the tool, exactly as parsed
  ones are. Structured access to a tool is not a way around the allow-list.

---

## 10. RAG, cache & router intelligence

- **Local RAG**: `/rag ingest [path]` chunks the repo (40-line windows) and
  embeds via Ollama (`nomic-embed-text`); top-4 chunks auto-inject into
  build/plan/goal turns; `/rag <query>` searches manually. JSON index per project.
  An index built with a different embedding model is reported rather than used:
  comparing vectors of different lengths used to score the overlapping prefix,
  which returned a confident ranking of unrelated text. Re-run `/rag ingest`
  after changing models.
- **Semantic cache**: exact + 0.985-cosine hits per provider+model, 7-day TTL,
  chat/thinking only (never tool turns). Hits reply instantly with `⚡`.
- **Router**: latency stats + cloud fallbacks (both single-shot and agent-loop
  turns, clean stream reset on retry).

---

## 11. Skills, plugins, MCP, hooks, prompts

### Subagents

`task` delegates a read-only question to a subagent with its own context window
and gets back a summary. The point is the parent's context: "search the repo for
every place this is called" becomes one tool call and a result line, instead of
40KB of matches competing for the context the rest of the work needs.

Available in `build` and `plan`, with two personas: `explorer` (search and
report) and `analyst` (read and explain). Give it a broad question
("what does this module do", "find every usage of X"); use ordinary tools for
anything specific.

**It can only read.** A subagent's allow-list is the *intersection* of what the
parent may use and what is marked delegable through the `Isolatable` interface —
currently 11 read-only built-ins. That intersection is deliberate: it means
delegation is always a narrowing, never a way to reach a tool the parent could
not. There is a test that a subagent asked to overwrite a file is refused and
told so.

This is a narrower allow-list, **not a sandbox**. A subagent is a model call that
can call the tools it was given; what keeps it safe is that every one of those
tools only reads.

Also bounded, because a subagent that hangs holds the parent's tool call open
where the parent can neither see nor cancel it: 4 tool rounds and 3 minutes by
default, and answers are capped at 8KB so a subagent cannot defeat its own
purpose by returning a wall of text. Progress appears as `subagent:` lines, and
`/debug` shows the question it was asked.

Works headlessly too — `ycode run --mode build` and unattended goal runs can
delegate, which they could not when the feature first landed. Under
`zero_data_leak` the delegate is removed from the registry entirely rather than
left unwired, so a local-only user does not get "not available in this context"
and conclude the feature is broken.

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
  fallbacks hard-blocked, plus every tool that reaches the network (`api`,
  `browser`, `github`, `git`, `db`, `notebook`, `scaffold`, `vscode`, all
  `mcp__*`/`plugin__*`), red `[LOCAL ONLY]` badge. A guardrail, not a sandbox:
  `bash`/`run` still execute arbitrary commands. See `docs/security.md`.
- **`daily_budget_usd: 1.50`** (default `0`, meaning no limit): a hard ceiling on
  a day's spend. A turn that would begin over the line does not begin, and an
  unattended goal run stops at the next iteration rather than mid-token. Covers
  the TUI, headless runs and goal runs alike, since all three record into the same
  tracker. Set it in-session with `/budget 1.50`, watch it in the sidebar, read it
  with `/budget`. There is no soft mode on purpose — a warning that then spends
  anyway is the worst of both. Local models cost nothing, so a limit only matters
  for cloud providers.
  Spend comes from the token counts the provider reports, not an estimate; when a
  provider reports none, nothing is charged and the log says `unmeasured` rather
  than showing a confident zero. A headless goal run stopped by the budget exits
  **7** (`spend_limited`), distinct from **3** (`budget_exhausted`, the iteration
  count), so CI can tell "raise `MaxIter`" from "raise `daily_budget_usd`".
- Full model: `docs/security.md`.

---

## 13. By the numbers

Every figure here is measured, and the benchmark that produces it is named. Where
a change made something faster, the before and after are both given — a speedup
with only the "after" is marketing. Where a benchmark found a problem rather than
confirming one, that is said too.

```bash
go test -run '^$' -bench . -benchmem ./internal/...
```

Measured on Windows/amd64, Go 1.27.1, 13th-gen Core i5-13420H, `-benchtime=200x`.

### Size

| Metric | Value |
| --- | --- |
| Packages | 49 (45 with tests) |
| Go source | 22,908 lines across 138 files |
| Go tests | 21,238 lines across 139 files |
| Test functions | 827 |
| Benchmarks | 51 |
| Test-to-source ratio | 0.93 |
| Registered tools | 29 built-ins |
| Slash commands | 31 |
| Providers | 4 (1 local, 3 cloud) |
| Commits | 132 |

Test lines are close to source lines on purpose. Most of what is here is
behaviour that is invisible until it breaks: a provider that returns HTML, a
context trim that corrupts history, a fallback that splices two responses
together.

### The cost of a turn, excluding the model call

This is the number that matters most, because it is the part of a turn ycode is
responsible for. From `BenchmarkTurn*` and the provider/router benchmarks, against
a scripted provider and a local server:

| Path | Time | Allocated |
| --- | --- | --- |
| Chat, no tools | 2.0 µs | 725 B, 20 allocs |
| Four tool calls | 67 µs | 9.0 KB, 110 allocs |
| Eight rounds deep | 43 µs | 6.2 KB, 76 allocs |
| One streamed chunk, parsed end to end | 297 µs | 22 KB |
| Provider call, warm cache | 3.9 µs | 112 B |
| Provider call, first (cold cache) | 91 µs | 4.1 KB, 45 allocs |
| Usage accounting, per provider attempt | 53 ns | 0 B |

Microseconds against a model call measured in hundreds of milliseconds. The
harness is not where a turn's latency goes, and these are the numbers that would
show it if that ever changed.

**The provider cache is worth 67×** — 91 µs for the first call against 3.9 µs
warm. That ratio is the whole argument for the cache: it is what stopped every
turn paying a fresh TCP connect and TLS handshake to a host it had just been
talking to. The router's usage ledger costs 53 ns per attempt, so what the daily
budget reads is effectively free.

### Measured speedups

Each row is a pair of benchmarks in the same run.

| Change | Before | After | Speedup | Memory |
| --- | --- | --- | --- | --- |
| Session listing, 1,000 sessions (`ListMeta` vs `List`) | 174.2 ms | 1.34 ms | **130×** | 51.7 MB → 167 KB (**310×**) |
| Context trim, 1,000 messages (vs the old implementation) | 1,157.9 µs | 15.6 µs | **74×** | 1.93 MB / 254 allocs → 14.3 KB / **1 alloc** |
| Transcript render per streamed delta (incremental vs rejoin) | 18.9 ms | 0.70 ms | **27×** | 16.4 MB → 747 KB (**22×**) |
| Live tool output, 50 lines (batched vs per-chunk) | 9.89 ms | 0.56 ms | **17.6×** | 2.19 MB → 332 KB (**6.6×**) |
| Six read-only tool calls (concurrent vs sequential) | 123.3 ms | 20.7 ms | **6.0×** | 81 → 107 allocs |
| Trace record, 250 KB tool output (truncate before redact) | 171 ms | 7.8 ms | **22×** | 3.0 MB → 495 KB (**6.1×**) |
| Redaction, ordinary prose (skip absent trigger words) | 74.6 µs | 51.0 µs | **1.46×** | — |
| Redaction, 1 KB of prose | 1.90 ms | 1.46 ms | **1.30×** | — |

Two of these also fixed a correctness problem, not just a slow one. The trim was
**corrupting conversation history** as well as being quadratic; incremental
rendering stopped re-joining every message on every token, which also stopped the
transcript flickering.

### Where the time goes in the tool parser

`ParseCalls` runs on every assistant message. It is two regex passes, and the
second only runs when the first finds nothing — an ordering that is a deliberate
optimisation and is worth having:

| Input | Time |
| --- | --- |
| Prose, no tool call | 4.7 µs |
| One well-formed call | 4.2 µs |
| Four calls | 15.9 µs |
| Malformed fence (tolerant path) | 3.7 µs |

### Two shapes worth knowing about

Benchmarking rather than reading found these; both are bounded, and both now have
scaling benchmarks that will show a change.

**`ParseCalls` is quadratic in stray tool openers.** 10 → 43 µs, 50 → 358 µs,
200 → 3.3 ms. The tolerant scan calls `extractObject` once per opener and each
call scans forward to the end of the message when nothing balances. A large tool
argument is mildly superlinear too: 1 KB → 60 µs, 8 KB → 431 µs, 64 KB → 7.1 ms.
Both are capped in practice by the round limit and the repeat guard, and a 64 KB
tool argument is a normal large file write rather than an attack. Fixing it means
changing the most delicate parser in the loop, so it is measured and documented
rather than rewritten.

**Redaction is dominated by two rules.** On a 48-byte string, `high-entropy-assign`
costs 24.6 µs and `api-key-assign` 12.5 µs, against ~3 µs for each fixed-format
rule — they open with `[a-z0-9_.-]*` before a keyword alternation, so the engine
re-walks the text looking for a prefix that might match. Both require a literal
word to be present, so `Redact` now skips them when none occurs. The optimisation
is held up by two tests: every listed trigger must appear in its own compiled
pattern, and 35 samples of real secret shapes must produce byte-identical output
with the triggers populated and disabled.

### Absolute figures

The paths a user feels, with no before/after.

| Benchmark | Time | Allocated |
| --- | --- | --- |
| Token estimate, one message | 462 ns | 146 B, 0 allocs |
| Coalescing writer, pass-through | 189 ns | 8 B, 1 alloc |
| Coalescing writer, 40 ms batched | 328 ns | 17 B, 0 allocs |
| Single streamed delta | 151 µs | 57 KB, 321 allocs |
| One live tool chunk, handled + rendered | 247 µs | 49 KB, 159 allocs |
| Sidebar render | 142 µs | 13 KB, 239 allocs |
| Trace record, small note | 11 µs | 1.6 KB, 48 allocs |
| Trace render, 600 records | 330 µs | 181 KB |
| Repo tree, 10,000 entries | 12.5 ms | 11 KB, 202 allocs |
| Cache store, one upsert | 873 µs | 1.6 KB, 46 allocs |
| Streaming render, 2,000 tokens | 175 ms | 166 MB cumulative |
| Streaming render, unterminated code fence | 41.8 ms | 15 MB |

Batching costs 139 ns: a delta every 40 ms is 328 ns instead of 189 ns. That is why
the delta coalescer is free and why the live tool gate was affordable.

**Streaming render, 2,000 tokens** allocates 166 MB *cumulatively* across the
stream. That is not 166 MB resident: it is garbage collected as it goes, and the
live view is a few KB. It is listed because a number that looks alarming should be
explained rather than omitted.

**Unterminated code fence** is the worst case for the streaming renderer — the
whole buffer is an open fence, so it cannot find a boundary to stop at. It is a
pathological input, and it is measured because worst cases are what decide whether
a terminal stays usable.

### What is not measured

- **No end-to-end latency numbers against live providers.** Everything here is
  local, synthetic, and reproducible in CI. Cloud latency is the provider's, not
  ycode's, and quoting it would say nothing about ycode.
- **`go test -race` does not run locally.** The toolchain here is 32-bit MinGW
  (`gcc -dumpmachine` → `mingw32`), and `race` needs 64-bit: `cc1.exe: sorry,
  unimplemented: 64-bit mode not compiled in`. The race detector runs in CI
  instead, which is where it belongs anyway.
- **No benchmark for the tool loop as a whole**, only its parts. A turn's real
  cost is dominated by the model call, which is not ycode's to measure.
- **Subagent quality is not benchmarked.** Its latency and cost are bounded and
  documented; whether a given local model delegates *well* is a question about the
  model, and answering it needs real prompts rather than fixtures.
- **`cost.Tracker.Add` is O(days), not O(1).** It rewrites its whole history to
  disk on every call — measured at ~550 µs with one day recorded and 1.2 ms with
  a year. Fine per turn, but it is the one unbounded thing in the cost path, and
  `/different` calls it five times for one user action. Fixing it is a storage
  strategy change rather than a tidy, so it is measured and left alone.
- **A test that passed locally for an accidental reason.** CI failed
  `TestExecStreamDoesNotDeadlockOnLargeStderr` on every run while it passed on
  this machine, and it was recorded here as a load-sensitive flake. It was not a
  flake, and the local pass was meaningless.

  The bug was real. Once the retained-output buffer filled, `execStream` stopped
  accepting output, so a command that printed a megabyte of build noise and then
  failed kept the noise and discarded the failure — the one line that said what
  went wrong. Retention now keeps the tail.

  The local pass came from `shellCmd`, which runs `sh -c` on Unix and
  `powershell -Command` here. PowerShell cannot parse the test's `1>&2`
  redirection and raises a `ParserError` without executing anything, so the
  stderr flood never happened. The test then asserts that stdout contains
  `done`, and PowerShell's diagnostic quotes the line it failed to parse —
  which contains `printf done`. The assertion matched a substring of the error
  message and passed.

  Worth stating plainly, because it invalidates a habit: a green run is evidence
  only for the scenarios that run. This test has been reporting on a scenario
  that never executed here, and would have kept doing so.
- **One known gap in redaction**, found by benchmarking rather than by reading the
  patterns: every assignment rule requires the keyword at the *end* of the name, so
  `db_secret_value=...` and `api_secret_value=...` are not redacted. Recorded in a
  test that fails if the behaviour changes. Widening the patterns changes the
  false-positive cost — a tool log full of `[REDACTED]` is its own problem — so
  that is a decision to make deliberately rather than inside a benchmarking change.
## 14. Configuration reference

`~/.ycode/config.yaml` (user) + `<project>/.ycode/config.yaml` (overrides;
see `configs/ycode.yaml` for defaults):

```yaml
ollama_host: http://localhost:11434
theme: dark
active_provider: ollama
active_model: ""
zero_data_leak: false
daily_budget_usd: 0
gemini_key_env: GEMINI_API_KEY
openrouter_key_env: OPENROUTER_API_KEY
groq_key_env: GROQ_API_KEY
```

Other files: `mcp.json`, `hooks.yaml`, `webhooks.yaml`,
`automations.yaml` (`automations: [{name, prompt, mode, interval_minutes}]`),
`prompts/`, `skills/`, `plugins/` — all under `~/.ycode/`.

`ycode config set` validates before it writes, and nothing is saved when a value
is rejected — a bad value used to print a complaint and then write the file
anyway. A leading `-` is read by the flag parser rather than as a value, so a
negative number needs `--`: `ycode config set daily_budget_usd -- -1`.

---

## 15. Project structure

```
cmd/ycode/main.go            # CLI: TUI + run/serve/batch/ci/daemon/audit/version
internal/tui/                # Bubble Tea UI: app, palette, connect modal, render, slash, goal run
internal/modes/              # plan/goal/build/chat/thinking prompts, tool allow-lists, round budgets
internal/goal/               # goal state: verdict markers (GOAL MET/GOAL BLOCKED), iteration budget
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

## 16. Data layout

```
~/.ycode/
  config.yaml  keyring.key  ycode.db (sessions, cost, latency, batch, memory)
  cost.json  router.json  batch.json  memory.json  (legacy, auto-imported once)
  cache.json  mcp.json  hooks.yaml  webhooks.yaml store.yaml
  automations.yaml  audit/  sessions/ (legacy)  rag/  skills/  plugins/  prompts/
<project>/.ycode/  config.yaml  hooks.yaml  automations.yaml  todos.json
```

---

## 17. Development

```sh
go build ./...   # build
go vet ./...     # vet
go test ./...    # full suite (~16 packages; RAG live test needs nomic-embed-text)
golangci-lint run ./...   # lint (v2 config in .golangci.yml)
```

Conventions: small focused packages, table-less unit tests per package,
`gofmt` clean, no CGO (pure-Go SQLite path), Windows-first paths verified.

---

## 18. Releases & CI

- `.goreleaser.yaml`: `ycode_<os>_<arch>.tar.gz` for linux/darwin and
  `ycode_<os>_<arch>.zip` for windows, × amd64/arm64, plus `checksums.txt`.
  Version, commit, and build date are injected via `-X main.*` ldflags, so
  `ycode version` reports the real release. Tag to release:
  `git tag v0.12.1 && git push origin v0.12.1`.
- **Supply chain:** `checksums.txt` is signed keylessly with
  [Sigstore/cosign](https://docs.sigstore.dev/) over the release workflow's
  OIDC identity, so anyone can verify provenance without trusting a checked-in
  key. Verify the signature first, then the download:

  ```sh
  curl -LO https://github.com/Yash-K-Jagani/ycode/releases/download/v0.12.1/checksums.txt
  curl -LO https://github.com/Yash-K-Jagani/ycode/releases/download/v0.12.1/checksums.txt.sigstore.json

  cosign verify-blob \
    --certificate-identity 'https://github.com/Yash-K-Jagani/ycode/.github/workflows/release.yml@refs/tags/v0.12.1' \
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
    --bundle checksums.txt.sigstore.json checksums.txt

  sha256sum --check --ignore-missing checksums.txt
  ```

  Installers and `ycode upgrade` both verify the SHA256; the signature is what
  establishes that the checksum came from this repo's pipeline.
- **Package managers:** `homebrew_casks` (with a quarantine-stripping hook,
  since the binaries are unsigned), `scoops`, and `winget` publish a manifest
  per release. These pipes are skipped automatically when the corresponding
  `HOMEBREW_TOKEN` / `SCOOP_TOKEN` / `WINGET_TOKEN` secret is absent, so a
  missing tap never breaks a release.
- `.github/workflows/ci.yml`: build matrix (Go × OS) + a linux-only `-race`
  job + `go vet`/`go test` + gofmt + golangci-lint + `ycode ci` review on PRs.
- `.github/workflows/release.yml`: goreleaser + cosign installer + the
  optional-pipe resolution above.
- `scripts/test-skip-logic.sh` covers the shell in `release.yml` that decides
  which publish pipes to skip; run it with `bash`.

---

## 19. Troubleshooting

| Symptom | Fix |
|---|---|
| `/models` empty / ollama errors | Start Ollama; `ollama pull qwen2.5-coder:3b`; check `OLLAMA_HOST` |
| Model asks for paths | Use `/build` (chat has no tools); `/rag ingest` helps big repos |
| Raw `<tool:>` tags in answers / round-limit notes | Rebuild (`go install ./cmd/ycode`); try a 3b+ coder model |
| Plan mode calls nothing | Rebuild — old binaries predate plan tool docs |
| A goal run stops after N turns | That's the iteration budget: `/goal` again with a narrower goal, or `ycode run --mode goal --goal-iters 30` headlessly |
| Goal run burns tokens without finishing | 1.5b models rarely honour the `GOAL MET`/`GOAL BLOCKED` contract — use a 3b+ coder model (`/models`) |
| Cloud 4xx/rate limits | Check key env, `/status` latency/failures; fallbacks engage automatically |
| `golangci-lint-action` fails | Use golangci-lint v2 config (`version: "2"`, `formatters:` for gofmt) |
| `ycode` resolves to another program | `where ycode`; ensure `go/bin` wins or uninstall the clash |

Run `/doctor` inside the TUI for a guided health check.

---

## 20. License

MIT — see `LICENSE`. Plan/architecture source of truth: `plan.md`.
