package goal

import (
	"strings"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	g := New("  ship the healthcheck  ", 0)
	if g.MaxIter != 12 {
		t.Fatalf("MaxIter = %d, want 12", g.MaxIter)
	}
	if g.Text != "ship the healthcheck" {
		t.Fatalf("Text = %q, want trimmed", g.Text)
	}
	if g.Status != Active || g.Iter != 0 {
		t.Fatalf("fresh goal = %+v", g)
	}
	if !g.Running() {
		t.Fatal("fresh goal should be running")
	}
	if g.Stop() {
		t.Fatal("fresh goal should not be stopped")
	}
}

func TestNextExhaustsBudget(t *testing.T) {
	g := New("do it", 3)
	for i := 1; i <= 2; i++ {
		if !g.Next() {
			t.Fatalf("iteration %d should continue", i)
		}
		if g.Iter != i {
			t.Fatalf("Iter = %d, want %d", g.Iter, i)
		}
	}
	if g.Exceeded() {
		t.Fatal("budget not used up yet")
	}
	if g.Next() {
		t.Fatal("third Next should exhaust the budget")
	}
	if !g.Exceeded() || g.Status != Spent {
		t.Fatalf("exhausted goal = %+v", g)
	}
	if g.Running() {
		t.Fatal("spent goal must not run")
	}
	if !strings.Contains(g.StopNote(), "budget spent") {
		t.Fatalf("StopNote = %q", g.StopNote())
	}
}

func TestNextRespectsVerdict(t *testing.T) {
	g := New("do it", 5)
	g.Verdict("all done.\nGOAL MET")
	if g.Next() {
		t.Fatal("a met goal must not continue")
	}
	if g.Status != Met {
		t.Fatalf("Status = %q, want met", g.Status)
	}
	if g.Iter != 1 {
		t.Fatalf("Iter = %d, want 1", g.Iter)
	}
	if !strings.Contains(g.StopNote(), "goal met after 1 iteration") {
		t.Fatalf("StopNote = %q", g.StopNote())
	}
}

func TestVerdict(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   Status
	}{
		{"met", "Added the endpoint.\nGOAL MET", Met},
		{"met lowercase", "added it\ngoal met", Met},
		{"blocked", "I need a database URL.\nGOAL BLOCKED: no DSN available", Blocked},
		{"neither", "I updated main.go and added a test.", Active},
		{"narrated progress only", "I will now add the endpoint on the next turn.", Active},
		{"explicit met", "done\nGOAL_STATUS: met", Met},
		{"explicit blocked", "stuck\nGOAL_STATUS: blocked", Blocked},
		{"explicit aliases", "finished\nGOAL_STATUS: DONE", Met},
		{"explicit complete", "finished\nGOAL_STATUS: completed", Met},
		{"met wins over prose", "GOAL MET\nI could not re-verify the last step", Met},
		{"blocked wins over met", "GOAL MET\nGOAL BLOCKED: cannot verify", Blocked},
		{"empty", "", Active},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := New("g", 4)
			if got := g.Verdict(c.answer); got != c.want {
				t.Fatalf("Verdict(%q) = %q, want %q", c.answer, got, c.want)
			}
			if g.Status != c.want {
				t.Fatalf("Status = %q, want %q", g.Status, c.want)
			}
		})
	}
}

func TestVerdictNilGoal(t *testing.T) {
	var g *Goal
	if g.Running() || g.Stop() || g.Next() || g.Exceeded() {
		t.Fatal("nil goal must be inert")
	}
	if g.Verdict("GOAL MET") != Active {
		t.Fatal("nil goal has no status to change")
	}
	if g.PromptBlock("", "") != "" || g.Summary() != "no goal" || g.StopNote() != "" {
		t.Fatal("nil goal must render as empty")
	}
}

func TestBlockedStopsRun(t *testing.T) {
	g := New("g", 4)
	g.Verdict("GOAL BLOCKED: needs a decision from the user")
	if !g.Stop() {
		t.Fatal("blocked goal should stop")
	}
	if g.Next() {
		t.Fatal("blocked goal must not continue")
	}
	if !strings.Contains(g.StopNote(), "blocked after") {
		t.Fatalf("StopNote = %q", g.StopNote())
	}
}

