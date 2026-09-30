package cost

import (
	"testing"
	"time"
)

// Benchmarks for the spend tracker.
//
// It runs on every turn - and twice per comparison row in /different - and it is
// the thing a daily budget is enforced against, so its cost is part of the cost of
// a turn and its correctness is what a limit rests on.

// benchTracker is a Tracker with no database, so the measurement includes the
// JSON-file fallback rather than SQLite. That is the honest default: Add
// persists, and a benchmark that skipped persistence would be measuring
// something the product does not do.
func benchTracker(tb testing.TB) *Tracker {
	tb.Helper()
	tr := &Tracker{days: map[string]*dayEntry{}}
	// Into the test's own temp dir, so thousands of benchmark iterations do not
	// leave thousands of files behind in the system temp directory.
	tr.file = tb.TempDir() + "/cost.json"
	return tr
}

// Add rewrites its whole history on every call: persist marshals the days map
// and writes it. So its cost is O(days recorded), not O(1) - measured below, and
// reported rather than fixed here because the fix is a storage strategy change
// rather than a code tidy.
//
// The per-turn number today is fine - a turn takes seconds - but it is the one
// place in the cost path that grows without bound, and /different calls Add five
// times in a row for one user action.
func BenchmarkAdd(b *testing.B) {
	for _, bc := range []struct {
		name string
		prov string
		p, c int
	}{
		// A chat turn on a cloud provider.
		{"gemini_chat", "gemini", 1200, 340},
		// A tool-heavy turn: the prompt is re-sent every round, so these add up
		// over eight rounds of a long session.
		{"gemini_deep", "gemini", 24000, 1800},
		// A local model costs nothing, but the accounting still runs.
		{"ollama", "ollama", 8000, 900},
	} {
		b.Run(bc.name, func(b *testing.B) {
			tr := benchTracker(b)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tr.Add(bc.prov, bc.p, bc.c)
			}
		})
	}
}

// The lookup behind every budget check. It takes a lock and copies a value, so
// it is called several times per turn: at the start, and again at each goal
// iteration boundary.
func BenchmarkToday(b *testing.B) {
	tr := benchTracker(b)
	tr.Add("gemini", 1000, 100)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tr.Today()
	}
}

// A budget check builds a Budget and reads the day total, which is what the TUI
// does before every turn and the headless runner does before every iteration.
func BenchmarkExceededCheck(b *testing.B) {
	tr := benchTracker(b)
	tr.Add("gemini", 1000, 100)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bud := NewBudget(tr, 100)
		if bud.Exceeded() {
			b.Fatal("a budget of 100 should not be exceeded")
		}
	}
}

// A tracker accumulates a map entry per day, so its cost grows with how long
// someone has been using ycode - not with how many turns. This is the axis that
// decides whether a year-old install is still fast.
func BenchmarkAddWithManyDaysRecorded(b *testing.B) {
	for _, days := range []int{1, 30, 365} {
		b.Run(itoa(days), func(b *testing.B) {
			tr := benchTracker(b)
			for d := 0; d < days; d++ {
				tr.days[dayKey(d)] = &dayEntry{USD: float64(d), PromptTok: 1000}
			}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tr.Add("gemini", 100, 10)
			}
		})
	}
}

func dayKey(d int) string {
	return time.Now().AddDate(0, 0, -d).Format("2006-01-02")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
