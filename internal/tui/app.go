package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/agent"
	"github.com/Yash-K-Jagani/ycode/internal/agents"
	"github.com/Yash-K-Jagani/ycode/internal/audit"
	"github.com/Yash-K-Jagani/ycode/internal/cache"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	yctx "github.com/Yash-K-Jagani/ycode/internal/context"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/embed"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/mcp"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
	"github.com/Yash-K-Jagani/ycode/internal/rag"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
	"github.com/Yash-K-Jagani/ycode/internal/store"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
	"github.com/Yash-K-Jagani/ycode/internal/webhooks"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Model struct {
	cfg     config.Config
	router  *router.Router
	sess    *sessions.Session
	ta      textarea.Model
	vp      viewport.Model
	keys    KeyMap
	th      theme.Theme
	msgs    []string
	busy    bool
	stream  strings.Builder
	prog    *tea.Program
	startup string

	mode    modes.Mode
	agent   agents.Agent
	toolreg *tools.Registry
	workdir string
	tracker *cost.Tracker

	mcpMgr       *mcp.Manager
	mcpNames     []string
	hookset      *hooks.Hooks
	skillMgr     *skills.Manager
	pendingSkill string

	embedder *embed.Client
	ragIdx   *rag.Index
	ragOff   bool
	semCache *cache.Cache

	pluginLoader *plugins.Loader
	pluginNames  []string

	palIdx    int
	palHide   bool
	lastInput string
	atIdx     int
	atHide    bool

	conn       *connectFlow
	winW, winH int

	watchCancel context.CancelFunc
	watchTarget string

	turnCancel context.CancelFunc
	cancelled  bool

	sideOn         bool
	sessPTok       int
	sessCTok       int
	sessUSD        float64
	lastCtx        int
	lastCtxB       int
	toolCallsTotal int
	toolTurns      int
	toolModeTurns  int
}

type deltaMsg string
type doneMsg struct {
	text  string
	note  string
	ptok  int
	ctok  int
	usd   float64
	ctx   int
	ctxB  int
	calls int
	agent bool
}

type reviewPostDone struct {
	text string
	repo string
	pr   int
}
type errMsg struct{ err error }
type sysMsg string
type toolMsg string

// fileOpMsg renders a file write/edit as a distinct card (filename on top).
type fileOpMsg struct {
	op     string
	path   string
	detail string
	ok     bool
}
type resetStreamMsg struct{}

// storeInstalledMsg carries a finished store install to the UI thread.
type storeInstalledMsg struct {
	kind string
	name string
	err  error
}
type mcpLoadedMsg struct {
	names []string
	tools []tools.Tool
	err   error
}

type progWriter struct{ send func(string) }

func (w progWriter) Write(p []byte) (int, error) { w.send(string(p)); return len(p), nil }

var _ io.Writer = progWriter{}

func New(cfg config.Config, r *router.Router, sess *sessions.Session, workdir string) Model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything…  (/help, Tab modes, @file to attach)"
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetHeight(3)
	vp := viewport.New(80, 20)
	th := theme.Dark()
	ag, _ := agents.Get("builder")
	em := embed.New(cfg.OllamaHost, "")
	m := Model{
		cfg: cfg, router: r, sess: sess, ta: ta, vp: vp,
		keys: DefaultKeyMap(), th: th,
		mode: modes.Chat, agent: ag,
		toolreg: tools.DefaultRegistry(workdir),
		workdir: workdir, tracker: cost.New(),
		mcpMgr: mcp.NewManager(), hookset: hooks.Load(),
		skillMgr: skills.NewManager(),
		embedder: em, semCache: cache.New(em.Embed),
		pluginLoader: plugins.NewLoader(),
		sideOn:       true,
	}
	m.registerPluginTools()
	return m
}

func (m *Model) registerPluginTools() {
	m.pluginLoader.Reload()
	live := map[string]bool{}
	for _, t := range m.pluginLoader.Tools() {
		m.toolreg.Add(t)
		live[t.Name()] = true
	}
	var kept []string
	for _, n := range m.pluginNames {
		if live[n] {
			kept = append(kept, n)
		} else {
			m.toolreg.Remove(n)
		}
	}
	for n := range live {
		found := false
		for _, k := range kept {
			if k == n {
				found = true
				break
			}
		}
		if !found {
			kept = append(kept, n)
		}
	}
	m.pluginNames = kept
}

