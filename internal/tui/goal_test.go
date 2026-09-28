package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
)

func newGoalModel(t *testing.T) *Model {
	t.Helper()
	workdir := t.TempDir()
	ta := textarea.New()
	ta.SetHeight(1)
	return &Model{
		cfg:     config.Defaults(),
		mode:    modes.Goal,
		workdir: workdir,
		toolreg: tools.DefaultRegistry(workdir),
		tracker: cost.New(),
		hookset: hooks.LoadFiles(nil),
		ta:      ta,
		vp:      viewport.New(80, 20),
		th:      theme.For("dark"),
		sess:    sessions.New("ollama", "test-model"),
		goal:    goal.New("add a healthcheck endpoint", 3),
		sideOn:  true,
		prog:    nil,
	}
}

func TestAdvanceGoalContinuesWhileActive(t *testing.T) {
	m := newGoalModel(t)
	cmd := m.advanceGoal(doneMsg{mode: modes.Goal, text: "Step 1 done.\nStep 1 done, moving on to the test", calls: 3, oks: 3, work: 1})
	if cmd == nil {
		t.Fatal("an active goal with no verdict must keep running")
	}
	if m.goal.Iter != 1 {
		t.Fatalf("Iter = %d, want 1", m.goal.Iter)
	}
	if m.goal.Status != goal.Active {
		t.Fatalf("Status = %q, want active", m.goal.Status)
	}
	if !m.busy {
		t.Fatal("the next iteration should start working")
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "continuing unattended") {
		t.Fatalf("no progress note shown: %v", m.msgs)
	}
	// The transcript keeps only real user turns: the synthetic nudge must
	// not appear as a user message.
	for _, msg := range m.sess.Messages {
		if strings.Contains(msg.Content, "Keep working the active goal") {
			t.Fatal("continuation nudge leaked into the session transcript")
		}
	}
	if len(m.sess.Messages) != 0 {
		t.Fatalf("auto turn added %d session messages, want 0", len(m.sess.Messages))
	}
}

func TestAdvanceGoalStopsOnVerdict(t *testing.T) {
	for _, c := range []struct {
		name   string
		text   string
		status goal.Status
		note   string
	}{
		{"met", "Added the route and a test.\nGOAL MET", goal.Met, "goal met after 1 iteration"},
		{"blocked", "Need a database URL.\nGOAL BLOCKED: no DSN", goal.Blocked, "blocked after 1 iteration"},
		{"explicit", "Done.\nGOAL_STATUS: met", goal.Met, "goal met after 1 iteration"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newGoalModel(t)
			// A credible met: the turn really did call tools, and the task
			// list is empty because every step was marked done.
			if cmd := m.advanceGoal(doneMsg{mode: modes.Goal, text: c.text, calls: 4, oks: 4, work: 2}); cmd != nil {
				t.Fatal("a finished goal must not continue")
			}
			if m.goal.Status != c.status {
				t.Fatalf("Status = %q, want %q", m.goal.Status, c.status)
			}
			if m.busy {
				t.Fatal("the run should be idle")
			}
			if !strings.Contains(strings.Join(m.msgs, "\n"), c.note) {
				t.Fatalf("missing stop note %q: %v", c.note, m.msgs)
			}
		})
	}
}

func TestAdvanceGoalStopsOnBudget(t *testing.T) {
	m := newGoalModel(t)
	m.goal = goal.New("endless", 2)
	for i := 1; i <= 2; i++ {
		if cmd := m.advanceGoal(doneMsg{mode: modes.Goal, text: "still working, I will add the endpoint next", calls: 2, oks: 2, work: 1}); i < 2 && cmd == nil {
			t.Fatalf("iteration %d should have continued", i)
		}
	}
	if m.goal.Status != goal.Spent {
		t.Fatalf("Status = %q, want budget_exhausted", m.goal.Status)
	}
	if m.goal.Iter != 2 {
		t.Fatalf("Iter = %d, want 2", m.goal.Iter)
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "budget spent") {
		t.Fatalf("missing exhaustion note: %v", m.msgs)
	}
}

func TestAdvanceGoalIgnoresOtherTurns(t *testing.T) {
	m := newGoalModel(t)
	if cmd := m.advanceGoal(doneMsg{mode: modes.Build, text: "GOAL MET"}); cmd != nil {
		t.Fatal("only goal-mode turns may advance a goal")
	}
	if m.goal.Iter != 0 || m.goal.Status != goal.Active {
		t.Fatalf("build turn changed the goal: %+v", m.goal)
	}
	bare := &Model{mode: modes.Goal}
	if cmd := bare.advanceGoal(doneMsg{mode: modes.Goal, text: "I will pick up the next step"}); cmd != nil {
		t.Fatal("no goal, no continuation")
	}
}

