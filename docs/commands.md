# Slash commands

| Command | Purpose |
|---|---|
| `/help` | This list |
| `/exit` | Quit (state saved) |
| `/new` | Fresh session |
| `/models` | Numbered model list; `/models <n>`, `/models <provider> <model>`, `/models install <ollama-model>` |
| `/model` | Alias of `/models` |
| `/variants` | Installed Ollama variants + quant guidance |
| `/sessions` | List; `/sessions <id>` resumes (restores its provider/model) |
| `/status` | Mode, tokens, cost today, cache, RAG, latency |
| `/connect` | Interactive connect: pick provider, paste key, choose model |
| `/doctor` | Health: tools, Ollama, RAG, cache, model advice |
| `/agent [name]` | Pick builder/planner/reviewer |
| `/init` | Scaffold `.ycode/` + `AGENTS.md` |
| `/editor [path]` | Open `$EDITOR` without leaving the TUI |
| `/plan` `/build` `/chat` `/thinking` | Switch mode (or Tab) |
| `/review [path]` | AI review of `git diff` |
| `/test [path]` | Run project tests |
| `/refactor <goal>` | Checkpoint branch + armed instruction |
| `/rag [ingest [path]|<query>]` | Local RAG |
| `/mcps [list|add|remove|tools]` | MCP servers |
| `/skills [list|install|run|export|import]` | Skills (install accepts git URL or owner/repo) |
| `/hooks` | Show configured hooks |
| `/prompts [list|show|run|save|versions]` | Prompt library |
| `/plugins [install|reload]` | Script plugins (install accepts git URL, owner/repo, or local dir) |

Modes: **build** (all tools), **plan** (read-only tools), **chat**/**thinking** (no tools).
