package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Yash-K-Jagani/ycode/internal/agents"
	"github.com/Yash-K-Jagani/ycode/internal/cache"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/embed"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/mcp"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/plugins"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/rag"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/skills"
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

	// joined is msgs pre-joined, and sr is the incremental renderer for the
	// in-flight assistant message. Both exist because the transcript used to be
	// rebuilt in full on every streamed token: once per delta it re-rendered the
	// whole answer through glamour/chroma and rejoined the entire history, which
	// is quadratic in the length of the answer. See transcript.go.
	joined joinedTranscript
	sr     streamRender

	// side holds the sidebar's disk-backed values. The sidebar is rendered on
	// every message, and bubbletea sends one per streamed token, so reading the
	// todo file and the batch queue there meant two file/SQLite reads per
	// chunk of output. See sidebarCache.
	side sidebarCache

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

	modelsModal   *modelsModal
	sessionsModal *sessionsModal
	storeModal    *storeModal

	watchCancel context.CancelFunc
	watchTarget string

	turnCancel context.CancelFunc
	cancelled  bool

	pendingPlan string
	goal        *goal.Goal

	sideOn         bool
	sessPTok       int
	sessCTok       int
	sessUSD        float64
	lastCtx        int
	lastCtxB       int
	toolCallsTotal int
	toolTurns      int
	toolModeTurns  int

	sp        spinner.Model
	busySince time.Time

	showTree bool
	compact  bool

	toasts []toast
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
	oks   int
	fails int
	work  int
	agent bool
	mode  modes.Mode
}

// noSuccessNote explains a tool-less turn honestly (names failures).
func noSuccessNote() string {
	return "no tools completed successfully — rephrase with explicit paths, allow the prompt (y), or try a larger coder model (/models)"
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
	diff   string
	ok     bool
}