func TestPromptBlock(t *testing.T) {
	g := New("add /healthz returning 200", 4)
	g.Iter = 2
	block := g.PromptBlock("[ ] 1. add handler\n[x] 2. wire route", "")
	for _, want := range []string{
		"ACTIVE GOAL", "iteration 3 of 4", "add /healthz returning 200",
		"[ ] 1. add handler", "[x] 2. wire route", "BUDGET",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
	if withPlan := g.PromptBlock("", "1. do a\n2. do b"); !strings.Contains(withPlan, "APPROVED PLAN") {
		t.Fatalf("pending plan missing:\n%s", withPlan)
	}
	empty := New("g", 2).PromptBlock("", "")
	if !strings.Contains(empty, "empty — create the steps with todo add") {
		t.Fatalf("empty list should be called out:\n%s", empty)
	}
}

func TestContinuationIsSystemShaped(t *testing.T) {
	g := New("g", 5)
	g.Iter = 1
	c := g.Continuation()
	for _, want := range []string{"iteration 2 of 5", "No user is watching", "GOAL MET", "GOAL BLOCKED", "Do not restate the goal", "Do not imitate a tool result"} {
		if !strings.Contains(c, want) {
			t.Fatalf("continuation missing %q: %s", want, c)
		}
	}
	// A continuation nudge that offers a third way out teaches the model to
	// narrate instead of acting.
	if strings.Contains(c, "CONTINUE:") {
		t.Fatalf("continuation must not offer a continue marker: %s", c)
	}
}

// The verdict is a claim; the evidence decides. A claim the evidence does not
// support does not end the run — the work is still owed — but it is recorded
// and reported. All of these failure modes were seen in testing with
// qwen2.5-coder:3b.
func TestReconcileChecksTheClaim(t *testing.T) {
	worked := Evidence{Calls: 4, SucceededCalls: 4, WorkCalls: 2}
	cases := []struct {
		name     string
		answer   string
		ev       Evidence
		want     Status
		rejected bool
	}{
		{"credible met", "done\nGOAL MET", worked, Met, false},
		{
			// The exact false positive seen in testing: a 3b model created a
			// two-item task list, marked both done, changed nothing, and said
			// "GOAL MET". Todo calls are bookkeeping, not work.
			name:     "met after only bookkeeping",
			answer:   "GOAL MET",
			ev:       Evidence{Calls: 4, SucceededCalls: 4, WorkCalls: 0},
			want:     Active,
			rejected: true,
		},
		{
			// A turn that wrote greeting.txt fine but errored on main.go
			// still claimed GOAL MET for both — recoverable, so it retries.
			name:     "met with a failed call",
			answer:   "GOAL MET",
			ev:       Evidence{Calls: 3, SucceededCalls: 2, WorkCalls: 1, FailedCalls: 1},
			want:     Active,
			rejected: true,
		},
		{
			name:     "met with open steps",
			answer:   "GOAL MET",
			ev:       Evidence{Calls: 4, SucceededCalls: 4, WorkCalls: 2, OpenSteps: 2},
			want:     Active,
			rejected: true,
		},
		{"met with nothing at all", "GOAL MET", Evidence{}, Active, true},
		{"prose only", "I will do that next turn.", Evidence{}, Stalled, false},
		{"prose after real work", "Step 1 done, starting step 2.", worked, Active, false},
		{"blocked needs no evidence", "GOAL BLOCKED: no DSN", Evidence{}, Blocked, false},
		{"failing calls still narrate", "trying again shortly", Evidence{Calls: 3, SucceededCalls: 0}, Active, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := New("g", 4)
			if got := g.Reconcile(c.answer, c.ev); got != c.want {
				t.Fatalf("Reconcile = %q, want %q", got, c.want)
			}
			if g.Rejected() != c.rejected {
				t.Fatalf("Rejected() = %v, want %v", g.Rejected(), c.rejected)
			}
			// A run continues while the goal is still owed, unless the model
			// gave up or the budget is gone.
			if more := g.Next(); more != (c.want == Active) {
				t.Fatalf("Next() = %v for status %q", more, c.want)
			}
		})
	}
}

