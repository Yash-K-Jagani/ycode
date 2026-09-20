# Providers

ycode routes through one OpenAI-compatible streaming path plus native Ollama.

| Provider | Key env | Notes |
|---|---|---|
| `ollama` | none (`OLLAMA_HOST`, default `http://localhost:11434`) | Auto-detects installed models; default when no model set |
| `gemini` | `GEMINI_API_KEY` | OpenAI-compat endpoint |
| `openrouter` | `OPENROUTER_API_KEY` | Sends `HTTP-Referer`/`X-Title` |
| `groq` | `GROQ_API_KEY` | OpenAI-compat endpoint |

Keys are read from the environment only — never written to disk.
`/models` switches (number, `provider model`, or fuzzy name); `/models install <name>` runs `ollama pull`.
On primary failure, configured cloud providers are tried as fallbacks (disabled in zero-data-leak mode).
Per-provider latency is tracked in `~/.ycode/router.json` and shown in `/status`.
