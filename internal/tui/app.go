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
	"github.com/Yash-K-Jagani/ycode/internal/trace"
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

	// unseen is how many lines have arrived below the reader since they last
	// looked. Zero while they are following the output; shown as a jump-to-
	// latest affordance when they are not.
	unseen int

	// hist is the submitted-prompt history for the input box. See input.go.
	hist inputHistory

	// budget is the daily spend ceiling. Nil is not a thing: New always sets
	// one, and Unlimited() is the answer when no limit is configured, so
	// callers never have to check for nil before asking.
	budget *cost.Budget

	// tracer records the last few turns for /debug: request, response, tool
	// calls, usage and routing decisions. See internal/trace.
	tracer *trace.Recorder

	// budgetWarned records that the limit has already been reported, so the
	// message is given once at the crossing rather than on every turn after it.
	budgetWarned bool

	// liveTool is the tool currently streaming output, and liveBuf the tail of
	// what it has printed so far.
	//
	// It is rendered as a provisional tail rather than appended to the
	// transcript, because the transcript is permanent and this is not: the same
	// output arrives again, complete, when the call finishes. Appending here and
	// appending again on completion would show a command's output twice.
	liveTool string
	liveBuf  []byte

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

// toolMsg is a finished tool call: the one-line status the transcript has always
// shown, plus the output when there was any worth reading.
//
// The output used to be dropped. A bash call reported "ok (812 bytes)" and
// never showed what the command printed, and for the tools people actually run
// by hand that output is usually the entire point of running them.
type toolMsg struct {
	name   string
	status string
	out    string
}

// toolChunkMsg is one piece of a streaming tool's output, arriving while the
// call is still in flight.
type toolChunkMsg struct {
	tool  string
	chunk string
}

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

// newViewport is the transcript viewport, with its keymap cleared.
//
// The default keymap is a pager's: space pages down, b pages up, and
// u/d/j/k/h/l scroll by half-page and by line. The update loop handed every key
// to the viewport as well as to the input box, so all of those were live while
// typing - every space in a prompt paged the transcript, and typing one
// sentence scrolled it 91 lines.
//
// In a chat client the input box has the keyboard. The transcript is scrolled
// by keys that cannot occur in text, handled by Model.scrollKey: PageUp and
// PageDown, alt-arrow and ctrl-arrow, and End to snap back to the bottom.
//
// This is a constructor rather than a line inside New so that a test building a
// Model by hand gets the same viewport as production. A helper that quietly
// diverges from the real thing is how a bug like this comes back.
func newViewport(w, h int) viewport.Model {
	vp := viewport.New(w, h)
	vp.KeyMap = viewport.KeyMap{}
	return vp
}

func New(cfg config.Config, r *router.Router, sess *sessions.Session, workdir string) Model {
	ta := newTextarea()
	th := theme.For(cfg.Theme)
	ApplyTheme(th)
	ag, _ := agents.Get("builder")
	em := embed.New(cfg.OllamaHost, "")
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(th.Accent)
	vp := newViewport(80, 20)
	// One tracker, shared with the budget. Two would each keep their own
	// in-memory day totals and disagree with each other, and the budget would
	// then be enforcing against a number nothing else was recording.
	tracker := cost.New()
	m := Model{
		cfg: cfg, router: r, sess: sess, ta: ta, vp: vp,
		keys: DefaultKeyMap(), th: th,
		mode: modes.Chat, agent: ag,
		toolreg: tools.DefaultRegistry(workdir),
		workdir: workdir, tracker: tracker,
		tracer: trace.New(),
		budget: cost.NewBudget(tracker, cfg.DailyBudgetUSD),
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
	case toolChunkMsg:
		if m.cancelled {
			return m, nil
		}
		// A different tool name means the previous one finished without us being
		// told, which happens when a turn is abandoned mid-call. Start clean
		// rather than interleaving two commands' output into one block.
		if m.liveTool != msg.tool {
			m.liveTool = msg.tool
			m.liveBuf = m.liveBuf[:0]
		}
		m.liveBuf = append(m.liveBuf, msg.chunk...)
		if len(m.liveBuf) > maxLiveToolBytes {
			// Keep the tail. For a build or a test run the end holds the answer,
			// and the beginning is often thousands of lines of progress bars.
			m.liveBuf = append(m.liveBuf[:0], m.liveBuf[len(m.liveBuf)-maxLiveToolBytes:]...)
		}
		m.syncViewport(m.livePreview())
		return m, nil
	case toolMsg:
		if m.cancelled {
			return m, nil
		}
		// A tool result is the only thing in the TUI that can change the task
		// list the sidebar shows, so this is where the cache is dropped.
		m.invalidateSidebar()
		// Whatever was streaming is finished. The provisional block has to go
		// before the permanent one goes in, or the same output appears twice.
		m.clearLiveTool()
		head := fmt.Sprintf("🔧 %s → %s", msg.name, msg.status)
		if out := strings.TrimRight(msg.out, "\n"); out != "" {
			// The command card rather than a sys line: output is multi-line and
			// wants its own tinted block, or it drowns in the transcript.
			m.appendCmdCard(cmdOpMsg{tool: msg.name, cmd: head, detail: capOutput(out), ok: true})
			return m, nil
		}
		m.appendSys(head)
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
		if handled, resized := m.handleInputKey(msg); handled {
			if resized {
				m.resizeInput()
			}
			return m, nil
		}
		if m.scrollKey(msg) {
			return m, nil
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
			// Recorded before the submit, because submit clears the box and
			// the value is not available afterwards.
			m.hist.add(m.ta.Value())
			m.hist.reset()
			return m, m.submit()
		}
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
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
		// Re-measure the box whenever the content changes, not only on the
		// keys that can insert a line break. Backspacing from a pasted log has
		// to shrink the prompt back, or it stays tall and empty.
		m.resizeInput()
	}
	return m, tea.Batch(cmds...)
}
