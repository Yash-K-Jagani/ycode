package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/agents"
	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/mcp"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/prompts"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/internal/rag"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/store"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

type slashHandler func(ctx context.Context, m *Model, args string) (string, tea.Cmd)

type editorDoneMsg struct{ err error }

var ErrQuit = fmt.Errorf("quit")

type modelEntry struct {
	Provider  string
	Model     string
	Installed bool
}

func listEntries(ctx context.Context, m *Model) ([]modelEntry, string) {
	var out []modelEntry
	note := ""
	seen := map[string]bool{}
	if ms, err := ollama.New(m.cfg.OllamaHost).ListModels(ctx); err == nil {
		for _, mi := range ms {
			out = append(out, modelEntry{Provider: "ollama", Model: mi.ID, Installed: true})
			seen["ollama\x00"+mi.ID] = true
		}
	} else {
		note = "ollama unreachable (" + err.Error() + "); showing cloud catalog only"
	}
	for _, c := range registry.Catalog() {
		if seen[c.Provider+"\x00"+c.ID] {
			continue
		}
		out = append(out, modelEntry{Provider: c.Provider, Model: c.ID})
	}
	return out, note
}

func applyModel(m *Model, provider, model string) string {
	m.cfg.ActiveProvider = provider
	m.cfg.ActiveModel = model
	m.router.Update(m.cfg)
	_ = m.cfg.Save()
	if m.sess != nil {
		m.sess.Provider = provider
		m.sess.Model = model
		_ = m.sess.Save()
	}
	warn := ""
	if provider != "ollama" && !hasKey(m, provider) {
		warn = fmt.Sprintf(" — warning: no API key set for %s (see /connect)", provider)
	}
	return fmt.Sprintf("Switched to %s / %s%s", provider, model, warn)
}

func hasKey(m *Model, provider string) bool {
	switch provider {
	case "gemini":
		return m.cfg.GeminiAPIKey != ""
	case "openrouter":
		return m.cfg.OpenRouterKey != ""
	case "groq":
		return m.cfg.GroqKey != ""
	}
	return true
}

func modelsHelp(m *Model, entries []modelEntry, note string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Models (current: %s / %s)\n", m.cfg.ActiveProvider, m.cfg.ActiveModel)
	for i, e := range entries {
		tag := ""
		if e.Installed {
			tag = " [installed]"
		}
		fmt.Fprintf(&b, "%d. %s / %s%s\n", i+1, e.Provider, e.Model, tag)
	}
	if note != "" {
		b.WriteString(note + "\n")
	}
	b.WriteString("Use: /models <number>  |  /models <provider> <model>  |  /models <model-name>")
	return b.String()
}

func selectModel(m *Model, entries []modelEntry, args string) string {
	a := strings.TrimSpace(args)
	if n, err := strconv.Atoi(a); err == nil {
		if n < 1 || n > len(entries) {
			return fmt.Sprintf("number out of range (1-%d)", len(entries))
		}
		e := entries[n-1]
		return applyModel(m, e.Provider, e.Model)
	}
	parts := strings.Fields(a)
	if len(parts) == 2 {
		return applyModel(m, parts[0], parts[1])
	}
	// single token: exact installed match → exact catalog match → contains match
	for _, e := range entries {
		if e.Installed && strings.EqualFold(e.Model, a) {
			return applyModel(m, e.Provider, e.Model)
		}
	}
	for _, e := range entries {
		if strings.EqualFold(e.Model, a) {
			return applyModel(m, e.Provider, e.Model)
		}
	}
	var fuzzy []modelEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Model), strings.ToLower(a)) {
			fuzzy = append(fuzzy, e)
		}
	}
	if len(fuzzy) == 1 {
		return applyModel(m, fuzzy[0].Provider, fuzzy[0].Model)
	}
	if len(fuzzy) > 1 {
		var b strings.Builder
		b.WriteString("Multiple matches:\n")
		for _, e := range fuzzy {
			fmt.Fprintf(&b, "- %s / %s\n", e.Provider, e.Model)
		}
		return b.String()
	}
	return "No model matches " + strconv.Quote(a)
}

