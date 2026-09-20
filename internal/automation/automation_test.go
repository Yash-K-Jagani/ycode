package automation

import (
	"testing"
	"time"
)

func TestNextAfter(t *testing.T) {
	// Monday 2026-09-21 08:00 UTC; "0 9 * * 1" -> same day 09:00
	from := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	next, err := nextAfter("0 9 * * 1", from)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %v want %v", next, want)
	}
	// after 09:00 Monday -> next Monday
	next, _ = nextAfter("0 9 * * 1", want.Add(time.Hour))
	if next.Weekday() != time.Monday || next.Hour() != 9 || !next.After(want) {
		t.Fatalf("bad rollover: %v", next)
	}
	if _, err := nextAfter("not a cron", from); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := nextAfter("0 0 31 2 *", from); err != nil {
		t.Logf("feb-31 parses (never fires): %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	// no automations files in temp HOME/cwd guarantee nothing; just no-crash
	_ = Load()
}
