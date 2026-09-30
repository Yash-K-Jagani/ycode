package cost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The days map used to be serialised in full on every Add, making a turn O(days):
// ~550us with one day recorded and 1.2ms with a year. Storing a row per day
// fixes that, and these tests pin the properties that make it safe - none of
// which is about timing, which would only be flaky.

func newFileTracker(t *testing.T) (*Tracker, string) {
	t.Helper()
	dir := t.TempDir()
	tr := &Tracker{file: filepath.Join(dir, "cost.json"), days: map[string]*dayEntry{}}
	return tr, dir
}

func dayFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != "" && len(e.Name()) > len("cost.json.day-") {
			n++
		}
	}
	return n
}

// Add must touch one day's record, not every day it knows about. This is the
// O(days) property stated as a fact about the filesystem, so it cannot go flaky
// the way a threshold comparison would.
func TestAddWritesOnlyTheChangedDay(t *testing.T) {
	tr, dir := newFileTracker(t)
	for i := range 400 {
		tr.persistDay(time.Now().AddDate(0, 0, -i).Format("2006-01-02"), &dayEntry{USD: 1})
	}
	before := dayFiles(t, dir)
	if before != 400 {
		t.Fatalf("setup wrote %d day files, want 400", before)
	}
	tr.Add("gemini", 100, 100)
	if after := dayFiles(t, dir); after != before {
		t.Fatalf("Add wrote %d new files; it should write exactly the day it changed", after-before)
	}
}

// Splitting the store across files is only safe if history survives a restart.
// Silently starting the day at zero is the failure mode that looks like the
// feature simply not working.
func TestHistorySurvivesRestart(t *testing.T) {
	tr, dir := newFileTracker(t)
	tr.persistDay("2026-01-01", &dayEntry{PromptTok: 10, ComplTok: 20, USD: 1.5})
	tr.persistDay("2026-01-02", &dayEntry{PromptTok: 30, ComplTok: 40, USD: 2.5})

	reloaded := &Tracker{file: filepath.Join(dir, "cost.json")}
	reloaded.load()
	if len(reloaded.days) != 2 {
		t.Fatalf("restart lost history: %d days, want 2", len(reloaded.days))
	}
	if got := reloaded.days["2026-01-02"]; got == nil || got.USD != 2.5 || got.PromptTok != 30 {
		t.Fatalf("day not restored intact: %+v", got)
	}
}

// A pre-existing install has one aggregate file and no per-day files. Without a
// migration it would read as zero spend forever.
func TestMigratesLegacyAggregateFile(t *testing.T) {
	tr, dir := newFileTracker(t)
	legacy := map[string]dayEntry{
		"2026-02-01": {PromptTok: 7, ComplTok: 9, USD: 0.75},
		"2026-02-02": {PromptTok: 1, ComplTok: 2, USD: 0.25},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(tr.file, data, 0o644); err != nil {
		t.Fatal(err)
	}

	fresh := &Tracker{file: tr.file}
	fresh.load()
	if len(fresh.days) != 2 {
		t.Fatalf("legacy spend not imported: %d days, want 2", len(fresh.days))
	}
	if got := fresh.days["2026-02-01"]; got == nil || got.USD != 0.75 {
		t.Fatalf("legacy day wrong: %+v", got)
	}
	// Migrated into per-day records, so the next Add is O(1) too.
	if n := dayFiles(t, dir); n != 2 {
		t.Fatalf("migration wrote %d day files, want 2", n)
	}
}

// Today is the only accessor, so an entry for today must be the sum of every Add
// made today, and must not depend on how much history exists.
func TestTodaySumsAcrossDays(t *testing.T) {
	tr, _ := newFileTracker(t)
	tr.persistDay("2020-01-01", &dayEntry{USD: 500})
	if _, _, usd := tr.Today(); usd != 0 {
		t.Fatalf("a past day's spend leaked into today: %v", usd)
	}
	tr.Add("gemini", 1000, 1000)
	_, _, first := tr.Today()
	tr.Add("gemini", 1000, 1000)
	_, _, second := tr.Today()
	if second <= first {
		t.Fatalf("today did not accumulate: %v then %v", first, second)
	}
}
