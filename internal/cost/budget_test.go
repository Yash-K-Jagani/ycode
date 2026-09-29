package cost

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// memTracker is a Tracker with no database behind it, so the budget can be
// tested against an exact spend instead of a priced one.
func memTracker() *Tracker {
	return &Tracker{days: map[string]*dayEntry{}}
}

func (t *Tracker) addUSD(usd float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := today()
	e := t.days[d]
	if e == nil {
		e = &dayEntry{}
		t.days[d] = e
	}
	e.USD += usd
}

func today() string { return time.Now().Format("2006-01-02") }

// --- unlimited ---

// Zero is the default, and for a local provider it is the only sensible value.
func TestZeroMeansUnlimited(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 0)
	if !b.Unlimited() {
		t.Fatal("a zero limit should be unlimited")
	}
	tr.addUSD(1e6)
	if b.Exceeded() {
		t.Fatal("an unlimited budget was exceeded")
	}
	if b.Remaining() != -1 {
		t.Fatalf("Remaining = %v, want -1 for unlimited", b.Remaining())
	}
	if b.Fraction() != 0 {
		t.Fatalf("Fraction = %v, want 0 for unlimited", b.Fraction())
	}
	if msg := b.BlockedMessage(); msg != "" {
		t.Fatalf("an unlimited budget produced a message: %q", msg)
	}
}

func TestNegativeLimitIsUnlimited(t *testing.T) {
	if !NewBudget(memTracker(), -5).Unlimited() {
		t.Fatal("a negative limit should be unlimited rather than always-exceeded")
	}
}

// A nil Budget is not a thing New produces, but callers hold one and a nil
// receiver should answer rather than panic.
func TestNilBudgetIsSafe(t *testing.T) {
	var b *Budget
	if !b.Unlimited() || b.Exceeded() || b.Remaining() != -1 || b.Limit() != 0 || b.Spent() != 0 {
		t.Fatal("a nil budget did not behave as unlimited")
	}
	if b.WillExceedAfter(1e9) {
		t.Fatal("a nil budget should never predict an overspend")
	}
}

// --- the stop ---

func TestExceededOnlyAtOrOverTheLimit(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 1.00)
	if b.Exceeded() {
		t.Fatal("nothing spent, but reported exceeded")
	}
	tr.addUSD(0.99)
	if b.Exceeded() {
		t.Fatal("0.99 of 1.00 should not be a stop")
	}
	tr.addUSD(0.01)
	// >= rather than >, so a limit set to exactly what you intend to spend
	// stops the next turn rather than allowing one more that takes you past it.
	if !b.Exceeded() {
		t.Fatal("exactly at the limit should be a stop")
	}
	tr.addUSD(0.01)
	if b.Remaining() != 0 {
		t.Fatalf("Remaining = %v once over, want 0 not a negative", b.Remaining())
	}
}

func TestFractionAndRemaining(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 2.00)
	tr.addUSD(0.50)
	if got := b.Fraction(); got != 0.25 {
		t.Fatalf("Fraction = %v, want 0.25", got)
	}
	if got := b.Remaining(); got != 1.50 {
		t.Fatalf("Remaining = %v, want 1.50", got)
	}
	if got := b.Spent(); got != 0.50 {
		t.Fatalf("Spent = %v", got)
	}
	tr.addUSD(99)
	// Clamped, so a meter does not overflow its bar.
	if got := b.Fraction(); got != 1 {
		t.Fatalf("Fraction = %v far past the limit, want 1", got)
	}
	if got := b.Remaining(); got != 0 {
		t.Fatalf("Remaining = %v far past the limit", got)
	}
}

// The message is the only thing the user sees when a turn is refused, so it has
// to carry the number and a way out.
func TestBlockedMessageIsActionable(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 1.50)
	tr.addUSD(1.60)
	msg := b.BlockedMessage()
	for _, want := range []string{"1.6000", "1.5000", "daily_budget_usd", "0", "local"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q: %q", want, msg)
		}
	}
}

// A soft limit that warns and then spends anyway is worse than none: the user
// has been told they are in control when they are not. So there is no warning
// mode, and WillExceedAfter is advisory only - it exists to let a turn be
// allowed but its result flagged, never to stop one on an estimate.
func TestWillExceedAfterIsAdvisoryOnly(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 1.00)
	tr.addUSD(0.90)
	if !b.WillExceedAfter(0.20) {
		t.Fatal("a turn that would cross the line should be predicted")
	}
	// But it must not have become a stop.
	if b.Exceeded() {
		t.Fatal("an estimate must not trigger the hard stop")
	}
	unlimited := NewBudget(memTracker(), 0)
	if unlimited.WillExceedAfter(1e9) {
		t.Fatal("an unlimited budget should never predict an overspend")
	}
}

// Spend is recorded per day, so yesterday's overspend must not stop today's
// work. This is the check that catches a limiter reading a single running
// total instead of today's.
func TestTheLimitAppliesToTodayOnly(t *testing.T) {
	tr := memTracker()
	tr.mu.Lock()
	tr.days["2000-01-01"] = &dayEntry{USD: 500}
	tr.mu.Unlock()
	b := NewBudget(tr, 1.00)
	if b.Exceeded() {
		t.Fatal("a previous day's spend blocked today")
	}
	tr.addUSD(1.00)
	if !b.Exceeded() {
		t.Fatal("today's spend should stop it")
	}
}

func TestLimitIsImmutableForTheDay(t *testing.T) {
	// The limit is read from the config, not stored per day, so it takes
	// effect on the next query. That is deliberate: a person lowering their own
	// limit expects the next turn to respect it.
	tr := memTracker()
	tr.addUSD(0.50)
	if NewBudget(tr, 1.00).Exceeded() {
		t.Fatal("under a 1.00 limit at 0.50")
	}
	if !NewBudget(tr, 0.40).Exceeded() {
		t.Fatal("lowering the limit below today's spend should stop immediately")
	}
}

// The budget reads through the tracker, so the two must not be able to drift
// into separate views of the same money.
func TestBudgetTracksTheTracker(t *testing.T) {
	tr := memTracker()
	b := NewBudget(tr, 1.00)
	if b.Spent() != 0 {
		t.Fatalf("Spent = %v on a fresh tracker", b.Spent())
	}
	tr.Add("gemini", 1000, 1000)
	_, _, usd := tr.Today()
	if b.Spent() != usd {
		t.Fatalf("budget says %v, tracker says %v", b.Spent(), usd)
	}
	if usd <= 0 {
		t.Fatalf("a metered provider recorded $%v", usd)
	}
}

// A limiter is read from the UI thread while a turn goroutine records spend, so
// the tracker underneath it has to be safe for that.
func TestBudgetIsSafeWhileSpendIsRecorded(t *testing.T) {
	if testing.Short() {
		t.Skip("stress")
	}
	tr := memTracker()
	b := NewBudget(tr, 1.00)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.addUSD(0.001)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = b.Exceeded()
				_ = b.Fraction()
				_ = b.Remaining()
			}
		}()
	}
	wg.Wait()
	if got := b.Spent(); got < 0.79 || got > 0.81 {
		t.Fatalf("lost or duplicated spend: %v, want about 0.8", got)
	}
}

func TestLocalProvidersCostNothingToStop(t *testing.T) {
	// The default experience must be unchanged: a local model with no budget
	// set records volumes and never stops.
	b := NewBudget(memTracker(), 0)
	for i := 0; i < 100; i++ {
		b.WillExceedAfter(999)
	}
	if b.Exceeded() || !b.Unlimited() {
		t.Fatal("an unbudgeted local session was stopped")
	}
}
