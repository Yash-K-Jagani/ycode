# Plugins

Script plugins live in `~/.ycode/plugins/<name>/plugin.json`:

```json
{
  "name": "hello",
  "description": "says hi",
  "command": "echo hi",
  "args": [],
  "schema": {"type": "object"}
}
```

- `command` runs in the plugin dir (shell on Linux/macOS, PowerShell on Windows).
- Model input arrives in `$YCODE_PLUGIN_ARGS` (JSON). stdout is the result (32KB cap, 90s timeout).
- Exposed as `plugin__<name>` tools in build mode. Blocked in read-only mode.
- `/plugins` lists, `/plugins reload` rescans `~/.ycode/plugins` (fsnotify watcher API supports live hot-reload).

See also: MCP servers (`/mcps`) for richer integrations.
