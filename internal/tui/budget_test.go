package tui

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/goal"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
)

// redirectConfig points config.Dir() at a temp dir for the duration of the
// test. Without this, /budget's save would rewrite the developer's real
// ~/.ycode/config.yaml — a test that changes a machine's configuration is
// worse than no test.
//
// db.Shared is primed first, deliberately. It resolves its path from
// config.Dir() on first use and caches the open handle for the life of the
// process, so redirecting the env before anything calls it would create a
// database inside the temp dir that nothing can close — TempDir's cleanup then
// fails on a locked file. Priming it first means the temp dir only ever holds
// the config file, which is the thing under test.
func redirectConfig(t *testing.T) {
	t.Helper()
	db.Shared()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows
	if got := config.Dir(); filepath.Dir(got) != dir {
		t.Fatalf("config.Dir() = %q, expected it under %q", got, dir)
	}
}

// budgetModel is a Model with just enough wired to exercise the budget.
//
// The router is deliberately nil: if a blocked turn ever escapes to a provider
// the test panics, which is a louder failure than a command that quietly comes
// back nil. Tests that expect the turn to be allowed therefore assert on the
// absence of the stop message rather than on the returned command.
func budgetModel(t *testing.T, limit float64) *Model {
	t.Helper()
	redirectConfig(t)
	tr := cost.NewEphemeral()
	cfg := config.Defaults()
	cfg.DailyBudgetUSD = limit
	return &Model{
		cfg: cfg, tracker: tr, th: theme.Dark(),
		budget:  cost.NewBudget(tr, limit),
		sess:    &sessions.Session{},
		workdir: t.TempDir(),
		mode:    modes.Chat,
	}
}

func (m *Model) transcript() string { return strings.Join(m.msgs, "\n") }

func runSlash(t *testing.T, m *Model, line string) string {
	t.Helper()
	name, args, _ := strings.Cut(strings.TrimSpace(line), " ")
	h, ok := slashRegistry()[name]
	if !ok {
		t.Fatalf("no such command %q", name)
	}
	out, _ := h(context.Background(), m, strings.TrimSpace(args))
	return out
}

