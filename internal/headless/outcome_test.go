package headless

import "testing"

func TestOutcomeExitCodes(t *testing.T) {
	// 0 must mean the work is done, and only that.
	for _, o := range []Outcome{OutcomeNone, OutcomeMet} {
		if got := o.ExitCode(); got != 0 {
			t.Fatalf("%s should exit 0, got %d", o, got)
		}
	}
	want := map[Outcome]int{
		OutcomeBlocked:    2,
		OutcomeExhausted:  3,
		OutcomeStalled:    4,
		OutcomeCancelled:  5,
		OutcomeUnverified: 6,
		Outcome("weird"):  6,
	}
	for o, code := range want {
		if got := o.ExitCode(); got != code {
			t.Fatalf("%s exit = %d, want %d", o, got, code)
		}
	}
	// Every failing outcome must be distinct from 0, or CI reads a stall as a
	// pass.
	for o := range want {
		if o.ExitCode() == 0 {
			t.Fatalf("%s must not exit 0", o)
		}
	}
}

func TestGoalModeDispatchReportsOutcome(t *testing.T) {
	o := Options{Mode: "goal"}
	o.withDefaults()
	if o.GoalIters != 0 {
		t.Fatalf("withDefaults should not invent a goal budget: %d", o.GoalIters)
	}
	// A non-goal run has no pass/fail notion.
	if OutcomeNone.ExitCode() != 0 {
		t.Fatal("a plain turn must exit 0")
	}
}
