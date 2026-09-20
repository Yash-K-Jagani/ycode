# Security & privacy

- **Keys**: env vars only (`GEMINI_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `GH_TOKEN`); never on disk.
- **Audit logs**: every turn/tool call is secret-redacted, NaCl-encrypted (`~/.ycode/keyring.key`, mode 600), append-only per-day files under `~/.ycode/audit/`. Read with `ycode audit [--date YYYY-MM-DD|list]`.
- **Commit guard**: `git commit` via the agent is blocked when secret patterns appear in the diff (override: `args: "force"`).
- **Injection guard**: tool results and URLs are scanned; hits add a SAFETY NOTE so the model treats them as untrusted data.
- **Zero-Data-Leak mode**: set `zero_data_leak: true` in `~/.ycode/config.yaml`.
  - Router hard-blocks all cloud providers and fallbacks; TUI shows a red `[LOCAL ONLY]` badge.
  - `browser` and `github` tools are blocked; MCP/plugins (local processes) still work.
  - Embeddings/RAG stay local via Ollama.
- **Sandboxing**: `bash` has a destructive-command denylist, 60s timeout, 32KB output cap, project-dir cwd.