func TestRejectionIsReportedAndExplained(t *testing.T) {
	g := New("ship it", 4)
	g.Reconcile("GOAL MET: everything is done", Evidence{})
	note := g.RejectionNote()
	if !strings.Contains(note, "GOAL MET rejected") {
		t.Fatalf("RejectionNote = %q", note)
	}
	if !strings.Contains(note, "no tool was called at all") {
		t.Fatalf("RejectionNote should name the reason: %q", note)
	}
	if !strings.Contains(note, "continuing") {
		t.Fatalf("RejectionNote should say the run continues: %q", note)
	}
	// A turn that called nothing at all says so.
	g0 := New("ship it", 4)
	g0.Reconcile("GOAL MET", Evidence{Calls: 4, SucceededCalls: 4, WorkCalls: 0})
	if n := g0.RejectionNote(); !strings.Contains(n, "nothing was changed or run") {
		t.Fatalf("RejectionNote = %q", n)
	}
	// Both reasons are reported when both apply.
	g2 := New("ship it", 4)
	g2.Reconcile("GOAL MET", Evidence{Calls: 3, SucceededCalls: 0, OpenSteps: 1})
	n := g2.RejectionNote()
	if !strings.Contains(n, "nothing was changed or run") || !strings.Contains(n, "1 task-list step(s) still open") {
		t.Fatalf("RejectionNote = %q", n)
	}
	// A partial failure is named as such, not as "nothing succeeded".
	g3 := New("ship it", 4)
	g3.Reconcile("GOAL MET", Evidence{Calls: 3, SucceededCalls: 2, WorkCalls: 1, FailedCalls: 1})
	if n := g3.RejectionNote(); !strings.Contains(n, "1 tool call(s) failed in the turn that claimed it") {
		t.Fatalf("RejectionNote = %q", n)
	}
	// A clean turn clears a previous rejection, so notes are never stale.
	g.Reconcile("still working", worked2())
	if g.Rejected() || g.RejectionNote() != "" {
		t.Fatalf("a rejected flag leaked into the next turn: %q", g.RejectionNote())
	}
}

// worked2 is a turn that genuinely changed something.
func worked2() Evidence { return Evidence{Calls: 2, SucceededCalls: 2, WorkCalls: 1} }

func TestStopNoteCarriesTheLastRejection(t *testing.T) {
	g := New("ship it", 2)
	g.Reconcile("GOAL MET", Evidence{Calls: 2, SucceededCalls: 2, WorkCalls: 0})
	g.Next() // iteration 1, still owed
	g.Reconcile("GOAL MET", Evidence{Calls: 2, SucceededCalls: 2, WorkCalls: 0})
	if g.Next() {
		t.Fatal("the budget should be spent")
	}
	if g.Status != Spent {
		t.Fatalf("Status = %q, want budget_exhausted", g.Status)
	}
	note := g.StopNote()
	if !strings.Contains(note, "budget spent") {
		t.Fatalf("StopNote = %q", note)
	}
	if !strings.Contains(note, "last GOAL MET claim was rejected") {
		t.Fatalf("the final report must say the claim was never credible: %q", note)
	}
}

func TestMetDoesNotCarryAStaleRejection(t *testing.T) {
	g := New("ship it", 4)
	g.Reconcile("GOAL MET", Evidence{Calls: 1})
	g.Reconcile("GOAL MET", Evidence{Calls: 3, SucceededCalls: 3, WorkCalls: 2})
	if g.Status != Met {
		t.Fatalf("Status = %q, want met", g.Status)
	}
	if strings.Contains(g.StopNote(), "rejected") {
		t.Fatalf("a met run must not report a stale rejection: %q", g.StopNote())
	}
}