// runsWithoutPanic executes fn, swallowing a panic.
//
// The Model built here has no router, so a turn that is allowed to proceed
// panics partway through. That is useful: it means the budget let the turn
// through, which is exactly what the "allowed" tests are checking. The panic is
// contained in a closure so the assertions after the call still run — a bare
// deferred recover in the test body would unwind past them and turn every one
// of these tests into a silent pass.
func runsWithoutPanic(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// spendTo records a real metered turn rather than poking a float in, so this
// goes down the same path the app does.
//
// Token counts are derived from the real price table instead of being guessed:
// pricing is per thousand tokens, so a hardcoded "1.00 dollars is 10000 tokens"
// would silently record a fraction of a cent and every assertion below would
// pass for the wrong reason.
func spendTo(t *testing.T, m *Model, usd float64) {
	t.Helper()
	if _, _, after := m.tracker.Today(); after != 0 {
		t.Fatalf("tracker started at $%v, not 0", after)
	}
	per1k, _ := providers.Pricing("gemini")
	if per1k <= 0 {
		t.Fatal("gemini has no prompt price, so spend cannot be recorded")
	}
	tok := int(usd / per1k * 1000)
	if got := m.tracker.Add("gemini", tok, 0); math.Abs(got-usd) > usd/100 {
		t.Fatalf("asked for $%v, recorded $%v (%d tokens at $%v/1k)", usd, got, tok, per1k)
	}
}

// --- the hard stop ---

// The whole point of P2c: a turn that would begin over budget must not begin.
//
// The spend is comfortably past the limit rather than exactly on it. Testing
// the boundary from token counts would be testing float arithmetic, and the
// cost package already covers >= at the limit where the amounts are exact.
func TestTurnIsRefusedOverBudget(t *testing.T) {
	m := budgetModel(t, 1.00)
	spendTo(t, m, 1.20)

	if cmd := m.startTurn("hello", turnOpts{}); cmd != nil {
		t.Fatal("startTurn returned a command while over budget: the turn was not stopped")
	}
	if !strings.Contains(m.transcript(), "daily budget reached") {
		t.Fatalf("no explanation given: %q", m.transcript())
	}
}

// A budget that stops work early is worse than no budget at all.
func TestTurnIsAllowedUnderBudget(t *testing.T) {
	m := budgetModel(t, 1.00)
	spendTo(t, m, 0.50)
	runsWithoutPanic(func() { m.startTurn("hello", turnOpts{}) })
	if strings.Contains(m.transcript(), "daily budget") {
		t.Fatalf("stopped a turn that was under budget: %q", m.transcript())
	}
}

func TestNoBudgetIsNeverAStop(t *testing.T) {
	m := budgetModel(t, 0)
	spendTo(t, m, 1e3)
	runsWithoutPanic(func() { m.startTurn("hello", turnOpts{}) })
	if strings.Contains(m.transcript(), "budget") {
		t.Fatalf("an unbudgeted session mentioned a budget: %q", m.transcript())
	}
}

// Repeating the message on every blocked turn makes a deliberate limit look
// like a fault, so it is given once.
func TestBlockedMessageIsGivenOnce(t *testing.T) {
	m := budgetModel(t, 1.00)
	spendTo(t, m, 1.20)
	m.startTurn("one", turnOpts{})
	m.startTurn("two", turnOpts{})
	m.startTurn("three", turnOpts{})
	if n := strings.Count(m.transcript(), "daily budget reached"); n != 1 {
		t.Fatalf("message appeared %d times, want 1", n)
	}
}

// The default config must leave every session exactly as it was.
func TestDefaultConfigHasNoBudget(t *testing.T) {
	if got := config.Defaults().DailyBudgetUSD; got != 0 {
		t.Fatalf("default daily budget = %v, want 0", got)
	}
}

// --- goal runs ---

// This is the case that justifies the feature. A goal run is unattended and
// iterates on its own, so a turn-level stop alone would end it a step late and
// without saying why.
func TestGoalRunStopsAtTheLimit(t *testing.T) {
	m := budgetModel(t, 1.00)
	m.mode = modes.Goal
	m.goal = goal.New("ship it", 10)
	m.goal.Status = goal.Active
	spendTo(t, m, 1.20)

	if cmd := m.advanceGoal(doneMsg{mode: modes.Goal, text: "made progress", calls: 1, oks: 1}); cmd != nil {
		t.Fatal("advanceGoal scheduled another iteration while over budget")
	}
	out := m.transcript()
	if !strings.Contains(out, "goal run stopped") || !strings.Contains(out, "daily budget") {
		t.Fatalf("goal run was not stopped with an explanation: %q", out)
	}
	if m.goal.Running() {
		t.Fatal("goal is still marked running after the budget stop")
	}
}

func TestGoalRunContinuesUnderBudget(t *testing.T) {
	m := budgetModel(t, 1.00)
	m.mode = modes.Goal
	m.goal = goal.New("ship it", 10)
	m.goal.Status = goal.Active
	spendTo(t, m, 0.50)

	// The iteration line is written before the next turn starts, so it is the
	// record that the budget check let the run continue.
	runsWithoutPanic(func() {
		m.advanceGoal(doneMsg{mode: modes.Goal, text: "made progress", calls: 1, oks: 1})
	})
	out := m.transcript()
	if !strings.Contains(out, "goal iteration") {
		t.Fatalf("an under-budget run did not continue: %q", out)
	}
	if strings.Contains(out, "budget") {
		t.Fatalf("an under-budget run mentioned a budget: %q", out)
	}
}

// --- the /budget command ---

func TestBudgetCommandReportsTheLimit(t *testing.T) {
	m := budgetModel(t, 1.50)
	spendTo(t, m, 0.75)
	out := runSlash(t, m, "/budget")
	for _, want := range []string{"1.5000", "0.7500", "50%"} {
		if !strings.Contains(out, want) {
			t.Errorf("/budget does not mention %q: %q", want, out)
		}
	}
}

func TestBudgetCommandSaysUnlimitedWhenUnset(t *testing.T) {
	m := budgetModel(t, 0)
	out := runSlash(t, m, "/budget")
	if !strings.Contains(out, "no daily budget") || !strings.Contains(out, "unlimited") {
		t.Fatalf("unset budget not reported clearly: %q", out)
	}
	if !strings.Contains(out, "/budget 1.00") {
		t.Fatalf("no hint at how to set one: %q", out)
	}
}

// A bad number must not become a limit. Silently storing 0 would turn a typo
// into unlimited spend, which is the failure this guards.
func TestBudgetCommandRejectsBadInput(t *testing.T) {
	for _, bad := range []string{"abc", "-1", "1.2.3"} {
		m := budgetModel(t, 0)
		out := runSlash(t, m, "/budget "+bad)
		if !strings.Contains(out, "usage") {
			t.Errorf("/budget %q did not report usage: %q", bad, out)
		}
		if m.cfg.DailyBudgetUSD != 0 {
			t.Errorf("/budget %q changed the limit to %v", bad, m.cfg.DailyBudgetUSD)
		}
	}
}

func TestBudgetCommandSetsAndPersists(t *testing.T) {
	m := budgetModel(t, 0)
	if out := runSlash(t, m, "/budget 2.00"); !strings.Contains(out, "2.0000") {
		t.Fatalf("/budget 2.00 reported %q", out)
	}
	if m.cfg.DailyBudgetUSD != 2.00 {
		t.Fatalf("limit = %v, want 2", m.cfg.DailyBudgetUSD)
	}
	if m.budget.Limit() != 2.00 {
		t.Fatalf("live budget limit = %v, want 2", m.budget.Limit())
	}
	if !strings.Contains(runSlash(t, m, "/budget"), "2.0000") {
		t.Fatal("the new limit is not visible afterwards")
	}
}

// A limit that does not survive a restart is a limit that does not exist.
func TestBudgetSurvivesAReload(t *testing.T) {
	m := budgetModel(t, 0)
	if out := runSlash(t, m, "/budget 3.00"); !strings.Contains(out, "$3.0000") {
		t.Fatalf("/budget reported %q", out)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.DailyBudgetUSD != 3.00 {
		t.Fatalf("reloaded limit = %v, want 3", got.DailyBudgetUSD)
	}
}

// Raising the limit has to unblock work in the same session, or lowering a
// limit to a typo would need a restart to undo.
func TestRaisingTheLimitClearsTheLatch(t *testing.T) {
	m := budgetModel(t, 1.00)
	spendTo(t, m, 1.20)
	m.startTurn("blocked", turnOpts{})
	if !m.budgetWarned {
		t.Fatal("expected the limit to have been reported")
	}
	runSlash(t, m, "/budget 5.00")
	if m.budgetWarned {
		t.Fatal("the latch survived raising the limit")
	}
	if m.budget.Exceeded() {
		t.Fatal("still over the new limit")
	}
}

// --- the sidebar ---

// The limit is only useful if it can be watched before it is hit.
func TestSidebarShowsTheBudgetOnlyWhenSet(t *testing.T) {
	m := budgetModel(t, 2.00)
	spendTo(t, m, 1.00)
	with := m.sidebar(40)
	if !strings.Contains(with, "budget") || !strings.Contains(with, "50%") {
		t.Fatalf("sidebar does not show the limit and usage: %q", with)
	}
	if strings.Contains(budgetModel(t, 0).sidebar(40), "budget $") {
		t.Fatal("an unbudgeted sidebar shows a budget row")
	}
}

// A bar at 100% that looks like one at 95% tells the user nothing they can act
// on, so the reached state is spelled out.
func TestSidebarSaysReached(t *testing.T) {
	m := budgetModel(t, 1.00)
	spendTo(t, m, 1.50)
	if !strings.Contains(m.sidebar(40), "REACHED") {
		t.Fatalf("sidebar does not report the limit as reached: %q", m.sidebar(40))
	}
}
