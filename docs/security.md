# Security & privacy

- **Keys**: env vars only (`GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `GH_TOKEN`); never on disk.
- **Audit logs**: every turn/tool call is secret-redacted, NaCl-encrypted (`~/.ycode/keyring.key`, mode 600), append-only per-day files under `~/.ycode/audit/`. Read with `ycode audit [--date YYYY-MM-DD|list]`.
- **Commit guard**: `git commit` via the agent is blocked when secret patterns appear in the diff (override: `args: "force"`).
- **Injection guard**: tool results and URLs are scanned; hits add a SAFETY NOTE so the model treats them as untrusted data.
- **Zero-Data-Leak mode**: set `zero_data_leak: true` in `~/.ycode/config.yaml`.
  - Router hard-blocks all cloud providers and fallbacks; TUI shows a red `[LOCAL ONLY]` badge.
  - Every tool that reaches the network is blocked, checked centrally in the agent loop against one table (`internal/tools/network.go`): `api`, `browser`, `github`, `git` (fetch/push), `db`, `notebook`, `scaffold`, `vscode`, and all `mcp__*` / `plugin__*` tools, which are opaque shell commands. The allow-list shown to the model is filtered from the same table, so it cannot drift.
  - Embeddings/RAG stay local via Ollama.
  - **This is a guardrail against accidents, not a sandbox.** `bash` and `run` execute arbitrary commands, so `bash curl …` still leaves the machine; they are deliberately not blocked because doing so would make the mode useless for real work. Treat the badge as "ycode will not send your data anywhere itself".
- **Sandboxing**: `bash` has a destructive-command denylist, 60s timeout, 32KB output cap, project-dir cwd.