func (m *Model) setMode(md modes.Mode) string {
	m.mode = md
	return ""
}

func slashRegistry() map[string]slashHandler {
	return map[string]slashHandler{
		"/help": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			return "Commands: /help /exit /new /models /sessions /status /connect /agent /init /editor /doctor /export /tools\nIntegrations: /review [path] · /mcps · /skills · /hooks\nIntelligence: /rag · /test [path] · /refactor <instruction>\nEcosystem: /prompts · /plugins · /store · /variants · /models install <name>\nModes: /plan /build /chat /thinking (or Tab). Stop output: Ctrl+C / Esc.", nil
		},
		"/tools": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			names := modes.AllowedTools(m.mode, append(m.mcpNames, m.pluginNames...)...)
			if len(names) == 0 {
				return "No tools in " + string(m.mode) + " mode — switch to /build (all) or /plan (read-only).", nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Tools available in %s mode (%d):\n", m.mode, len(names))
			for _, n := range names {
				desc := ""
				if t, ok := m.toolreg.Get(n); ok {
					desc = t.Description()
					if i := strings.IndexByte(desc, '.'); i >= 0 {
						desc = desc[:i]
					}
				}
				fmt.Fprintf(&b, "- %s: %s\n", n, desc)
			}
			return b.String(), nil
		},
		"/doctor": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			if strings.TrimSpace(args) == "fix" {
				return doctorFix(m), nil
			}
			return doctorReport(ctx, m), nil
		},
		"/exit": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) { panic(ErrQuit) },
		"/new": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			m.sess = sessions.New(m.cfg.ActiveProvider, m.cfg.ActiveModel)
			_ = m.sess.Save()
			m.msgs = nil
			m.vp.SetContent("")
			return "New session started.", nil
		},
		"/sessions": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			if f := strings.Fields(args); len(f) == 2 && f[0] == "fork" {
				fork, err := sessions.Fork(f[1])
				if err != nil {
					return "fork failed: " + err.Error(), nil
				}
				m.sess = fork
				m.renderAll()
				return "Forked as " + fork.Title + " (" + fork.ID + ") — original untouched.", nil
			}
			if f := strings.Fields(args); len(f) >= 1 && f[0] == "search" {
				q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "search")))
				list, err := sessions.List()
				if err != nil {
					return "sessions: " + err.Error(), nil
				}
				var b strings.Builder
				n := 0
				for _, s := range list {
					hay := strings.ToLower(s.ID + " " + s.Title + " " + s.Provider + " " + s.Model)
					if q != "" && !strings.Contains(hay, q) {
						continue
					}
					fmt.Fprintf(&b, "%s — %s (%s/%s) %d msgs\n", s.ID, s.Title, s.Provider, s.Model, len(s.Messages))
					if n++; n >= 15 {
						break
					}
				}
				if n == 0 {
					return "No sessions match " + strconv.Quote(q), nil
				}
				return b.String(), nil
			}
			if f := strings.Fields(args); len(f) >= 1 && f[0] == "prune" {
				keep := 30
				confirmed := false
				for _, tok := range f[1:] {
					if tok == "--yes" {
						confirmed = true
					} else if n, err := strconv.Atoi(tok); err == nil && n >= 0 {
						keep = n
					}
				}
				list, err := sessions.List()
				if err != nil {
					return "sessions: " + err.Error(), nil
				}
				ids := sessions.PruneIDs(list, keep)
				if len(ids) == 0 {
					return fmt.Sprintf("%d sessions, nothing to prune (keeping %d).", len(list), keep), nil
				}
				if !confirmed {
					return fmt.Sprintf("Would delete %d oldest sessions (keeping %d). Re-run with --yes.", len(ids), keep), nil
				}
				n := 0
				for _, id := range ids {
					if sessions.Delete(id) == nil {
						n++
					}
				}
				return fmt.Sprintf("Pruned %d sessions, %d kept.", n, len(list)-len(ids)), nil
			}
			if args != "" {
				s, err := sessions.Load(strings.TrimSpace(args))
				if err != nil {
					return "session not found: " + args, nil
				}
				m.sess = s
				if s.Provider != "" {
					m.cfg.ActiveProvider = s.Provider
				}
				if s.Model != "" {
					m.cfg.ActiveModel = s.Model
				}
				m.router.Update(m.cfg)
				_ = m.cfg.Save()
				m.renderAll()
				return "Resumed " + s.Title + " (" + m.cfg.ActiveProvider + "/" + m.cfg.ActiveModel + ")", nil
			}
			list, err := sessions.List()
			if err != nil || len(list) == 0 {
				return "No saved sessions.", nil
			}
			var b strings.Builder
			for i, s := range list {
				if i >= 10 {
					break
				}
				fmt.Fprintf(&b, "%s — %s (%s/%s) %d msgs\n", s.ID, s.Title, s.Provider, s.Model, len(s.Messages))
			}
			b.WriteString("Use: /sessions <id> to resume, /sessions fork <id>, /sessions search <q>, /sessions prune [N] [--yes]")
			return b.String(), nil
		},
		"/export": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			md := sessions.Export(m.sess)
			target := strings.TrimSpace(args)
			if target == "" {
				target = "session-" + m.sess.ID + ".md"
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(m.workdir, target)
			}
			if err := os.WriteFile(target, []byte(md), 0o644); err != nil {
				return "export failed: " + err.Error(), nil
			}
			return fmt.Sprintf("Exported %d messages to %s", len(m.sess.Messages), target), nil
		},
		"/models": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) == 2 && f[0] == "install" {
				m.appendSys("Pulling " + f[1] + " (may take a while)…")
				return "", modelsPullCmd(f[1])
			}
			if len(f) >= 2 && f[0] == "import" {
				m.appendSys("Importing " + f[1] + " into Ollama (may take a while)…")
				return "", modelsImportCmd(m.workdir, f[1], restOrEmpty(f, 2))
			}
			if len(f) == 2 && f[0] == "info" {
				raw, _ := json.Marshal(map[string]string{"action": "info", "path": f[1]})
				out, err := (&tools.ModelsTool{Workdir: m.workdir}).Run(ctx, raw)
				if err != nil {
					return "gguf info: " + err.Error(), nil
				}
				return out, nil
			}
			entries, note := listEntries(ctx, m)
			if args == "" {
				return modelsHelp(m, entries, note) + "\n/models install <ollama-model> — one-click install\n/models import <file.gguf> [name] — register local GGUF\n/models info <file.gguf> — inspect", nil
			}
			return selectModel(m, entries, args), nil
		},
		"/model": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			entries, note := listEntries(ctx, m)
			if args == "" {
				return modelsHelp(m, entries, note), nil
			}
			return selectModel(m, entries, args), nil
		},
		"/status": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			toks := sessions.EstimateTokens(m.sess.Messages)
			p, c, usd := m.tracker.Today()
			h, mi, size := m.semCache.Stats()
			ragInfo := "rag: no index (run /rag ingest)"
			if idx, ok := ragLoad(m); ok {
				ragInfo = fmt.Sprintf("rag: %d chunks (built %s)", len(idx.Chunks), idx.BuiltAt.Format("2006-01-02 15:04"))
			}
			return fmt.Sprintf("mode=%s agent=%s store=%s\nprovider=%s model=%s msgs=%d ~tokens=%d\ntoday: %d prompt + %d completion tokens · $%.4f\ncache: %d hits / %d misses (%d items)\ntools: %d/%d agentic turns used tools (%d calls)\n%s\nlatency:\n%s",
				m.mode, m.agent.Name, sessions.Backend(), m.cfg.ActiveProvider, m.cfg.ActiveModel, len(m.sess.Messages), toks, p, c, usd, h, mi, size, m.toolTurns, m.toolModeTurns, m.toolCallsTotal, ragInfo, m.router.Stats().Summary()), nil
		},
		"/connect": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			m.conn = newConnect()
			return "", textinput.Blink
		},
		"/plan":  func(ctx context.Context, m *Model, args string) (string, tea.Cmd) { return m.setMode(modes.Plan), nil },
		"/build": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) { return m.setMode(modes.Build), nil },
		"/chat":  func(ctx context.Context, m *Model, args string) (string, tea.Cmd) { return m.setMode(modes.Chat), nil },
		"/thinking": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			return m.setMode(modes.Thinking), nil
		},
		"/agent": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			if args == "" {
				var b strings.Builder
				b.WriteString("Agents (current: " + m.agent.Name + ")\n")
				for _, a := range agents.All() {
					fmt.Fprintf(&b, "- %s: %s\n", a.Name, a.Description)
				}
				b.WriteString("Use: /agent <name>")
				return b.String(), nil
			}
			a, ok := agents.Get(strings.TrimSpace(args))
			if !ok {
				return "unknown agent (try /agent with no args)", nil
			}
			m.agent = a
			return "Agent: " + a.Name + " — " + a.Description, nil
		},
		"/init": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			return m.initProject(), nil
		},
		"/editor": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			ed := os.Getenv("EDITOR")
			if ed == "" {
				ed = "notepad"
			}
			target := strings.TrimSpace(args)
			if target == "" {
				target = m.workdir
			}
			cmd := exec.Command(ed, target)
			return "Opened " + ed + " " + target, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return editorDoneMsg{err}
			})
		},
		"/review": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			spec := strings.TrimSpace(args)
			postPR := 0
			if f := strings.Fields(spec); len(f) >= 2 && f[0] == "--post" {
				n, err := strconv.Atoi(f[1])
				if err != nil || n <= 0 {
					return "Usage: /review [--post <pr>] [path]", nil
				}
				postPR = n
				spec = strings.TrimSpace(strings.TrimPrefix(spec, "--post "+f[1]))
			}
			diffArgs := map[string]string{"action": "diff"}
			if spec != "" {
				diffArgs["args"] = spec
			}
			raw, _ := json.Marshal(diffArgs)
			out, err := (&tools.GitTool{Workdir: m.workdir}).Run(ctx, raw)
			if err != nil {
				return "review failed: " + err.Error(), nil
			}
			if strings.TrimSpace(out) == "" || out == "(clean)" {
				return "Working tree clean — nothing to review.", nil
			}
			if len(out) > 30*1024 {
				out = out[:30*1024] + "\n…(diff truncated)"
			}
			if postPR > 0 {
				owner, repo, err := tools.GitHubRepo(m.workdir)
				if err != nil {
					return "review --post: " + err.Error(), nil
				}
				m.appendSys(fmt.Sprintf("Reviewing diff for PR #%d (%s/%s)…", postPR, owner, repo))
				return "", m.reviewPostCmd(out, owner+"/"+repo, postPR)
			}
			m.appendSys("Reviewing diff (" + fmt.Sprintf("%d bytes", len(out)) + ")…")
			return "", m.reviewCmd(out)
		},
		"/mcps": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) == 0 || f[0] == "list" {
				names := m.mcpMgr.Names()
				if len(names) == 0 {
					return "No MCP servers. Add one: /mcps add <name> <command> [args...]", nil
				}
				return "MCP servers: " + strings.Join(names, ", ") + fmt.Sprintf(" (%d tools loaded)", len(m.mcpNames)) + "\n/mcps tools — (re)load tools · /mcps remove <name>", nil
			}
			switch f[0] {
			case "add":
				if len(f) < 3 {
					return "Usage: /mcps add <name> <command> [args...]", nil
				}
				if err := m.mcpMgr.Add(f[1], mcp.ServerConfig{Command: f[2], Args: f[3:]}); err != nil {
					return "mcp add failed: " + err.Error(), nil
				}
				return "Added MCP server " + f[1] + ". Run /mcps tools to load its tools.", nil
			case "remove":
				if len(f) < 2 {
					return "Usage: /mcps remove <name>", nil
				}
				if err := m.mcpMgr.Remove(f[1]); err != nil {
					return "mcp remove failed: " + err.Error(), nil
				}
				return "Removed MCP server " + f[1] + ".", nil
			case "tools":
				m.appendSys("Loading MCP tools…")
				return "", m.loadMCPsCmd()
			default:
				return "Usage: /mcps [list|add|remove|tools]", nil
			}
		},
		"/skills": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) == 0 || f[0] == "list" {
				list, err := m.skillMgr.List()
				if err != nil {
					return "skills error: " + err.Error(), nil
				}
				if len(list) == 0 {
					return "No skills installed. /skills install <git-url|local-dir>", nil
				}
				var b strings.Builder
				for _, s := range list {
					fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
				}
				b.WriteString("/skills run <name> — inject into next turn")
				return b.String(), nil
			}
			switch f[0] {
			case "install":
				if len(f) < 2 {
					return "Usage: /skills install <git-url|local-dir>", nil
				}
				s, err := m.skillMgr.Install(strings.TrimSpace(strings.TrimPrefix(args, "install")))
				if err != nil {
					return "skill install failed: " + err.Error(), nil
				}
				return "Installed skill: " + s.Name, nil
			case "run":
				if len(f) < 2 {
					return "Usage: /skills run <name>", nil
				}
				s, body, err := m.skillMgr.Get(f[1])
				if err != nil {
					return "skill error: " + err.Error(), nil
				}
				m.pendingSkill = "Skill [" + s.Name + "]:\n" + body
				return "Skill '" + s.Name + "' armed — its instructions apply to your next message.", nil
			case "export":
				if len(f) < 3 {
					return "Usage: /skills export <name> <file.zip>", nil
				}
				if err := m.skillMgr.Export(f[1], f[2]); err != nil {
					return "export failed: " + err.Error(), nil
				}
				return "Exported skill '" + f[1] + "' to " + f[2], nil
			case "import":
				if len(f) < 2 {
					return "Usage: /skills import <file.zip>", nil
				}
				s, err := m.skillMgr.Import(f[1])
				if err != nil {
					return "import failed: " + err.Error(), nil
				}
				return "Imported skill: " + s.Name, nil
			default:
				return "Usage: /skills [list|install|run|export|import]", nil
			}
		},
		"/hooks": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			events := []hooks.Event{hooks.OnRequest, hooks.OnResponse, hooks.PreTool, hooks.PostTool, hooks.OnError}
			var b strings.Builder
			any := false
			for _, e := range events {
				for _, en := range m.hookset.Points[e] {
					fmt.Fprintf(&b, "%s: %s\n", e, en.Command)
					any = true
				}
			}
			if !any {
				return "No hooks configured. Add ~/.ycode/hooks.yaml or <project>/.ycode/hooks.yaml:\npre_tool:\n  - command: \"echo $YCODE_TOOL\"", nil
			}
			return b.String(), nil
		},
		"/rag": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) == 0 {
				if idx, ok := ragLoad(m); ok {
					return fmt.Sprintf("rag: %d chunks, built %s. /rag ingest [path] to rebuild, /rag <query> to search.", len(idx.Chunks), idx.BuiltAt.Format("2006-01-02 15:04")), nil
				}
				return "rag: no index. Run /rag ingest [path].", nil
			}
			if f[0] == "ingest" {
				root := m.workdir
				if len(f) > 1 {
					root = f[1]
				}
				m.appendSys("RAG ingest started for " + root + " (embedding, may take a while)…")
				return "", m.ragIngestCmd(root)
			}
			idx, ok := ragLoad(m)
			if !ok {
				return "rag: no index. Run /rag ingest first.", nil
			}
			qv, err := m.embedder.Embed(ctx, []string{args})
			if err != nil {
				return "rag query failed: " + err.Error(), nil
			}
			chunks := rag.Query(idx, qv[0], 4)
			if len(chunks) == 0 {
				return "rag: no matches.", nil
			}
			var b strings.Builder
			for _, c := range chunks {
				fmt.Fprintf(&b, "--- %s ---\n%s\n\n", c.Path, truncateForRag(c.Text))
			}
			return b.String(), nil
		},
		"/test": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			for _, tok := range f {
				if tok == "stop" {
					was := m.watching()
					m.stopWatch()
					if was {
						return "Test watch stopped.", nil
					}
					return "No watch running. Usage: /test [--watch] [path] [filter] | /test stop", nil
				}
			}
			watch := false
			var rest []string
			for _, tok := range f {
				if tok == "--watch" {
					watch = true
					continue
				}
				rest = append(rest, tok)
			}
			target := m.workdir
			payload := map[string]string{}
			if len(rest) > 0 {
				target = rest[0]
				payload["path"] = target
			} else {
				payload["path"] = target
			}
			if len(rest) > 1 {
				payload["run"] = strings.Join(rest[1:], " ")
			}
			if watch {
				m.startWatch(target)
				return "Watching " + target + " — tests re-run on save (/test stop to end).", nil
			}
			raw, _ := json.Marshal(payload)
			out, err := (&tools.TestGenTool{Workdir: m.workdir}).Run(ctx, raw)
			if len(out) > 6000 {
				out = out[:6000] + "\n…(truncated)"
			}
			if err != nil {
				return "Tests FAILED:\n" + out + "\nTip: switch to /build and ask the model to fix them.", nil
			}
			return "Tests passed:\n" + out, nil
		},
		"/refactor": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			goal := strings.TrimSpace(args)
			if goal == "" {
				return "Usage: /refactor <what to refactor> [force]", nil
			}
			force := strings.HasSuffix(goal, " force")
			goal = strings.TrimSuffix(goal, " force")
			raw, _ := json.Marshal(map[string]string{"action": "status"})
			st, _ := (&tools.GitTool{Workdir: m.workdir}).Run(ctx, raw)
			if strings.TrimSpace(st) != "" && st != "(clean)" && !force {
				return "Working tree is dirty. Commit first or append 'force' — refactor runs on a checkpoint branch but dirty files complicate revert.", nil
			}
			branch := fmt.Sprintf("ycode/refactor-%d", time.Now().Unix())
			raw2, _ := json.Marshal(map[string]string{"action": "create_branch", "args": branch})
			if _, err := (&tools.GitTool{Workdir: m.workdir}).Run(ctx, raw2); err != nil {
				return "refactor: could not create checkpoint branch: " + err.Error(), nil
			}
			m.setMode(modes.Build)
			m.ta.SetValue("Refactor (" + branch + " checkpoint created): " + goal + "\nPlan: explore, edit, run /test-equivalent via testgen, summarize the diff.")
			return "Checkpoint branch " + branch + " created. Instruction armed below — press Enter to run (revert with: git checkout main -- . / git branch -D " + branch + ").", nil
		},
		"/prompts": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			pm := prompts.NewManager()
			f := strings.Fields(args)
			if len(f) == 0 || f[0] == "list" {
				return "Prompts:\n- " + strings.Join(pm.List(), "\n- ") + "\n/prompts show|run <name> [input] · /prompts save <name> <text> · /prompts versions <name>", nil
			}
			switch f[0] {
			case "show":
				if len(f) < 2 {
					return "Usage: /prompts show <name>", nil
				}
				body, err := pm.Show(f[1])
				if err != nil {
					return err.Error(), nil
				}
				return body, nil
			case "run":
				if len(f) < 2 {
					return "Usage: /prompts run <name> [input]", nil
				}
				tmpl, err := pm.Show(f[1])
				if err != nil {
					return err.Error(), nil
				}
				input := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "run "+f[1]))
				m.pendingSkill = "Prompt [" + f[1] + "]:\n" + prompts.Render(tmpl, input)
				return "Prompt '" + f[1] + "' armed — applies to your next message.", nil
			case "save":
				if len(f) < 3 {
					return "Usage: /prompts save <name> <text with {{input}}>", nil
				}
				body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "save "+f[1]))
				if err := pm.Save(f[1], body); err != nil {
					return "save failed: " + err.Error(), nil
				}
				return "Saved prompt '" + f[1] + "' (previous version snapshotted).", nil
			case "versions":
				if len(f) < 2 {
					return "Usage: /prompts versions <name>", nil
				}
				vs := pm.Versions(f[1])
				if len(vs) == 0 {
					return "No snapshots for '" + f[1] + "'.", nil
				}
				return "Snapshots:\n- " + strings.Join(vs, "\n- "), nil
			default:
				return "Usage: /prompts [list|show|run|save|versions]", nil
			}
		},
		"/plugins": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) > 0 && f[0] == "install" {
				src := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "install"))
				if src == "" {
					return "Usage: /plugins install <git-url|owner/repo|local-dir>", nil
				}
				name, err := m.pluginLoader.Install(src)
				if err != nil {
					return "plugin install failed: " + err.Error(), nil
				}
				m.registerPluginTools()
				return "Installed plugin: " + name + " (tools registered)", nil
			}
			if len(f) > 0 && f[0] == "reload" {
				m.registerPluginTools()
			}
			names := m.pluginLoader.Describe()
			if len(names) == 0 {
				return "No plugins. Add ~/.ycode/plugins/<name>/plugin.json {name, description, command} or install one: /plugins install <git-url|owner/repo|local-dir|.wasm>. Then /plugins reload.", nil
			}
			return fmt.Sprintf("Plugins (%d):\n- %s\nUsable in build mode as tools.", len(names), strings.Join(names, "\n- ")), nil
		},
		"/store": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			f := strings.Fields(args)
			if len(f) == 0 || f[0] == "list" {
				idx, err := store.Load()
				if err != nil {
					return "store: " + err.Error() + " — run /store update first", nil
				}
				return storeList(idx.Search("")), nil
			}
			switch f[0] {
			case "update":
				url := ""
				if len(f) > 1 {
					url = f[1]
				}
				m.appendSys("Updating store index…")
				return "", m.storeUpdateCmd(url)
			case "search":
				idx, err := store.Load()
				if err != nil {
					return "store: " + err.Error(), nil
				}
				q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), "search"))
				res := idx.Search(q)
				if len(res) == 0 {
					return "No store entries match " + strconv.Quote(q), nil
				}
				return storeList(res), nil
			case "install":
				if len(f) < 2 {
					return "Usage: /store install <name> (see /store list)", nil
				}
				idx, err := store.Load()
				if err != nil {
					return "store: " + err.Error(), nil
				}
				e, ok := idx.Get(f[1])
				if !ok {
					return "No store entry " + strconv.Quote(f[1]), nil
				}
				m.appendSys(fmt.Sprintf("Installing %s (%s) from %s …", e.Name, e.Kind, displaySource(e)))
				return "", m.storeInstallCmd(e)
			case "remove":
				if len(f) < 2 {
					return "Usage: /store remove <name>", nil
				}
				what, err := store.Remove(f[1], m.skillMgr, m.pluginLoader)
				if err != nil {
					return "store remove: " + err.Error(), nil
				}
				m.registerPluginTools()
				return "Removed " + what, nil
			case "verify":
				if len(f) > 1 {
					msg, err := store.Verify(f[1], m.skillMgr, m.pluginLoader)
					if err != nil {
						return f[1] + ": " + err.Error(), nil
					}
					return f[1] + ": " + msg, nil
				}
				return strings.Join(store.VerifyAll(m.skillMgr, m.pluginLoader), "\n"), nil
			default:
				return "Usage: /store [list|search|install|remove|verify|update]", nil
			}
		},
		"/variants": func(ctx context.Context, m *Model, args string) (string, tea.Cmd) {
			ms, err := ollama.New(m.cfg.OllamaHost).ListModels(ctx)
			if err != nil {
				return "variants: " + err.Error(), nil
			}
			var b strings.Builder
			b.WriteString("Installed variants (current marked *):\n")
			for _, mi := range ms {
				mark := " "
				if mi.ID == m.cfg.ActiveModel && m.cfg.ActiveProvider == "ollama" {
					mark = "*"
				}
				fmt.Fprintf(&b, "%s %s\n", mark, mi.ID)
			}
			b.WriteString("\nTip (~4GB VRAM): prefer Q4_K_M 7B-or-smaller, e.g. qwen2.5-coder:7b-instruct-q4_K_M.\nSwap with /models <number>; install with /models install <name>.")
			return b.String(), nil
		},
	}
}