// cmdOpMsg renders a command execution on its own tinted background.
type cmdOpMsg struct {
	tool   string
	cmd    string
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

// modelSwitchedMsg carries a Ctrl+O model choice from the Cmd goroutine to the
// UI thread, where m.cfg, m.sess and the router are safe to mutate.
type modelSwitchedMsg struct{ model string }

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
	th := theme.For(cfg.Theme)
	ApplyTheme(th)
	ag, _ := agents.Get("builder")
	em := embed.New(cfg.OllamaHost, "")
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(th.Accent)
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
		sp:           sp,
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
	trim := strings.TrimSpace(m.ta.Value())
	if strings.HasPrefix(trim, "/") || strings.HasPrefix(trim, ":") {
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

// cycleModelCmd asks Ollama for the next installed model.
//
// The switch itself is applied on the UI thread via modelSwitchedMsg: doing it
// here would write m.cfg, m.sess and router.cfg from a Cmd goroutine while the
// turn goroutine and View() read them.
// Shutdown saves state and releases everything that outlives the process. It is
// exported so the CLI can call it from a defer, covering every exit path: the
// Ctrl+D and Esc keys, the /exit panic, a recovered handler panic and a signal.
// MCP servers are child processes, and on Windows they survive the parent, so
// without this repeated sessions leave orphans holding stdio pipes.
func (m *Model) Shutdown() {
	_ = m.sess.Save()
	if m.pluginLoader != nil {
		m.pluginLoader.Close()
	}
	if m.mcpMgr != nil {
		m.mcpMgr.Close()
	}
	m.stopWatch()
}

func (m *Model) cycleModelCmd() tea.Cmd {
	host, current := m.cfg.OllamaHost, m.cfg.ActiveModel
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ms, err := ollama.New(host).ListModels(ctx)
		if err != nil || len(ms) == 0 {
			return sysMsg("No ollama models found — use /models to see options")
		}
		idx := 0
		for i, mi := range ms {
			if mi.ID == current {
				idx = i
				break
			}
		}
		return modelSwitchedMsg{model: ms[(idx+1)%len(ms)].ID}
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

// turnOpts controls one turn of work. Auto turns are synthetic continuations
// (goal mode): they carry no user message into the transcript, only a system
// nudge, so the run leaves one clean record of the goal instead of a dozen
// "continue" prompts.
type turnOpts struct {
	auto  bool
	nudge string
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Modals and the connect form take precedence over everything else.
	if model, cmd, handled := m.updateOverlay(msg); handled {
		return model, cmd
	}
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		if m.busy {
			return m, cmd
		}
		return m, nil
	case toastExpireMsg:
		return m, nil
	case tea.WindowSizeMsg:
		m.winW, m.winH = msg.Width, msg.Height
		side := 0
		if m.sideOn && msg.Width > sideWidth+40 {
			side += sideWidth + 3
		}
		if m.showTree && msg.Width > fileTreeWidth+40 {
			side += fileTreeWidth + 3
		}
		if side > 0 && msg.Width > side+40 {
			m.vp.Width = msg.Width - side
		} else {
			m.vp.Width = msg.Width
		}
		m.vp.Height = msg.Height - 7
		m.ta.SetWidth(msg.Width - 4)
		if m.startup == "" {
			m.startup = theme.Splash(m.th.Accent)
			body := m.joined.body
			if body == "" {
				m.vp.SetContent(m.startup)
			} else {
				m.vp.SetContent(m.startup + "\n\n" + body)
			}
		}
		return m, nil
	case deltaMsg:
		if m.cancelled {
			return m, nil
		}
		m.stream.WriteString(string(msg))
		// Two renderers on purpose: the part of the answer that is already
		// final is rendered once and cached, and the paragraph still arriving
		// is rendered cheaply on every token. The history is not rejoined
		// either. Previously this re-rendered the whole answer through
		// glamour/chroma and rejoined every message, once per token.
		preview := m.sr.update(m.stream.String(), renderAssistant, renderStreaming)
		m.syncViewport(preview)
		return m, nil
	case toolMsg:
		if m.cancelled {
			return m, nil
		}
		// A tool result is the only thing in the TUI that can change the task
		// list the sidebar shows, so this is where the cache is dropped.
		m.invalidateSidebar()
		m.appendSys(string(msg))
		return m, nil
	case fileOpMsg:
		if m.cancelled {
			return m, nil
		}
		m.invalidateSidebar()
		m.appendFileCard(msg)
		return m, nil
	case cmdOpMsg:
		if m.cancelled {
			return m, nil
		}
		m.invalidateSidebar()
		m.appendCmdCard(msg)
		return m, nil
	case resetStreamMsg:
		m.stream.Reset()
		m.sr.reset()
		m.syncViewport()
		return m, nil
	case doneMsg:
		m.busy = false
		m.turnCancel = nil
		// A turn can end with the task list changed by a batch of tools, and
		// goal mode closes steps in its final turn.
		m.invalidateSidebar()
		if m.cancelled {
			m.cancelled = false
			m.stopGoalRun()
			m.stream.Reset()
			m.sr.reset()
			m.syncViewport()
			m.noteGoalStop()
			return m, nil
		}
		_ = sessions.MaybeAutoTitle(m.sess)
		if m.mode == modes.Plan && strings.Contains(msg.text, "AWAITING APPROVAL") {
			m.pendingPlan = strings.TrimSpace(msg.text)
		}
		m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleAssistant, Content: msg.text})
		_ = m.sess.Save()
		// The turn's final render is done from scratch, so the incremental
		// render used while it streamed cannot leave a stale tail behind.
		m.appendMsg(m.formatMsg(apitypes.RoleAssistant, msg.text))
		m.stream.Reset()
		m.sr.reset()
		m.sessPTok += msg.ptok
		m.sessCTok += msg.ctok
		m.sessUSD += msg.usd
		m.toolCallsTotal += msg.calls
		if msg.agent {
			m.toolModeTurns++
			if msg.oks > 0 {
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
		if cont := m.advanceGoal(msg); cont != nil {
			return m, cont
		}
		return m, nil
	case errMsg:
		m.busy = false
		m.turnCancel = nil
		if m.cancelled {
			m.cancelled = false
			m.stopGoalRun()
			m.noteGoalStop()
			m.stream.Reset()
			m.sr.reset()
			m.syncViewport()
			return m, nil
		}
		m.stream.Reset()
		// Resolved once, because three separate uses of msg.err.Error() is
		// three chances to dereference a nil - and a panic inside Update kills
		// the session rather than showing a wrong thing. Every current sender
		// only builds this with a non-nil error, but the handler does not
		// depend on that.
		text := "the turn failed for an unknown reason"
		if msg.err != nil {
			text = msg.err.Error()
		}
		m.appendSys("error: " + text)
		// A failed turn ends the run: continuing on the same error would
		// burn the iteration budget silently.
		if m.goal != nil && m.goal.Status == goal.Active {
			m.goal.Status = goal.Blocked
			m.noteGoalStop()
		}
		if out := m.hookset.Fire(context.Background(), hooks.OnError, map[string]string{"error": text}); out != "" {
			m.appendSys(out)
		}
		webhooks.Fire("turn_error", map[string]any{"error": text})
		return m, nil
	case reviewPostDone:
		m.busy = false
		m.turnCancel = nil
		if m.cancelled {
			m.cancelled = false
			m.stream.Reset()
			m.sr.reset()
			m.syncViewport()
			return m, nil
		}
		m.stream.Reset()
		m.sr.reset()
		m.sess.Messages = append(m.sess.Messages, apitypes.Message{Role: apitypes.RoleAssistant, Content: msg.text})
		_ = m.sess.Save()
		m.appendMsg(m.formatMsg(apitypes.RoleAssistant, msg.text))
		m.appendSys(m.postReview(msg.text, msg.repo, msg.pr))
		return m, nil
	case modelSwitchedMsg:
		if m.busy {
			// The active turn is already streaming with the previous model.
			return m, nil
		}
		m.appendSys(selectModelSilent(m, msg.model))
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
		// Dedup: /mcps tools is reloadable, and every duplicate name would be
		// appended to the mode allow-list and then rendered in full into the
		// system prompt (schema and all), so repeated reloads bloated every
		// later request until the provider rejected it.
		seen := make(map[string]bool, len(m.mcpNames))
		for _, n := range m.mcpNames {
			seen[n] = true
		}
		var fresh []string
		for _, n := range msg.names {
			if !seen[n] {
				seen[n] = true
				fresh = append(fresh, n)
			}
		}
		m.mcpNames = append(m.mcpNames, fresh...)
		note := fmt.Sprintf("mcp: loaded %d tools", added)
		if len(fresh) < len(msg.names) {
			note += fmt.Sprintf(" (%d already known)", len(msg.names)-len(fresh))
		}
		m.appendSys(note)
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
			m.Shutdown()
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
				m.sr.reset()
				m.syncViewport()
				if g := m.goal; g != nil && g.Running() {
					m.appendSys("(stopped — the goal run is cancelled with it; Esc again to quit)")
				} else {
					m.appendSys("(stopped — partial output discarded, Ctrl+C/Esc again to quit)")
				}
				return m, nil
			}
			_ = m.sess.Save()
			m.Shutdown()
			return m, tea.Quit
		case "ctrl+n":
			m.sess = sessions.New(m.cfg.ActiveProvider, m.cfg.ActiveModel)
			_ = m.sess.Save()
			m.clearMsgs()
			m.pendingPlan = ""
			m.goal = nil
			m.appendSys("New session started.")
			return m, nil
		case "ctrl+o":
			if m.busy {
				return m, nil
			}
			return m, m.cycleModelCmd()
		case "ctrl+p":
			// The palette is a completion popup driven by the input, so seeding
			// a "/" is all it takes to open it. This binding was declared in
			// the keymap and documented, but never dispatched.
			if m.busy {
				return m, nil
			}
			m.palHide = false
			m.ta.SetValue("/")
			m.ta.CursorEnd()
			m.ta.Focus()
			return m, textarea.Blink
		case "ctrl+r":
			// Same dispatch as typing /sessions, so there is one behaviour
			// rather than two that can diverge.
			if m.busy {
				return m, nil
			}
			h, ok := slashRegistry()["/sessions"]
			if !ok {
				return m, nil
			}
			out, cmd := h(context.Background(), m, "")
			if out != "" {
				m.appendSys(out)
			}
			return m, cmd
		case "ctrl+b":
			m.sideOn = !m.sideOn
			m.vp.Width = m.winW
			side := 0
			if m.sideOn && m.winW > sideWidth+40 {
				side += sideWidth + 3
			}
			if m.showTree && m.winW > fileTreeWidth+40 {
				side += fileTreeWidth + 3
			}
			if side > 0 && m.winW > side+40 {
				m.vp.Width = m.winW - side
			}
			return m, nil
		case "ctrl+\\":
			m.showTree = !m.showTree
			m.vp.Width = m.winW
			side := 0
			if m.sideOn && m.winW > sideWidth+40 {
				side += sideWidth + 3
			}
			if m.showTree && m.winW > fileTreeWidth+40 {
				side += fileTreeWidth + 3
			}
			if side > 0 && m.winW > side+40 {
				m.vp.Width = m.winW - side
			}
			return m, nil
		case "ctrl+=", "ctrl+_", "ctrl+]":
			m.compact = !m.compact
			SetCompact(m.compact)
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
