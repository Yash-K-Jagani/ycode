package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// scripted is an Ollama-compatible server that replays canned replies, so the
// goal loop can be driven end to end without a model. Each POST /api/chat
// consumes the next reply; the last reply repeats once the script runs out.
type scripted struct {
	mu      sync.Mutex
	replies []string
	seen    [][]apitypes.Message
	srv     *httptest.Server
}

func newScripted(t *testing.T, replies ...string) *scripted {
	t.Helper()
	s := &scripted{replies: replies}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"fake-model","size":1}]}`))
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []apitypes.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.seen = append(s.seen, body.Messages)
		i := len(s.seen) - 1
		reply := s.replies[len(s.replies)-1]
		if i < len(s.replies) {
			reply = s.replies[i]
		}
		s.mu.Unlock()
		b, _ := json.Marshal(reply)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"content":` + string(b) + `},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"content":""},"done":true}` + "\n"))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *scripted) lastPrompt() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 || len(s.seen[len(s.seen)-1]) == 0 {
		return ""
	}
	return s.seen[len(s.seen)-1][0].Content
}

func (s *scripted) lastRole() apitypes.Role {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.seen[len(s.seen)-1]
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Role
}

func (s *scripted) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// newLoopModel wires a Model to the scripted server the way New() would.
func newLoopModel(t *testing.T, s *scripted, workdir string) *Model {
	t.Helper()
	ta := textarea.New()
	ta.SetHeight(1)
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.ActiveModel = "fake-model"
	cfg.OllamaHost = s.srv.URL
	return &Model{
		cfg:     cfg,
		router:  router.New(cfg),
		mode:    modes.Goal,
		workdir: workdir,
		toolreg: tools.DefaultRegistry(workdir),
		tracker: cost.New(),
		hookset: hooks.LoadFiles(nil),
		ta:      ta,
		vp:      viewport.New(80, 20),
		th:      theme.For("dark"),
		sess:    sessions.New("ollama", "fake-model"),
		goal:    goal.New("write hello to out.txt", 4),
		sideOn:  true,
		ragOff:  true,
	}
}

// runCmd executes a command exactly once, flattening tea.Batch.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if bm, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range bm {
			if sub != nil {
				out = append(out, runCmd(sub)...)
			}
		}
		return out
	}
	return []tea.Msg{msg}
}

// drain executes cmd, feeds every resulting message back into Update, and
// follows whatever command Update returns — that repetition is the goal loop.
// It returns how many iterations ran.
func drain(t *testing.T, m *Model, cmd tea.Cmd) int {
	t.Helper()
	iterations := 0
	for cmd != nil && iterations < 10 {
		iterations++
		var next tea.Cmd
		for _, msg := range runCmd(cmd) {
			if _, ok := msg.(spinner.TickMsg); ok {
				continue
			}
			out, c := m.Update(msg)
			if nm, ok := out.(*Model); ok {
				m = nm
			}
			if c != nil && next == nil {
				next = c
			}
		}
		if !m.busy {
			break
		}
		cmd = next
	}
	return iterations
}

const writeHello = `<tool:write>{"path":"out.txt","content":"hello"}</tool:write>`

// A turn that really wrote a file and then claimed the goal is met ends the
// run as met, and the model was shown the goal block.
func TestGoalLoopEndsOnCredibleMet(t *testing.T) {
	dir := t.TempDir()
	s := newScripted(t, writeHello, "wrote it and checked\nGOAL MET")
	m := newLoopModel(t, s, dir)

	drain(t, m, m.startTurn(m.goal.Text, turnOpts{}))

	if m.goal.Status != goal.Met {
		t.Fatalf("Status = %q, want met", m.goal.Status)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("the write should have landed: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("out.txt = %q", data)
	}
	if !strings.Contains(s.lastPrompt(), "ACTIVE GOAL") {
		t.Fatalf("the model was not shown the goal block:\n%s", s.lastPrompt())
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "goal met after 1 iteration") {
		t.Fatalf("no met note: %v", m.msgs)
	}
}

// The same claim with no work behind it is rejected, and the run continues
// into another iteration that does the work.
//
// The second scripted reply is also a bare claim on purpose: the TUI's
// existing self-correction retry runs inside the turn first, and it must be
// given nothing to correct with before the goal loop sees the verdict.
func TestGoalLoopRejectsUnearnedMetThenContinues(t *testing.T) {
	dir := t.TempDir()
	s := newScripted(t,
		"everything is already done\nGOAL MET", // no tool call: a lie
		"still nothing to do",                  // self-correction retry: also a lie
		writeHello,
		"now it really is\nGOAL MET", // backed by the write above
	)
	m := newLoopModel(t, s, dir)

	drain(t, m, m.startTurn(m.goal.Text, turnOpts{}))

	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Fatalf("the follow-up iteration should have written the file: %v", err)
	}
	if m.goal.Status != goal.Met {
		t.Fatalf("Status = %q, want met once the work is real", m.goal.Status)
	}
	if m.goal.Iter != 2 {
		t.Fatalf("Iter = %d, want 2 — the rejected claim costs an iteration", m.goal.Iter)
	}
	transcript := strings.Join(m.msgs, "\n")
	if !strings.Contains(transcript, "GOAL MET rejected") {
		t.Fatalf("the rejection should be shown to the user: %v", m.msgs)
	}
	if !strings.Contains(transcript, "no tool was called at all") {
		t.Fatalf("the rejection should say why: %v", m.msgs)
	}
	if !strings.Contains(transcript, "continuing unattended") {
		t.Fatalf("the run should have carried on: %v", m.msgs)
	}
}