func doctorFix(m *Model) string {
	var b strings.Builder
	b.WriteString("doctor fix:\n")
	m.semCache.Clear()
	b.WriteString("- semantic cache cleared\n")
	n := batch.Load().Clear(true)
	fmt.Fprintf(&b, "- batch: cleared %d finished jobs\n", n)
	if err := m.cfg.Save(); err != nil {
		fmt.Fprintf(&b, "- config save FAILED: %v\n", err)
	} else {
		b.WriteString("- config re-saved ok\n")
	}
	b.WriteString("Re-run /doctor to verify.")
	return b.String()
}

func doctorReport(ctx context.Context, m *Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mode=%s agent=%s\nprovider=%s model=%s\n", m.mode, m.agent.Name, m.cfg.ActiveProvider, m.cfg.ActiveModel)
	allowed := modes.AllowedTools(m.mode, append(m.mcpNames, m.pluginNames...)...)
	if len(allowed) == 0 {
		b.WriteString("tools: NONE in this mode — switch to /build (all) or /plan (read-only) to use file tools\n")
	} else {
		fmt.Fprintf(&b, "tools (%d): %s\n", len(allowed), strings.Join(allowed, ", "))
	}
	if ms, err := ollama.New(m.cfg.OllamaHost).ListModels(ctx); err != nil {
		b.WriteString("ollama: UNREACHABLE (" + err.Error() + ")\n")
	} else {
		fmt.Fprintf(&b, "ollama: ok (%d models)\n", len(ms))
	}
	if _, ok := ragLoad(m); ok {
		b.WriteString("rag: indexed\n")
	} else {
		b.WriteString("rag: no index (/rag ingest)\n")
	}
	h, mi, size := m.semCache.Stats()
	fmt.Fprintf(&b, "cache: %d hits / %d misses (%d items)\n", h, mi, size)
	if strings.Contains(m.cfg.ActiveModel, "1.5b") || strings.Contains(m.cfg.ActiveModel, "1b") {
		b.WriteString("advice: tiny models often mangle tool calls — prefer a 3b+ coder model (/models) for build mode\n")
	}
	return b.String()
}

