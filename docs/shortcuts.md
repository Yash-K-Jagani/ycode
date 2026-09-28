# Shortcuts

| Key | Action |
|---|---|
| `Ctrl+C` | Stop generation (discards partial output) / quit if idle |
| `Ctrl+D` | Exit |
| `Ctrl+N` | New session |
| `Ctrl+B` | Toggle right sidebar (folder, model, tokens, spend, goal, tasks) |
| `Ctrl+\` | Toggle the file tree |
| `Ctrl+=` / `Ctrl+_` / `Ctrl+]` | Toggle compact mode |
| `Ctrl+O` | Cycle installed Ollama models |
| `Ctrl+P` | Open the command palette (same as typing `/`) |
| `Ctrl+R` | Resume a session (same as `/sessions`) |
| `Tab` / `Shift+Tab` | Cycle mode Plan → Goal → Build → Chat → Thinking |
| `Esc` | Stop generation (ends a goal run) / close dialogs |
| `↑` / `↓` | Input history |
| `Enter` | Send |

`Ctrl+L` is reserved and does nothing yet.

Model switching, palette and session shortcuts are inert while a turn is
streaming, so they cannot retarget the input or open a picker mid-run.