func TestAdvanceGoalStopsWhenUserLeavesGoalMode(t *testing.T) {
	m := newGoalModel(t)
	m.mode = modes.Chat
	if cmd := m.advanceGoal(doneMsg{mode: modes.Goal, text: "I will add the endpoint next", calls: 2, oks: 2, work: 1}); cmd != nil {
		t.Fatal("leaving goal mode must stop the run")
	}
	if m.goal.Status != goal.Cancelled {
		t.Fatalf("Status = %q, want cancelled", m.goal.Status)
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "left goal mode") {
		t.Fatalf("missing stop note: %v", m.msgs)
	}
}

func TestAdvanceGoalStallsOnProseOnlyTurn(t *testing.T) {
	m := newGoalModel(t)
	// A turn with no tool calls and no verdict did nothing. Another iteration
	// would only produce the same prose.
	prose := doneMsg{mode: modes.Goal, text: "- todo add \"add the endpoint\"\nGo to the next step.", calls: 0}
	if cmd := m.advanceGoal(prose); cmd != nil {
		t.Fatal("a prose-only turn must not be retried")
	}
	if m.goal.Status != goal.Stalled {
		t.Fatalf("Status = %q, want stalled", m.goal.Status)
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "described the work instead of calling tools") {
		t.Fatalf("no stall note: %v", m.msgs)
	}
}

// A small model will happily announce "all criteria satisfied" without having
// touched anything. Observed against qwen2.5-coder:3b: main.go untouched, a
// broken _test.go on disk, both todo steps still open — and GOAL MET.
func TestGoalMetIsCheckedNotBelieved(t *testing.T) {
	cases := []struct {
		name  string
		turn  doneMsg
		clear bool // finish the task list so a met claim can be credible
		want  goal.Status
		note  string
	}{
		{
			name: "no successful tool call",
			turn: doneMsg{mode: modes.Goal, text: "GOAL MET: all criteria satisfied", calls: 2, oks: 0},
			want: goal.UnearnedMet,
			note: "nothing was changed or run",
		},
		{
			// Bookkeeping is not work: todos ticked, files untouched.
			name:  "only bookkeeping",
			turn:  doneMsg{mode: modes.Goal, text: "GOAL MET: all criteria satisfied", calls: 4, oks: 4},
			clear: true,
			want:  goal.UnearnedMet,
			note:  "only planning/reading steps completed",
		},
		{
			name: "task list still open",
			turn: doneMsg{mode: modes.Goal, text: "GOAL MET: all criteria satisfied", calls: 4, oks: 4, work: 2},
			want: goal.UnearnedMet,
			note: "2 task-list step(s) still open",
		},
		{
			name:  "credible",
			turn:  doneMsg{mode: modes.Goal, text: "GOAL MET: both steps verified", calls: 4, oks: 4, work: 2},
			clear: true,
			want:  goal.Met,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			todo := &tools.TodoTool{Workdir: dir}
			mustRun(t, todo, `{"action":"add","text":"create main.go"}`)
			mustRun(t, todo, `{"action":"add","text":"create main_test.go"}`)
			if c.clear {
				mustRun(t, todo, `{"action":"done","id":1}`)
				mustRun(t, todo, `{"action":"done","id":2}`)
			}
			m := newGoalModel(t)
			m.workdir = dir
			if cmd := m.advanceGoal(c.turn); cmd != nil {
				t.Fatal("the run must not continue past a verdict")
			}
			if m.goal.Status != c.want {
				t.Fatalf("Status = %q, want %q", m.goal.Status, c.want)
			}
			out := strings.Join(m.msgs, "\n")
			if c.note != "" && !strings.Contains(out, c.note) {
				t.Fatalf("missing %q in: %s", c.note, out)
			}
			if c.want == goal.UnearnedMet && !strings.Contains(out, "goal NOT met") {
				t.Fatalf("a rejected GOAL MET must be reported: %s", out)
			}
		})
	}
}

