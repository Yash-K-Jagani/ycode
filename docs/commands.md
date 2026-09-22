# Slash commands

| Command | Purpose |
|---|---|
| `/help` | This list |
| `/exit` | Quit (state saved) |
| `/new` | Fresh session |
| `/models` | Numbered model list; `/models <n>`, `/models <provider> <model>`, `/models install <ollama-model>` |
| `/model` | Alias of `/models` |
| `/export [file]` | Save transcript as markdown |
| `/tools` | List tools available in the current mode |
| `/variants` | Installed Ollama variants + quant guidance |
| `/sessions` | List; `/sessions <id>` resumes (restores its provider/model), `/sessions fork <id>` branches |
| `/status` | Mode, tokens, cost today, cache, RAG, latency |
| `/connect` | Interactive connect: pick provider, paste key, choose model |
| `/doctor` | Health: tools, Ollama, RAG, cache, model advice (`/doctor fix` repairs) |
| `/agent [name]` | Pick builder/planner/reviewer |
| `/init` | Scaffold `.ycode/` + `AGENTS.md` |
| `/editor [path]` | Open `$EDITOR` without leaving the TUI |
| `/plan` `/build` `/chat` `/thinking` | Switch mode (or Tab) |
| `/review [path]` | AI review of `git diff` (`/review --post <pr>` posts inline review comments) || `/test [path] [filter]` | Run project tests, optionally one test by name (`/test --watch [path]` re-runs on save, `/test stop` ends) |
| `/refactor <goal>` | Checkpoint branch + armed instruction |
| `/rag [ingest [path]|<query>]` | Local RAG |
| `/mcps [list|add|remove|tools]` | MCP servers |
| `/skills [list|install|run|export|import]` | Skills (install accepts git URL or owner/repo) |
| `/hooks` | Show configured hooks |
| `/prompts [list|show|run|save|versions]` | Prompt library |
| `/plugins [install|reload]` | Script plugins (install accepts git URL, owner/repo, or local dir) |
| `/store [list|search|install|remove|verify|update]` | Curated skill/plugin store |

Modes: **build** (all tools), **plan** (read-only tools), **chat**/**thinking** (no tools).

Type `@` in the input for path completion — attached files are inlined into
your message (24KB each, 5 max; `@dir` attaches a listing).
