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
| `Enter` | Send |
| `Alt+Enter` / `Ctrl+J` | New line in the prompt (multi-line questions) |
| `↑` / `↓` | Prompt history, on the first / last line of the prompt; cursor movement inside it |
| `Ctrl+U` | Clear the current line |
| `Ctrl+W` / `Alt+Backspace` | Delete the word before the cursor |
| `Ctrl+A` / `Ctrl+E` | Start / end of line |
| `Ctrl+N` / `Ctrl+P` | Next / previous line of a multi-line prompt |
| `PageUp` / `PageDown` | Scroll the transcript by half a page |
| `Alt+↑` / `Alt+↓` | Scroll the transcript by one line |
| `Ctrl+↑` / `Ctrl+↓` | Scroll the transcript by half a page |
| `Home` / `End` | Jump to the top / the latest output |

`Ctrl+L` is reserved and does nothing yet.

Model switching, palette and session shortcuts are inert while a turn is
streaming, so they cannot retarget the input or open a picker mid-run.

## Scrolling the transcript

The prompt box has the keyboard. Every other key that moves the transcript is
one that cannot occur in ordinary text, which is deliberate: the viewport
component ships with a pager keymap binding space, `b`, and `u`/`d`/`j`/`k`/
`h`/`l`, and those used to be live while typing, so every space in a question
paged the transcript.

While the output is streaming the view follows the bottom. Scroll up and it
stops following, so you can read back through an answer that is still being
written; a `↓ N new lines · End to jump` pill appears while you are behind. `End`
returns to the latest output.