func (m *Model) SetProgram(p *tea.Program) { m.prog = p }

func (m Model) Init() tea.Cmd { return textarea.Blink }

func (m *Model) renderAll() {
	m.msgs = nil
	for _, msg := range m.sess.Messages {
		m.msgs = append(m.msgs, m.formatMsg(msg.Role, msg.Content))
	}
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

func (m *Model) formatMsg(role apitypes.Role, content string) string {
	if role == apitypes.RoleUser {
		st := lipgloss.NewStyle().Foreground(m.th.Accent).Bold(true)
		return st.Render("you › ") + content
	}
	if role == apitypes.RoleSystem {
		st := lipgloss.NewStyle().Foreground(m.th.Dim).Italic(true)
		return st.Render(content)
	}
	return renderAssistant(content)
}

func (m *Model) appendSys(s string) {
	st := lipgloss.NewStyle().Foreground(m.th.Dim).Background(sysBG).Padding(0, 1)
	m.msgs = append(m.msgs, st.Render(s))
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

// appendFileCard renders a write/edit as a distinct card: filename on top.
func (m *Model) appendFileCard(f fileOpMsg) {
	mark := "✓"
	if !f.ok {
		mark = "✗"
	}
	path := f.path
	if path == "" {
		path = "(unknown file)"
	}
	head := fileCardHead(f.ok).Render(fmt.Sprintf("%s %s %s", mark, f.op, path))
	body := fileCardStyle().Render(f.detail)
	m.msgs = append(m.msgs, head+"\n"+body)
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
}

func (m *Model) initProject() string {
	dir := filepath.Join(m.workdir, ".ycode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "init failed: " + err.Error()
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		_ = os.WriteFile(cfgPath, []byte("# ycode project overrides\n# active_provider: ollama\n"), 0o644)
	}
	agentsPath := filepath.Join(m.workdir, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		_ = os.WriteFile(agentsPath, []byte("# Agent instructions\n\n- Be concise.\n- Run tests after edits.\n"), 0o644)
	}
	return "Initialized ycode in " + m.workdir + " (.ycode/config.yaml, AGENTS.md)"
}

func (m *Model) cycleMode(dir int) {
	order := modes.Order()
	idx := 0
	for i, md := range order {
		if md == m.mode {
			idx = i
			break
		}
	}
	idx = (idx + dir + len(order)) % len(order)
	m.mode = order[idx]
}

// paletteItems returns the visible completion items (nil when closed).
func (m *Model) paletteItems() []slashItem {
	if m.busy || m.palHide {
		return nil
	}
	return filterSlash(m.ta.Value())
}

// atItems returns @file completion items (nil when closed).
func (m *Model) atItems() ([]slashItem, string) {
	if m.busy || m.atHide {
		return nil, ""
	}
	if strings.HasPrefix(strings.TrimSpace(m.ta.Value()), "/") {
		return nil, ""
	}
	token, ok := atToken(m.ta.Value())
	if !ok {
		return nil, ""
	}
	matches := completeFiles(m.workdir, token)
	if len(matches) == 0 {
		return nil, ""
	}
	items := make([]slashItem, len(matches))
	for i, p := range matches {
		items[i] = slashItem{Name: "@" + p}
	}
	return items, token
}

func (m *Model) cycleModelCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ms, err := ollama.New(m.cfg.OllamaHost).ListModels(ctx)
		if err != nil || len(ms) == 0 {
			return sysMsg("No ollama models found — use /models to see options")
		}
		idx := 0
		for i, mi := range ms {
			if mi.ID == m.cfg.ActiveModel {
				idx = i
				break
			}
		}
		next := ms[(idx+1)%len(ms)]
		return sysMsg(selectModelSilent(m, next.ID))
	}
}

// loadMCPsCmd starts configured MCP servers and returns their tools via message.
func (m *Model) loadMCPsCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ts, err := m.mcpMgr.Tools(ctx)
		if err != nil {
			return mcpLoadedMsg{err: err}
		}
		var names []string
		for _, t := range ts {
			names = append(names, t.Name())
		}
		return mcpLoadedMsg{names: names, tools: ts}
	}
}

