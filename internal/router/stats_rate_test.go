package router

import (
	"math"
	"testing"
	"time"
)

// NewStats loads from the shared on-disk database, so a Stats built without
// isolation inherits whatever a previous run recorded - including a real
// developer's provider history. Every test here isolates first, which is what
// makes the exact-value assertions below meaningful rather than accidental.
func newIsolatedStats(t *testing.T) *Stats {
	t.Helper()
	isolate(t)
	return NewStats()
}

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The two candidates disagree, and the difference is the whole bug: mean of
// per-call rates versus total tokens over total time. A short fast call and a
// long slow one give completely different answers, so the definition has to be
// pinned rather than assumed.
func TestAvgTokSIsMeanOfPerCallRates(t *testing.T) {
	s := newIsolatedStats(t)
	// 100 tok/s for 1s, then 300 tok/s for 1s. Mean of rates = 200.
	// Cumulative tokens / cumulative seconds = 400/2 = 200 too, so this case
	// cannot tell them apart; the next one can.
	s.Record("p", time.Second, 100, true)
	s.Record("p", time.Second, 300, true)
	approx(t, s.ByProvider["p"].AvgTokS, 200)
}

// One 10s call making 1000 tokens, then nine instant failures.
//   - mean of rates:                          100 tok/s (only one call had tokens)
//   - cumulative tokens / cumulative seconds: 1000/10 = 100 tok/s as well
//
// Identical again, because the failures contributed no time. The case that
// actually separates them needs unequal token counts over unequal times.
func TestAvgTokSIsNotCumulativeThroughput(t *testing.T) {
	s := newIsolatedStats(t)
	// call 1: 10 tok/s (10 tokens in 1s).  call 2: 100 tok/s (1000 in 10s).
	// mean of rates      = (10 + 100) / 2 = 55
	// cumulative ratio   = 1010 / 11     = 91.8
	s.Record("p", time.Second, 10, true)
	s.Record("p", 10*time.Second, 1000, true)
	st := s.ByProvider["p"]
	approx(t, st.AvgTokS, 55)
	if math.Abs(st.AvgTokS-1010.0/11.0) < 1e-6 {
		t.Fatal("AvgTokS is still cumulative throughput, not the mean of rates")
	}
}

// A failed call produced no tokens, so it has no rate. Averaging in a zero
// would describe the failure count as if it were slow generation.
func TestFailedCallsDoNotDragAverageToZero(t *testing.T) {
	s := newIsolatedStats(t)
	s.Record("p", time.Second, 500, true) // 500 tok/s
	for i := 0; i < 9; i++ {
		s.Record("p", time.Millisecond, 0, false) // instant failures
	}
	st := s.ByProvider["p"]
	approx(t, st.AvgTokS, 500)
	if st.Failures != 9 {
		t.Fatalf("failures = %d, want 9", st.Failures)
	}
	if st.Calls != 10 {
		t.Fatalf("calls = %d, want 10", st.Calls)
	}
	// The raw counters still add up, whatever the derived figure does.
	if st.Tokens != 500 {
		t.Fatalf("tokens = %d, want 500", st.Tokens)
	}
}

func TestFastestRateIsTracked(t *testing.T) {
	s := newIsolatedStats(t)
	s.Record("p", 2*time.Second, 100, true) // 50
	s.Record("p", 1*time.Second, 900, true) // 900
	s.Record("p", 4*time.Second, 200, true) // 50
	approx(t, s.ByProvider["p"].FastestTokS, 900)
}

func TestZeroDurationDoesNotDivideByZero(t *testing.T) {
	s := newIsolatedStats(t)
	s.Record("p", 0, 100, true)
	st := s.ByProvider["p"]
	if st.AvgTokS != 0 {
		t.Fatalf("AvgTokS = %v, want 0 for an instantaneous call", st.AvgTokS)
	}
	if math.IsNaN(st.AvgTokS) || math.IsInf(st.AvgTokS, 0) {
		t.Fatalf("AvgTokS is not finite: %v", st.AvgTokS)
	}
}

// A restart must not quietly change the figure the user has been reading, so
// the accumulated rate sum is persisted rather than a running mean that would
// be recomputed from whatever survived.
func TestAvgTokSSurvivesReload(t *testing.T) {
	isolate(t)
	s := NewStats()
	s.Record("p", time.Second, 10, true)
	s.Record("p", 10*time.Second, 1000, true)
	want := s.ByProvider["p"].AvgTokS

	reloaded := NewStats()
	got, ok := reloaded.ByProvider["p"]
	if !ok {
		t.Fatalf("provider stats did not survive a reload: %+v", reloaded.ByProvider)
	}
	approx(t, got.AvgTokS, want)
}

func TestSummaryIncludesProvider(t *testing.T) {
	s := newIsolatedStats(t)
	s.Record("ollama", time.Second, 42, true)
	if got := s.Summary(); got == "no latency data yet" {
		t.Fatalf("unexpected empty summary: %q", got)
	}
}

func TestNoDataSummary(t *testing.T) {
	s := newIsolatedStats(t)
	if got := s.Summary(); got != "no latency data yet" {
		t.Fatalf("got %q", got)
	}
}
