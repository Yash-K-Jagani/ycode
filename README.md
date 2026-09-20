# ycode
Terminal-first AI coding harness in Go — local Ollama models + free cloud APIs (Gemini, OpenRouter, Groq).

Single binary, private by design, hackable (skills, plugins, MCP, hooks). See `plan.md` for architecture and `docs/` for guides.

## Install

```sh
# Linux/macOS
curl -fsSL https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.sh | bash
# Windows (PowerShell)
irm https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.ps1 | iex
# or from source
go install github.com/Yash-K-Jagani/ycode/cmd/ycode@latest
```

Requires [Ollama](https://ollama.ai) for local models (`ollama pull qwen2.5-coder:3b`). Cloud keys via env: `GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`.

## Use

```sh
ycode              # TUI: Tab switches plan/build/chat/thinking
ycode run "task"   # headless turn (--mode build|plan|chat)
ycode status       # provider health + cost
ycode serve        # local HTTP API (api/openapi.yaml)
ycode batch ...    # offline queue: add/list/run/clear
ycode ci           # diff review + tests (PR-friendly markdown)
ycode audit        # decrypted local audit log
ycode version
```

TUI highlights: `/models` (numbered switching, one-click `install`), `/review`, `/test`, `/refactor` (checkpoint branch), `/rag ingest`, `/doctor`, Ctrl+O model cycling. Full list: `docs/commands.md`.

## Privacy

Env-only keys, encrypted redacted audit logs, secret-blocking commits, injection guards, and `zero_data_leak: true` for local-only operation. Details: `docs/security.md`.

## Milestones

M1 Core chat · M2 agent loop/tools · M3 git/GitHub/MCP/hooks/skills/browser · M4 RAG/cache/fallbacks/security/testgen · M5 plugins/prompts/API/SDK/batch/CI · M6 audit/ZDL hardening + docs. This tree implements M1–M6.