func TestIsWorkTool(t *testing.T) {
	// Tools that change something (or run something) count as work.
	for _, n := range []string{"write", "create", "edit", "patch", "run", "testgen", "bash", "git", "delete"} {
		if !IsWorkTool(n) {
			t.Fatalf("%s should count as work", n)
		}
	}
	// Bookkeeping and observation must never count as work: they succeed just
	// as easily while the goal is untouched.
	for _, n := range []string{"todo", "memory", "summary", "read", "grep", "glob", "tree", "changes", "security"} {
		if IsWorkTool(n) {
			t.Fatalf("%s must not count as work", n)
		}
	}
	if IsWorkTool("nope") || IsWorkTool("") {
		t.Fatal("unknown tools are not work")
	}
}

// A model can finish the work and then stop calling tools. The report must
// describe what actually changed, not assert that nothing happened — observed
// against qwen2.5-coder:3b, which wrote a correct function and its test file
// and was then reported as "nothing was done".
func TestStopNoteReportsRealWork(t *testing.T) {
	cases := []struct {
		name    string
		maxIter int
		turns   []Evidence
		want    []string
		notWant []string
	}{
		{
			name:    "work done, list closed, then a stall",
			maxIter: 3,
			turns: []Evidence{
				{Calls: 2, SucceededCalls: 2, WorkCalls: 2}, // real work, no verdict
				{}, // then the model stopped calling tools
			},
			want: []string{
				"the model stopped calling tools",
				"2 change(s) were made",
				"every task-list step is closed",
				"review the diff",
			},
			notWant: []string{"nothing was done"},
		},
		{
			name:    "work done, steps still open, then a stall",
			maxIter: 3,
			turns: []Evidence{
				{Calls: 2, SucceededCalls: 2, WorkCalls: 1, OpenSteps: 2},
				{OpenSteps: 2}, // still two steps open when it stalled
			},
			want:    []string{"1 change(s) were made", "2 task-list step(s) were still open"},
			notWant: []string{"nothing was done", "may already be done"},
		},
		{
			name:    "no work at all, then a stall",
			maxIter: 3,
			turns:   []Evidence{{}},
			want:    []string{"described the work instead of calling tools", "nothing was done"},
			notWant: []string{"change(s) were made"},
		},
		{
			name:    "work done, list closed, budget spent",
			maxIter: 2,
			turns:   []Evidence{{Calls: 2, SucceededCalls: 2, WorkCalls: 3}, {Calls: 1, SucceededCalls: 1, WorkCalls: 1}},
			want:    []string{"budget spent", "4 change(s) were made", "may already be done"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := New("ship it", c.maxIter)
			for _, ev := range c.turns {
				g.Reconcile("I'll finish the rest shortly", ev)
				if g.Next() {
					continue
				}
			}
			note := g.StopNote()
			if note == "" {
				t.Fatal("a finished run must report something")
			}
			for _, w := range c.want {
				if !strings.Contains(note, w) {
					t.Fatalf("StopNote = %q\nmissing %q", note, w)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(note, w) {
					t.Fatalf("StopNote = %q\nshould not contain %q", note, w)
				}
			}
		})
	}
}

func TestStalledStopNote(t *testing.T) {
	g := New("g", 5)
	g.Status = Stalled
	g.Iter = 2
	if !g.Stop() {
		t.Fatal("a stalled run is stopped")
	}
	note := g.StopNote()
	if !strings.Contains(note, "described the work instead of calling tools") {
		t.Fatalf("StopNote = %q", note)
	}
	if !strings.Contains(note, "after 2 iteration") {
		t.Fatalf("StopNote = %q", note)
	}
	if g.Running() {
		t.Fatal("stalled run must not continue")
	}
}

func TestSummaryAndStopNotes(t *testing.T) {
	g := New("a very long goal statement that would otherwise wrap across the whole sidebar and push everything else out of view entirely", 3)
	if !strings.Contains(g.Summary(), "…") {
		t.Fatalf("long goal should truncate: %s", g.Summary())
	}
	if !strings.Contains(g.Summary(), "iter 0/3") {
		t.Fatalf("summary missing progress: %s", g.Summary())
	}
	g.Status = Cancelled
	if !strings.Contains(g.StopNote(), "cancelled after") {
		t.Fatalf("StopNote = %q", g.StopNote())
	}
	if g.StopNote() == "" {
		t.Fatal("cancelled goal should say so")
	}
}