// reviewCmd streams a single-shot review of diff through the model.
func (m *Model) reviewCmd(diff string) tea.Cmd {
	m.busy = true
	m.stream.Reset()
	m.cancelled = false
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	prog := m.prog
	prompt := "You are a strict code reviewer (" + m.agent.Name + " perspective: " + m.agent.Prompt + "). " +
		"Review the unified diff below. Output: summary, then per-file findings with file:line, then concrete fixes. Be concise.\n\n```diff\n" + diff + "\n```"
	hist := []apitypes.Message{{Role: apitypes.RoleUser, Content: prompt}}
	return func() tea.Msg {
		w := progWriter{send: func(s string) {
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}}
		full, err := m.router.Stream(turnCtx, hist, w)
		if err != nil {
			return errMsg{err}
		}
		pt := yctx.Estimate(hist)
		ct := len(full) / 4
		usd := m.tracker.Add(m.cfg.ActiveProvider, pt, ct)
		return doneMsg{text: "## Code review\n\n" + full, ptok: pt, ctok: ct, usd: usd, ctx: pt, ctxB: yctx.BudgetFor("")}
	}
}

// reviewPostCmd streams a review constrained to JSON findings for posting.
func (m *Model) reviewPostCmd(diff, repo string, pr int) tea.Cmd {
	m.busy = true
	m.stream.Reset()
	m.cancelled = false
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	prog := m.prog
	prompt := "You are a strict code reviewer. Review the unified diff below. " +
		"Output ONLY raw JSON, no fences, no prose: " +
		`{"summary": "2-3 sentence verdict", "comments": [{"path": "file", "line": N, "body": "finding + fix"}]}. ` +
		"line = NEW-file line number of the finding. Empty comments array when clean.\n\n```diff\n" + diff + "\n```"
	hist := []apitypes.Message{{Role: apitypes.RoleUser, Content: prompt}}
	return func() tea.Msg {
		w := progWriter{send: func(s string) {
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}}
		full, err := m.router.Stream(turnCtx, hist, w)
		if err != nil {
			return errMsg{err}
		}
		return reviewPostDone{text: full, repo: repo, pr: pr}
	}
}

func selectModelSilent(m *Model, model string) string { return applyModel(m, "ollama", model) }

