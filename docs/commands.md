# Slash commands

| Command | Purpose |
|---|---|
| `/help` | This list |
| `/exit` | Quit (state saved) |
| `/new` | Fresh session |
| `/models` | Numbered model list; `/models <n>`, `/models <provider> <model>`, `/models install <ollama-model>`, `/models <file.gguf>` / `/models import <file.gguf>` |
| `/model` | Alias of `/models` |
| `/export [file]` | Save transcript as markdown |
| `/tools` | List tools available in the current mode |
| `/undo` | Restore last file change (keeps 20 backups) |
| `/variants` | Installed Ollama variants + quant guidance |
| `/sessions` | List; `/sessions <id>` resumes (restores its provider/model), `/sessions fork <id>` branches |
| `/status` | Mode, goal, tokens, cost today, cache, RAG, latency |
| `/connect` | Interactive connect: pick provider, paste key, choose model |
| `/doctor` | Health: tools, Ollama, RAG, cache, model advice (`/doctor fix` repairs) |
| `/agent [name]` | Pick builder/planner/reviewer |
| `/init` | Scaffold `.ycode/` + `AGENTS.md` |
| `/editor [path]` | Open `$EDITOR` without leaving the TUI |
| `/plan` `/goal` `/build` `/chat` `/thinking` | Switch mode (or Tab) |
| `/goal [text]` | Goal mode: with text, set the goal and start working it unattended; without, show the current goal |
| `/review [path]` | AI review of `git diff` (`/review --post <pr>` posts inline review comments) |
| `/test [path] [filter]` | Run project tests, optionally one test by name (`/test --watch [path]` re-runs on save, `/test stop` ends) |
| `/refactor <goal>` | Checkpoint branch + armed instruction |
| `/rag [ingest [path]|<query>]` | Local RAG |
| `/mcps [list|add|remove|tools]` | MCP servers |
| `/skills [list|install|run|export|import]` | Skills (install accepts git URL or owner/repo) |
| `/hooks` | Show configured hooks |
| `/prompts [list|show|run|save|versions]` | Prompt library |
| `/plugins [install|reload]` | Script plugins (install accepts git URL, owner/repo, or local dir) |
| `/store [list|search|install|remove|verify|update]` | Curated skill/plugin store |

Modes: **build** (all tools), **goal** (all but `delete`), **plan** (read-only tools), **chat**/**thinking** (no tools).
Plan flow: plan mode asks clarifying questions when vague, then writes a plan ending in `AWAITING APPROVAL`. Switch to build and say **build it** to implement the approved plan.

Goal flow: `/goal add a healthcheck endpoint` switches to goal mode and starts
working immediately. The agent restates the goal as acceptance criteria, records
them in the todo list (sidebar), and works one step per iteration — no
questions, no waiting for you. It ends the run itself with `GOAL MET` (with
evidence per criterion), `GOAL BLOCKED: <reason>`, or gives up when the
12-iteration budget runs out. The goal and the iteration counter live in the
sidebar and `/status`; `Esc` stops a run, and starting a new goal or session
clears it (goals are not persisted).

The task list is the goal's own: `/goal` clears the previous one so a stale
step list can't send the run down the wrong path.

A claimed `GOAL MET` is checked, not believed: it only counts if a tool that
changes something actually succeeded, nothing errored, and the task list is
closed. Otherwise the claim is rejected, the reason is shown, and the run
continues.

### Goal mode exit codes (changed in v0.13)

`ycode run --mode goal` used to exit 0 whatever happened, so a CI job could not
tell a finished job from a model that merely said it was finished. It now exits
with the result:

| Code | Meaning |
|---|---|
| 0 | the goal is met (or this was not a goal run) |
| 2 | `GOAL BLOCKED` — the model could not continue |
| 3 | the iteration budget ran out with work still owed |
| 4 | stalled — a turn made no tool calls, so nothing was done |
| 5 | cancelled (timeout or interrupt) |
| 6 | ended while the goal was still active, unclassified |
| 1 | a hard error (bad config, no provider, unknown agent) |

Migration: if you gate CI on `ycode run --mode goal`, treat anything non-zero as
"not done". Nothing else changes, and non-goal modes still exit 0 on success.
`POST /v1/chat` also returns `goal_status` so a client can distinguish them.

Type `@` in the input for path completion — attached files are inlined into
your message (24KB each, 5 max; `@dir` attaches a listing).