var fileTaskRe = regexp.MustCompile(`(?i)\b(read|edit|write|create|fix|update|delete|open|list|show|find|search|run|test|clone|review|refactor)\b.*(file|folder|dir|code|repo|test|diff|path|\.\w{1,5}\b)|(\bfile\b|\bfolder\b|\bdirectory\b|\brepo\b)`)

// looksLikeWriteTask matches requests to put content into a file.
var writeTaskRe = regexp.MustCompile(`(?i)\b(write|create|save|put|add|insert|generate)\b.{0,50}\b(files?|notes?|scripts?|into|in|to|config|readme)\b|\bcreate\s+a\s+file\b`)

// hasCodeFence reports pasted code content (the write-instead-of-acting dodge).
func hasCodeFence(s string) bool { return strings.Contains(s, "```") }

func looksLikeWriteTask(s string) bool { return writeTaskRe.MatchString(s) }

var resultRoleplayRe = regexp.MustCompile(`(?i)<(write_result|tool_result:[a-z_]+)>`)

// hasResultRoleplay reports a <tool_result>/write_result block the model
// wrote itself without ever emitting the matching <tool:> call.
func hasResultRoleplay(s string) bool { return resultRoleplayRe.MatchString(s) }

func ragLoad(m *Model) (rag.Index, bool) {
	if m.ragIdx != nil && len(m.ragIdx.Chunks) > 0 {
		return *m.ragIdx, true
	}
	idx, ok := rag.Load(m.workdir)
	if ok {
		m.ragIdx = &idx
	}
	return idx, ok
}

func truncateForRag(s string) string {
	if len(s) > 1200 {
		return s[:1200] + "\n…"
	}
	return s
}
