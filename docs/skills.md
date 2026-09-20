# Skills

A skill is a folder with a `SKILL.md` (leading `# Name` + `Description:` line, then instructions):

```md
# reviewer
Description: reviews code strictly

Checklist: ...
```

- Stored in `~/.ycode/skills/<name>/`.
- `/skills install <git-url|local-dir>` — clone (github/gitlab only) or copy in.
- `/skills run <name>` — injects instructions into your next turn.
- `/skills export <name> <file.zip>` / `/skills import <file.zip>` — share without a server.
- Agents can also read skill files directly with the `read` tool.