func TestStopGoalRunIsIdempotent(t *testing.T) {
	m := newGoalModel(t)
	m.stopGoalRun()
	if m.goal.Status != goal.Cancelled {
		t.Fatalf("Status = %q, want cancelled", m.goal.Status)
	}
	if note := m.goalStopNote(); !strings.Contains(note, "cancelled after") {
		t.Fatalf("StopNote = %q", note)
	}
	// A second stop must not overwrite a verdict the model already gave.
	m.goal.Status = goal.Met
	m.stopGoalRun()
	if m.goal.Status != goal.Met {
		t.Fatalf("stop clobbered a met goal: %q", m.goal.Status)
	}
	bare := &Model{}
	bare.stopGoalRun()
	bare.goalStop()
	// A non-goal turn must not append an empty block to the transcript.
	quiet := newGoalModel(t)
	quiet.goal = nil
	quiet.goalStop()
	quiet.noteGoalStop()
	for _, msg := range quiet.msgs {
		if strings.TrimSpace(msg) == "" {
			t.Fatalf("empty transcript block: %q", msg)
		}
	}
}

func TestGoalCancelledTurnStopsRun(t *testing.T) {
	m := newGoalModel(t)
	m.cancelled = true
	if _, cmd := m.Update(doneMsg{mode: modes.Goal, text: "I will pick up the next step"}); cmd != nil {
		t.Fatal("a cancelled turn must not continue the run")
	}
	if m.goal.Status != goal.Cancelled {
		t.Fatalf("Status = %q, want cancelled", m.goal.Status)
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "cancelled after") {
		t.Fatalf("no cancellation note: %v", m.msgs)
	}
}

func TestGoalTodoBlockOrdersOpenStepsLast(t *testing.T) {
	dir := t.TempDir()
	if got := goalTodoBlock(dir); got != "" {
		t.Fatalf("empty list should render empty, got %q", got)
	}
	todo := &tools.TodoTool{Workdir: dir}
	mustRun(t, todo, `{"action":"add","text":"first step"}`)
	mustRun(t, todo, `{"action":"add","text":"second step"}`)
	mustRun(t, todo, `{"action":"done","id":1}`)
	block := goalTodoBlock(dir)
	first, second := strings.Index(block, "first step"), strings.Index(block, "second step")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("done steps should come first:\n%s", block)
	}
	if !strings.Contains(block, "[x] 1.") || !strings.Contains(block, "[ ] 2.") {
		t.Fatalf("bad marks:\n%s", block)
	}
	if err := tools.ClearTodos(dir); err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("clear: %v", err)
	}
}

func TestSetGoalClearsStaleTasks(t *testing.T) {
	m := newGoalModel(t)
	todo := &tools.TodoTool{Workdir: m.workdir}
	mustRun(t, todo, `{"action":"add","text":"leftover from earlier"}`)

	out := m.setGoal("  ship the release  ")
	if out != "" {
		t.Fatalf("setGoal should be quiet, got %q", out)
	}
	if m.mode != modes.Goal {
		t.Fatalf("mode = %q, want goal", m.mode)
	}
	if m.goal.Text != "ship the release" {
		t.Fatalf("Text = %q", m.goal.Text)
	}
	if got := goalTodoBlock(m.workdir); got != "" {
		t.Fatalf("stale task list survived: %q", got)
	}
	if !strings.Contains(strings.Join(m.msgs, "\n"), "cleared 1 task") {
		t.Fatalf("user was not told the list was cleared: %v", m.msgs)
	}
	if len(m.toasts) == 0 || !strings.Contains(m.toasts[0].msg, "goal started: ship the release") {
		t.Fatalf("no goal toast: %+v", m.toasts)
	}
}

func TestSetGoalWithoutArgsReports(t *testing.T) {
	m := newGoalModel(t)
	m.goal = nil
	out := m.setGoal("")
	if !strings.Contains(out, "No active goal") || !strings.Contains(out, "/goal <") {
		t.Fatalf("usage missing: %q", out)
	}
	m.setGoal("do the thing")
	out = m.setGoal("")
	if !strings.Contains(out, "do the thing") || !strings.Contains(out, "iter 0/12") {
		t.Fatalf("goal state missing: %q", out)
	}
}

func TestSidebarShowsGoal(t *testing.T) {
	m := newGoalModel(t)
	if s := m.sidebar(24); !strings.Contains(s, "iter 0/3") {
		t.Fatalf("goal should be present: %q", s)
	}
	bare := newGoalModel(t)
	bare.goal = nil
	if s := bare.sidebar(24); !strings.Contains(s, "no goal — /goal <text>") {
		t.Fatalf("empty goal state missing: %q", s)
	}
	m.goal.Status = goal.Met
	if s := m.sidebar(24); !strings.Contains(s, "met") {
		t.Fatalf("status missing: %q", s)
	}
}

func mustRun(t *testing.T, tl tools.Tool, args string) {
	t.Helper()
	if _, err := tl.Run(context.Background(), []byte(args)); err != nil {
		t.Fatalf("%s %s: %v", tl.Name(), args, err)
	}
}
