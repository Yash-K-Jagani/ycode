# Providers

ycode routes through one OpenAI-compatible streaming path plus native Ollama.

| Provider | Key env | Notes |
|---|---|---|
| `ollama` | none (`OLLAMA_HOST`, default `http://localhost:11434`) | Auto-detects installed models; default when no model set |
| `gemini` | `GEMINI_API_KEY` | OpenAI-compat endpoint |
| `openrouter` | `OPENROUTER_API_KEY` | Sends `HTTP-Referer`/`X-Title` |
| `groq` | `GROQ_API_KEY` | OpenAI-compat endpoint |

Keys come from the environment, or from the **OS keyring** if you paste one
during `setup` or `ycode config set <provider>_api_key` — never as plaintext
in `config.yaml`. Each provider's key is stored separately, so a key entered
for one is never sent to another.
`/models` switches (number, `provider model`, or fuzzy name); `/models install <name>` runs `ollama pull`.
On primary failure, configured cloud providers are tried as fallbacks in the
order above, skipping the active one (disabled in zero-data-leak mode).
Per-provider latency is tracked in `~/.ycode/router.json` and shown in `/status`.
