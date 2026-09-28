package automation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

// The daemon runs unattended, so a bug here is a job that silently never
// happens. The properties worth pinning: a broken config file says so, and two
// tasks that share a name do not starve each other.

func tempHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "ycode-automation-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("HOME", home)        // and on unix
	return home
}

func writeGlobal(t *testing.T, body string) {
	t.Helper()
	home := tempHome(t)
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config.Dir(), "automations.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = home
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// A typo in automations.yaml used to be swallowed, so every automation stopped
// with nothing on screen to explain it. The daemon is unattended, so there is
// nobody watching for a job that never arrives.
func TestLoadReportsAnUnparseableFile(t *testing.T) {
	writeGlobal(t, "automations: [ this is not: valid: yaml")

	tasks := Load()
	if len(tasks) != 0 {
		t.Fatalf("a broken file produced %d tasks", len(tasks))
	}
	report := captureStderr(t, func() { Load() })
	if report == "" {
		t.Fatal("an unparseable automations.yaml was silently ignored")
	}
	if !strings.Contains(report, "cannot parse") {
		t.Fatalf("the report does not say what went wrong: %q", report)
	}
	// It has to name the file, or the user cannot find it.
	if !strings.Contains(report, "automations.yaml") {
		t.Fatalf("the report does not name the file: %q", report)
	}
}

func TestLoadReadsTasks(t *testing.T) {
	writeGlobal(t, `
automations:
  - name: nightly
    prompt: run the test suite
    mode: build
    interval_minutes: 60
  - name: weekly
    prompt: summarise open PRs
    mode: goal
    cron: "0 9 * * 1"
`)
	tasks := Load()
	if len(tasks) != 2 {
		t.Fatalf("loaded %d tasks", len(tasks))
	}
	if tasks[0].Name != "nightly" || tasks[0].IntervalMinutes != 60 {
		t.Fatalf("first task = %+v", tasks[0])
	}
	if tasks[1].Cron != "0 9 * * 1" {
		t.Fatalf("second task = %+v", tasks[1])
	}
	// A task with no prompt would enqueue nothing, so it must be inert
	// everywhere rather than only at the enqueue point.
	if due := dueTasks(tasks, map[string]time.Time{}, time.Now()); len(due) != 1 {
		t.Fatalf("dueTasks = %d tasks, want only the interval one", len(due))
	}
	if dueTasks(tasks, map[string]time.Time{}, time.Now())[0].Name != "nightly" {
		t.Fatal("the cron task was treated as an interval task")
	}
}

// The regression: the ledger was keyed by name, so a project automation and a
// global one with the same name shared a slot. The first was enqueued, the
// second was skipped as "already run", and it stayed starved for the life of
// the daemon.
func TestTwoTasksWithTheSameNameBothRun(t *testing.T) {
	a := Task{Name: "check", Prompt: "run the linter", IntervalMinutes: 30}
	b := Task{Name: "check", Prompt: "run the type checker", IntervalMinutes: 30}

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	due := dueTasks([]Task{a, b}, map[string]time.Time{}, now)
	if len(due) != 2 {
		t.Fatalf("%d of 2 same-named tasks were due; they are starving each other", len(due))
	}
	if due[0].Prompt == due[1].Prompt {
		t.Fatal("the two tasks were treated as identical")
	}
}

func TestTaskKeyDistinguishesDifferentTasks(t *testing.T) {
	base := Task{Name: "check", Prompt: "run the linter", Mode: "build", IntervalMinutes: 30}
	same := base
	if taskKey(base) != taskKey(same) {
		t.Fatal("an identical task got two different keys, so it would run twice")
	}
	for name, changed := range map[string]Task{
		"different name":   {Name: "other", Prompt: base.Prompt, Mode: base.Mode, IntervalMinutes: base.IntervalMinutes},
		"different prompt": {Name: base.Name, Prompt: "something else", Mode: base.Mode, IntervalMinutes: base.IntervalMinutes},
		"different mode":   {Name: base.Name, Prompt: base.Prompt, Mode: "goal", IntervalMinutes: base.IntervalMinutes},
		"different cron":   {Name: base.Name, Prompt: base.Prompt, Mode: base.Mode, IntervalMinutes: base.IntervalMinutes, Cron: "0 9 * * 1"},
		"different every":  {Name: base.Name, Prompt: base.Prompt, Mode: base.Mode, IntervalMinutes: 60},
	} {
		if taskKey(base) == taskKey(changed) {
			t.Errorf("%s did not change the key", name)
		}
	}
}

func TestDueTasksRespectsTheInterval(t *testing.T) {
	task := Task{Name: "hourly", Prompt: "check", IntervalMinutes: 60}
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	last := map[string]time.Time{taskKey(task): t0}

	// Just after: not due.
	if d := dueTasks([]Task{task}, last, t0.Add(30*time.Minute)); len(d) != 0 {
		t.Fatalf("fired 30 minutes into a 60 minute interval")
	}
	// Well after: due.
	if d := dueTasks([]Task{task}, last, t0.Add(61*time.Minute)); len(d) != 1 {
		t.Fatalf("did not fire 61 minutes into a 60 minute interval")
	}
	// Never run before: due immediately. The daemon's first cycle relies on
	// this, so a restart re-runs rather than waiting a whole interval.
	if d := dueTasks([]Task{task}, map[string]time.Time{}, t0); len(d) != 1 {
		t.Fatal("a task with no recorded run was not due")
	}
	// The same task repeated in one pass is still one job.
	if d := dueTasks([]Task{task, task}, map[string]time.Time{}, t0); len(d) != 2 {
		t.Fatalf("a duplicated task appeared %d times", len(d))
	}
}

func TestDueTasksIgnoresUnusableTasks(t *testing.T) {
	now := time.Now()
	cases := map[string]Task{
		"no interval":   {Name: "x", Prompt: "do it"},
		"zero interval": {Name: "x", Prompt: "do it", IntervalMinutes: 0},
		"no prompt":     {Name: "x", IntervalMinutes: 30},
		"empty prompt":  {Name: "x", Prompt: "   ", IntervalMinutes: 30},
	}
	for name, task := range cases {
		if d := dueTasks([]Task{task}, map[string]time.Time{}, now); len(d) != 0 {
			t.Errorf("%s was treated as due", name)
		}
	}
}

// Daemon must return when its context is cancelled, and must not spin.
func TestDaemonStopsOnContextCancel(t *testing.T) {
	tempHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Daemon(ctx)
		close(done)
	}()
	// Give it a moment to reach its loop, then stop it.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Daemon did not return after its context was cancelled")
	}
}