// postReview parses model JSON findings and posts them as a PR review.
func (m *Model) postReview(text, repo string, pr int) string {
	clean := strings.TrimSpace(text)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	var findings struct {
		Summary  string `json:"summary"`
		Comments []struct {
			Path string `json:"path"`
			Line int    `json:"line"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(clean)), &findings); err != nil {
		return "could not parse review JSON: " + err.Error()
	}
	comments := make([]map[string]any, 0, len(findings.Comments))
	for _, c := range findings.Comments {
		comments = append(comments, map[string]any{"path": c.Path, "line": c.Line, "body": c.Body})
	}
	raw, _ := json.Marshal(map[string]any{
		"action": "review_post", "repo": repo, "number": pr,
		"review": map[string]any{"summary": findings.Summary, "comments": comments},
	})
	out, err := (&tools.GitHubTool{Workdir: m.workdir}).Run(context.Background(), raw)
	if err != nil {
		return "post failed: " + err.Error()
	}
	return out
}

func storeList(items []store.Entry) string {
	if len(items) == 0 {
		return "Store is empty."
	}
	var b strings.Builder
	for _, e := range items {
		fmt.Fprintf(&b, "- %s [%s] %s\n  %s\n", e.Name, e.Kind, displaySource(e), e.Description)
	}
	b.WriteString("/store install <name> · /store search <q> · /store update")
	return b.String()
}

func displaySource(e store.Entry) string {
	s := e.Source
	if e.Subdir != "" {
		s += "#" + e.Subdir
	}
	if e.Ref != "" {
		s += "@" + e.Ref
	}
	return s
}

// storeUpdateCmd refreshes the cached index in the background.
func (m *Model) storeUpdateCmd(url string) tea.Cmd {
	return func() tea.Msg {
		idx, err := store.Update(url)
		if err != nil {
			return sysMsg("store update failed: " + err.Error())
		}
		return sysMsg(fmt.Sprintf("store: %d entries", len(idx.Items)))
	}
}

// storeInstallCmd installs a store entry in the background.
func (m *Model) storeInstallCmd(e store.Entry) tea.Cmd {
	return func() tea.Msg {
		name, err := store.Install(e, m.skillMgr, m.pluginLoader)
		return storeInstalledMsg{kind: e.Kind, name: name, err: err}
	}
}

// modelsPullCmd pulls an Ollama model in the background.
func modelsPullCmd(model string) tea.Cmd {
	return func() tea.Msg {
		out, err := registry.Pull(context.Background(), model)
		if err != nil {
			return sysMsg("pull failed: " + err.Error() + "\n" + out)
		}
		return sysMsg("Installed " + model + " — switch with /models " + model)
	}
}

func modelsImportCmd(workdir, path, name string) tea.Cmd {
	return func() tea.Msg {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workdir, path)
		}
		out, err := registry.ImportGGUF(context.Background(), path, name)
		if err != nil {
			return sysMsg("import failed: " + err.Error() + "\n" + out)
		}
		return sysMsg(out + "\nSwitch with /models " + name)
	}
}

func restOrEmpty(f []string, i int) string {
	if i < len(f) {
		return strings.Join(f[i:], " ")
	}
	return ""
}

// ragIngestCmd builds the repo vector index in the background.
func (m *Model) ragIngestCmd(root string) tea.Cmd {
	if root == "" {
		root = m.workdir
	}
	return func() tea.Msg {
		idx, err := rag.Ingest(context.Background(), m.workdir, root, m.embedder.Embed)
		if err != nil {
			return sysMsg("rag ingest failed: " + err.Error())
		}
		m.ragIdx = &idx
		m.ragOff = false
		return sysMsg(fmt.Sprintf("rag: indexed %d chunks from %s", len(idx.Chunks), root))
	}
}

func (m *Model) submit() tea.Cmd {
	text := strings.TrimSpace(m.ta.Value())
	m.ta.Reset()
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		name := strings.Fields(text)[0]
		args := strings.TrimSpace(strings.TrimPrefix(text, name))
		h, ok := slashRegistry()[name]
		if !ok {
			m.appendSys("unknown command: " + name + " (try /help)")
			return nil
		}
		var out string
		var cmd tea.Cmd
		func() {
			defer func() {
				if r := recover(); r != nil {
					if e, ok := r.(error); ok && e == ErrQuit {
						if m.prog != nil {
							m.prog.Quit()
						}
					}
				}
			}()
			out, cmd = h(context.Background(), m, args)
		}()
		if out != "" {
			m.appendSys(out)
		}
		return cmd
	}
	m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleUser, Content: text})
	expanded, missing := expandAttachments(m.workdir, text)
	for _, miss := range missing {
		m.appendSys("attachment skipped: " + miss)
	}
	m.msgs = append(m.msgs, m.formatMsg(apitypes.RoleUser, text))
	m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
	m.vp.GotoBottom()
	m.busy = true
	m.stream.Reset()
	hist := append([]apitypes.Message(nil), m.sess.Messages...)
	userText := text
	if expanded != text {
		userText = expanded
		hist[len(hist)-1].Content = expanded
	}
	prog := m.prog
	mode := m.mode
	ag := m.agent
	reg := m.toolreg
	workdir := m.workdir
	tracker := m.tracker
	hookset := m.hookset
	skill := m.pendingSkill
	m.pendingSkill = ""
	mcpNames := append([]string(nil), m.mcpNames...)
	hookset.Fire(context.Background(), hooks.OnRequest, map[string]string{"mode": string(mode), "workdir": workdir})
	turnCtx, turnCancel := context.WithCancel(context.Background())
	m.turnCancel = turnCancel
	m.cancelled = false
	return func() tea.Msg {
		ctx := turnCtx
		if mode == modes.Plan {
			ctx = tools.WithReadOnly(ctx)
		}
		zdl := m.cfg.ZeroDataLeak
		if zdl {
			ctx = tools.WithZeroLeak(ctx)
		}
		toolCalls := 0
		written := 0
		toolBytes := 0
		w := progWriter{send: func(s string) {
			written += len(s)
			if prog != nil {
				prog.Send(deltaMsg(s))
			}
		}}
		onTool := func(name, args, result string, err error) {
			toolCalls++
			toolBytes += len(result)
			status := fmt.Sprintf("ok (%d bytes)", len(result))
			if err != nil {
				status = "error: " + err.Error()
			}
			audit.Log("tool", map[string]any{"tool": name, "args": truncateArgs(args), "status": status})
			if prog == nil {
				return
			}
			if name == "write" || name == "edit" {
				prog.Send(fileOpMsg{op: name, path: writeOpPath(args), detail: status, ok: err == nil})
				return
			}
			prog.Send(toolMsg(fmt.Sprintf("🔧 %s %s → %s", name, truncateArgs(args), status)))
		}
		p, model, err := m.router.Active()
		if err != nil {
			return errMsg{err}
		}
		sys := modes.SystemPrompt(mode, reg, workdir) + "\nActive agent: " + ag.Name + " — " + ag.Prompt
		if skill != "" {
			sys += "\nActive skill instructions:\n" + skill
		}
		if mode == modes.Build || mode == modes.Plan {
			sys += "\nRepo tree (" + workdir + ") — real paths, use them directly:\n" + yctx.Tree(workdir, 150, 4000) +
				"NEVER ask the user for paths or locations. If a file is named without a path, find it with glob/grep yourself."
		}
		// Local RAG: retrieve repo context when an index exists.
		ragNote := ""
		if !m.ragOff {
			if idx, ok := rag.Load(workdir); ok {
				if qv, err := m.embedder.Embed(ctx, []string{userText}); err == nil && len(qv) > 0 {
					if chunks := rag.Query(idx, qv[0], 4); len(chunks) > 0 {
						sys += "\n" + rag.FormatContext(chunks)
						ragNote = fmt.Sprintf(" · RAG %d chunks", len(chunks))
					}
				} else if err != nil {
					m.ragOff = true
					if prog != nil {
						prog.Send(sysMsg("RAG disabled this session (embed failed: " + err.Error() + ")"))
					}
				}
			}
		}
		budget := yctx.BudgetFor(model)
		trimmed, dropped := yctx.Trim(hist, budget-1500)
		if dropped > 0 && prog != nil {
			prog.Send(sysMsg(fmt.Sprintf("…compacted %d older messages to fit context", dropped)))
		}
		msgs := append([]apitypes.Message{{Role: apitypes.RoleSystem, Content: sys}}, trimmed...)
		promptTok := yctx.Estimate(msgs)
		allowed := modes.AllowedTools(mode, append(mcpNames, m.pluginNames...)...)
		if zdl {
			allowed = filterNetworkTools(allowed)
			sys += "\nZERO-DATA-LEAK: local only. No cloud providers, no browser, no GitHub API."
			msgs[0].Content = sys
		}
		notes := []string{fmt.Sprintf("~%d tokens", promptTok)}
		var answer string
		turnCalls := 0
		if len(allowed) == 0 {
			if mode == modes.Chat && fileTaskRe.MatchString(userText) && prog != nil {
				prog.Send(sysMsg("Tip: I have no file tools in chat mode — hit Tab or /build so I can read/edit files."))
			}
			if hit, ok := m.semCache.Lookup(ctx, userText, m.cfg.ActiveProvider, model); ok {
				est := yctx.Estimate([]apitypes.Message{{Role: apitypes.RoleUser, Content: userText}})
				return doneMsg{text: hit, note: "⚡ semantic cache hit (no model call)", ctx: est, ctxB: yctx.BudgetFor(model)}
			}
			full, fbNote, err := m.router.StreamWithFallback(ctx, msgs, w)
			if err != nil {
				return errMsg{err}
			}
			answer = full
			m.semCache.Store(ctx, userText, full, m.cfg.ActiveProvider, model)
			if fbNote != "" && prog != nil {
				prog.Send(sysMsg(fbNote))
			}
		} else {
			type pm struct {
				p     providers.Provider
				model string
				label string
			}
			chain := []pm{{p, model, ""}}
			for _, fb := range m.router.Fallbacks() {
				if fp, err := m.router.Provider(fb.Provider); err == nil {
					chain = append(chain, pm{fp, fb.Model, fb.Provider + "/" + fb.Model})
				}
			}
			done := false
			for i, cand := range chain {
				if i > 0 && prog != nil {
					prog.Send(resetStreamMsg{})
					prog.Send(sysMsg("↳ retrying turn on fallback " + cand.label))
				}
				res, err := agent.Run(ctx, cand.p, cand.model, msgs, reg, allowed, hookset, w, onTool)
				turnCalls += res.Calls
				if err != nil && res.Text == "" {
					if i < len(chain)-1 {
						continue
					}
					return errMsg{err}
				}
				answer = res.Text
				if err != nil {
					answer += "\n\n(stopped early: " + err.Error() + ")"
				}
				if i > 0 {
					notes = append(notes, "fell back to "+cand.label)
				}
				// Self-correction: pasted file content instead of acting.
				// One bounded retry round, then whatever comes back stands.
				if toolCalls == 0 && looksLikeWriteTask(userText) && hasCodeFence(answer) {
					if prog != nil {
						prog.Send(sysMsg("↳ pasted content instead of writing — retrying with tools…"))
					}
					retryMsgs := append(append([]apitypes.Message(nil), msgs...),
						apitypes.Message{Role: apitypes.RoleAssistant, Content: answer},
						apitypes.Message{Role: apitypes.RoleSystem, Content: "You pasted file content as text instead of using the write/edit tool. Redo this turn properly: emit ONLY tool call(s) that perform the write, no pasted content."})
					res2, err2 := agent.Run(ctx, cand.p, cand.model, retryMsgs, reg, allowed, hookset, w, onTool)
					turnCalls += res2.Calls
					if err2 == nil || res2.Text != "" {
						answer = res2.Text
						notes = append(notes, "self-corrected to tools")
					}
				}
				if toolCalls == 0 && fileTaskRe.MatchString(userText) {
					notes = append(notes, "no tools were called — rephrase with explicit paths, or try a larger coder model (/models)")
				}
				done = true
				break
			}
			if !done {
				return errMsg{fmt.Errorf("all providers failed")}
			}
		}
		complTok := written/4 + toolBytes/4
		turnUSD := tracker.Add(m.cfg.ActiveProvider, promptTok, complTok)
		_, _, usd := tracker.Today()
		notes = append(notes, fmt.Sprintf("$%.4f today", usd))
		if ragNote != "" {
			notes = append(notes, strings.TrimPrefix(ragNote, " · "))
		}
		audit.Log("turn", map[string]any{"mode": string(mode), "provider": m.cfg.ActiveProvider, "model": model, "prompt": userText, "answer": answer})
		return doneMsg{text: answer, note: "↳ " + strings.Join(notes, " · "), ptok: promptTok, ctok: complTok, usd: turnUSD, ctx: promptTok, ctxB: budget, calls: turnCalls, agent: len(allowed) > 0}
	}
}

func truncateArgs(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.conn != nil {
		if _, ok := msg.(tea.KeyMsg); ok {
			cmd := m.updateConnect(msg)
			if m.conn != nil && m.conn.done {
				res := m.conn.result
				m.conn = nil
				m.appendSys(m.applyConnect(res))
			}
			return m, cmd
		}
		if _, ok := msg.(fetchModelsMsg); ok {
			return m, m.updateConnect(msg)
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.winW, m.winH = msg.Width, msg.Height
		m.vp.Width = msg.Width
		if m.sideOn && msg.Width > sideWidth+40 {
			m.vp.Width = msg.Width - sideWidth - 3
		}
		m.vp.Height = msg.Height - 7
		m.ta.SetWidth(msg.Width - 4)
		if m.startup == "" {
			m.startup = theme.Splash(m.th.Accent)
			m.vp.SetContent(m.startup + "\n\n" + strings.Join(m.msgs, "\n\n"))
		}
		return m, nil
	case deltaMsg:
		if m.cancelled {
			return m, nil
		}
		m.stream.WriteString(string(msg))
		preview := m.formatMsg(apitypes.RoleAssistant, m.stream.String())
		base := strings.Join(m.msgs, "\n\n")
		if base != "" {
			base += "\n\n"
		}
		m.vp.SetContent(base + preview)
		m.vp.GotoBottom()
		return m, nil
	case toolMsg:
		if m.cancelled {
			return m, nil
		}
		m.appendSys(string(msg))
		return m, nil
	case fileOpMsg:
		if m.cancelled {
			return m, nil
		}
		m.appendFileCard(msg)
		return m, nil
	case resetStreamMsg:
		m.stream.Reset()
		m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
		m.vp.GotoBottom()
		return m, nil
	case doneMsg:
		m.busy = false
		m.turnCancel = nil
		if m.cancelled {
			m.cancelled = false
			m.stream.Reset()
			m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
			m.vp.GotoBottom()
			return m, nil
		}
		_ = sessions.MaybeAutoTitle(m.sess)
		m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleAssistant, Content: msg.text})
		_ = m.sess.Save()
		m.msgs = append(m.msgs, m.formatMsg(apitypes.RoleAssistant, msg.text))
		m.stream.Reset()
		m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
		m.vp.GotoBottom()
		m.sessPTok += msg.ptok
		m.sessCTok += msg.ctok
		m.sessUSD += msg.usd
		m.toolCallsTotal += msg.calls
		if msg.agent {
			m.toolModeTurns++
			if msg.calls > 0 {
				m.toolTurns++
			}
		}
		if msg.ctxB > 0 {
			m.lastCtx, m.lastCtxB = msg.ctx, msg.ctxB
		}
		if msg.note != "" {
			m.appendSys(msg.note)
		}
		m.hookset.Fire(context.Background(), hooks.OnResponse, map[string]string{"mode": string(m.mode), "workdir": m.workdir})
		webhooks.Fire("turn_complete", map[string]any{"mode": string(m.mode), "provider": m.cfg.ActiveProvider, "model": m.cfg.ActiveModel})
		return m, nil
	case errMsg:
		m.busy = false
		m.turnCancel = nil
		if m.cancelled {
			m.cancelled = false
			m.stream.Reset()
			m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
			m.vp.GotoBottom()
			return m, nil
		}
		m.stream.Reset()
		m.appendSys("error: " + msg.err.Error())
		if out := m.hookset.Fire(context.Background(), hooks.OnError, map[string]string{"error": msg.err.Error()}); out != "" {
			m.appendSys(out)
		}
		webhooks.Fire("turn_error", map[string]any{"error": msg.err.Error()})
		return m, nil
	case reviewPostDone:
		m.busy = false
		m.turnCancel = nil
		if m.cancelled {
			m.cancelled = false
			m.stream.Reset()
			m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
			m.vp.GotoBottom()
			return m, nil
		}
		m.stream.Reset()
		m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleAssistant, Content: msg.text})
		_ = m.sess.Save()
		m.msgs = append(m.msgs, m.formatMsg(apitypes.RoleAssistant, msg.text))
		m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
		m.vp.GotoBottom()
		m.appendSys(m.postReview(msg.text, msg.repo, msg.pr))
		return m, nil
	case mcpLoadedMsg:
		if msg.err != nil {
			m.appendSys("mcp error: " + msg.err.Error())
			return m, nil
		}
		added := 0
		for _, t := range msg.tools {
			m.toolreg.Add(t)
			added++
		}
		m.mcpNames = append(m.mcpNames, msg.names...)
		m.appendSys(fmt.Sprintf("mcp: loaded %d tools", added))
		return m, nil
	case sysMsg:
		m.appendSys(string(msg))
		return m, nil
	case storeInstalledMsg:
		if msg.err != nil {
			m.appendSys("store install failed: " + msg.err.Error())
			return m, nil
		}
		if msg.kind == "plugin" {
			m.registerPluginTools()
		}
		m.appendSys("Installed " + msg.kind + ": " + msg.name)
		return m, nil
	case editorDoneMsg:
		if msg.err != nil {
			m.appendSys("editor error: " + msg.err.Error())
		} else {
			m.appendSys("editor closed")
		}
		return m, nil
	case tea.KeyMsg:
		if pal := m.paletteItems(); len(pal) > 0 {
			complete := func() {
				if m.palIdx < 0 || m.palIdx >= len(pal) {
					m.palIdx = 0
				}
				sel := pal[m.palIdx]
				cur := m.ta.Value()
				rest := ""
				if i := strings.IndexByte(cur, ' '); i >= 0 {
					rest = cur[i:]
				}
				m.ta.SetValue(sel.Name + rest + " ")
				m.palIdx = 0
			}
			switch msg.String() {
			case "up":
				if m.palIdx > 0 {
					m.palIdx--
				} else {
					m.palIdx = len(pal) - 1
				}
				return m, nil
			case "down":
				m.palIdx = (m.palIdx + 1) % len(pal)
				return m, nil
			case "tab":
				complete()
				return m, nil
			case "enter":
				if strings.Contains(m.ta.Value(), " ") {
					break // has args — run it
				}
				complete()
				return m, nil
			case "esc":
				m.palHide = true
				return m, nil
			default:
				m.palIdx = 0
			}
		}
		if at, token := m.atItems(); len(at) > 0 {
			completeAt := func() {
				if m.atIdx < 0 || m.atIdx >= len(at) {
					m.atIdx = 0
				}
				cur := m.ta.Value()
				i := strings.LastIndex(cur, "@"+token)
				if i >= 0 {
					m.ta.SetValue(cur[:i] + at[m.atIdx].Name + " ")
				}
				m.atIdx = 0
			}
			switch msg.String() {
			case "up":
				if m.atIdx > 0 {
					m.atIdx--
				} else {
					m.atIdx = len(at) - 1
				}
				return m, nil
			case "down":
				m.atIdx = (m.atIdx + 1) % len(at)
				return m, nil
			case "tab":
				completeAt()
				return m, nil
			case "enter":
				if len(at) == 1 && at[0].Name == "@"+token {
					break // fully typed — send it
				}
				completeAt()
				return m, nil
			case "esc":
				m.atHide = true
				return m, nil
			default:
				m.atIdx = 0
			}
		}
		switch msg.String() {
		case "ctrl+d":
			_ = m.sess.Save()
			m.pluginLoader.Close()
			m.stopWatch()
			return m, tea.Quit
		case "ctrl+c", "esc":
			if m.busy {
				m.busy = false
				m.cancelled = true
				if m.turnCancel != nil {
					m.turnCancel()
					m.turnCancel = nil
				}
				m.stream.Reset()
				m.vp.SetContent(strings.Join(m.msgs, "\n\n"))
				m.vp.GotoBottom()
				m.appendSys("(stopped — partial output discarded, Ctrl+C/Esc again to quit)")
				return m, nil
			}
			_ = m.sess.Save()
			m.pluginLoader.Close()
			m.stopWatch()
			return m, tea.Quit
		case "ctrl+n":
			m.sess = sessions.New(m.cfg.ActiveProvider, m.cfg.ActiveModel)
			_ = m.sess.Save()
			m.msgs = nil
			m.vp.SetContent("")
			m.appendSys("New session started.")
			return m, nil
		case "ctrl+o":
			return m, m.cycleModelCmd()
		case "ctrl+b":
			m.sideOn = !m.sideOn
			m.vp.Width = m.winW
			if m.sideOn && m.winW > sideWidth+40 {
				m.vp.Width = m.winW - sideWidth - 3
			}
			return m, nil
		case "tab":
			m.cycleMode(1)
			return m, nil
		case "shift+tab":
			m.cycleMode(-1)
			return m, nil
		case "enter":
			if m.busy {
				return m, nil
			}
			return m, m.submit()
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.vp, _ = m.vp.Update(msg)
	cmds := []tea.Cmd{cmd}
	if m.conn != nil && m.conn.step == cStepKey {
		var c2 tea.Cmd
		m.conn.keyInput, c2 = m.conn.keyInput.Update(msg)
		cmds = append(cmds, c2)
	}
	if v := m.ta.Value(); v != m.lastInput {
		m.lastInput = v
		m.palHide = false
		m.palIdx = 0
		m.atHide = false
		m.atIdx = 0
	}
	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	badge := ""
	if m.cfg.ZeroDataLeak {
		badge = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff5555")).Render(" [LOCAL ONLY]")
	}
	chip := lipgloss.NewStyle().Bold(true).Foreground(m.th.Accent).Render("▸ "+string(m.mode)) +
		lipgloss.NewStyle().Foreground(m.th.Dim).Render(" · "+m.agent.Name+" · tab: switch mode")
	if m.watching() {
		chip += lipgloss.NewStyle().Foreground(lipgloss.Color("#2de1a7")).Render(" · ● watching " + m.watchTarget)
	}
	input := chip + "\n" + m.ta.View()
	chat := m.vp.View()
	if m.sideOn && m.winW > sideWidth+40 {
		chat = lipgloss.JoinHorizontal(lipgloss.Top, chat, m.sidebar(m.vp.Height))
	}
	out := chat + "\n"
	if pal := m.paletteItems(); len(pal) > 0 {
		out += renderPalette(pal, m.palIdx, m.vp.Width, m.th.Accent) + "\n"
	} else if at, _ := m.atItems(); len(at) > 0 {
		out += renderPalette(at, m.atIdx, m.vp.Width, m.th.Accent) + "\n"
	}
	status := lipgloss.NewStyle().Foreground(m.th.Dim).Render(
		fmt.Sprintf(" %s/%s · %d msgs · /help ", m.cfg.ActiveProvider, m.cfg.ActiveModel, len(m.sess.Messages))) + badge
	base := out + input + "\n" + status
	if m.conn != nil {
		w, h := m.winW, m.winH
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 24
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.conn.view(w-8, m.th.Accent))
	}
	return base
}

func filterNetworkTools(names []string) []string {
	var out []string
	for _, n := range names {
		if n == "browser" || n == "github" {
			continue
		}
		out = append(out, n)
	}
	return out
}