// A turn with no tool calls is a stall, and is not retried.
func TestGoalLoopStallsWithoutToolCalls(t *testing.T) {
	dir := t.TempDir()
	s := newScripted(t, "I will add the file in a moment.")
	m := newLoopModel(t, s, dir)

	drain(t, m, m.startTurn(m.goal.Text, turnOpts{}))

	if m.goal.Status != goal.Stalled {
		t.Fatalf("Status = %q, want stalled", m.goal.Status)
	}
	if s.requests() != 1 {
		t.Fatalf("a stalled turn must not be retried, got %d requests", s.requests())
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "instead of calling tools") {
		t.Fatalf("no stall note: %v", m.msgs)
	}
}

// The continuation nudge reaches the model as a system message, the goal
// block is re-sent, and the transcript keeps exactly one user turn.
//
// The first turn must do real work but reach no verdict: a turn that only
// talks is a stall, not a continuation.
func TestGoalLoopNudgeStaysOutOfTheTranscript(t *testing.T) {
	dir := t.TempDir()
	s := newScripted(t,
		writeHello,                  // turn 1, round 1: real work
		"made progress, more to do", // turn 1, round 2: no verdict → continue
		`<tool:write>{"path":"second.txt","content":"more"}</tool:write>`,
		"done\nGOAL MET",
	)
	m := newLoopModel(t, s, dir)

	drain(t, m, m.startTurn(m.goal.Text, turnOpts{}))

	if s.requests() < 4 {
		t.Fatalf("expected a second iteration, got %d requests", s.requests())
	}
	if s.lastRole() != apitypes.RoleSystem {
		t.Fatalf("the nudge should arrive as a system message, got %q", s.lastRole())
	}
	if !strings.Contains(s.lastPrompt(), "ACTIVE GOAL") {
		t.Fatal("the goal block should be re-sent on later iterations")
	}
	users := 0
	for _, msg := range m.sess.Messages {
		if msg.Role != apitypes.RoleUser {
			continue
		}
		users++
		if strings.Contains(msg.Content, "Keep working the active goal") {
			t.Fatalf("continuation leaked into the transcript: %q", msg.Content)
		}
	}
	if users != 1 {
		t.Fatalf("a multi-iteration run should record exactly 1 user turn, got %d", users)
	}
	if m.goal.Iter != 2 {
		t.Fatalf("Iter = %d, want 2", m.goal.Iter)
	}
}

// Cancelling a turn ends the run as cancelled and does not continue.
func TestGoalLoopCancelStopsTheRun(t *testing.T) {
	dir := t.TempDir()
	s := newScripted(t, "made progress, more to do")
	m := newLoopModel(t, s, dir)
	m.cancelled = true

	if _, cmd := m.Update(doneMsg{mode: modes.Goal, text: "more to do", calls: 2, oks: 2, work: 1}); cmd != nil {
		t.Fatal("a cancelled turn must not continue the run")
	}
	if m.goal.Status != goal.Cancelled {
		t.Fatalf("Status = %q, want cancelled", m.goal.Status)
	}
	if s.requests() != 0 {
		t.Fatalf("cancelling before the turn should not call the model, got %d requests", s.requests())
	}
}
