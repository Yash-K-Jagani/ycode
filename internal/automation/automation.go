package automation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type Task struct {
	Name            string `yaml:"name"`
	Prompt          string `yaml:"prompt"`
	Mode            string `yaml:"mode"`
	IntervalMinutes int    `yaml:"interval_minutes"`
	Cron            string `yaml:"cron,omitempty"` // standard 5-field, e.g. "0 9 * * 1"
}

// Load reads the automations files, project first then global.
//
// A file that exists but does not parse is reported. Swallowing it meant a typo
// in automations.yaml silently stopped every automation, with nothing on
// screen to say so - and this runs unattended, so there is no user watching
// for a job that never arrives.
func Load() []Task {
	var paths []string
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(cwd, ".ycode", "automations.yaml"))
	}
	paths = append(paths, filepath.Join(config.Dir(), "automations.yaml"))
	var out []Task
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var v struct {
			Automations []Task `yaml:"automations"`
		}
		if err := yaml.Unmarshal(data, &v); err != nil {
			fmt.Fprintf(os.Stderr, "automations: cannot parse %s: %v\n", p, err)
			continue
		}
		out = append(out, v.Automations...)
	}
	return out
}

// nextAfter computes the next fire time for a cron spec after from.
func nextAfter(spec string, from time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}

var queueMu sync.Mutex

func enqueue(t Task) {
	cwd, _ := os.Getwd()
	batch.Load().Add("[automation:"+t.Name+"] "+t.Prompt, t.Mode, "", cwd)
}

func runQueue(ctx context.Context) {
	queueMu.Lock()
	defer queueMu.Unlock()
	batch.Load().RunPending(ctx)
}

// taskKey identifies a task for the interval ledger.
//
// It used to be the name alone, which meant a project automation and a global
// one sharing a name fought over the same slot: the first was enqueued, the
// second was skipped as "already run this interval", and it stayed starved for
// the life of the daemon with nothing to say why. The prompt and schedule are
// part of the identity, so two tasks only collide when they really are the
// same task.
func taskKey(t Task) string {
	return strings.Join([]string{t.Name, t.Prompt, t.Mode, t.Cron, strconv.Itoa(t.IntervalMinutes)}, "\x00")
}

// dueTasks returns the interval tasks that should fire at now, and is the only
// place that decision is made. It is a pure function so the scheduling rule can
// be tested without a running daemon.
func dueTasks(tasks []Task, last map[string]time.Time, now time.Time) []Task {
	var due []Task
	for _, t := range tasks {
		// A task with no prompt would enqueue nothing, and a task with no
		// interval is a cron task, handled by the scheduler. The prompt is
		// trimmed because a whitespace-only one passes a `== ""` check and
		// would enqueue a blank job every interval, forever.
		if t.IntervalMinutes <= 0 || strings.TrimSpace(t.Prompt) == "" {
			continue
		}
		if now.Sub(last[taskKey(t)]) < time.Duration(t.IntervalMinutes)*time.Minute {
			continue
		}
		due = append(due, t)
	}
	return due
}

// Daemon enqueues due tasks and runs the queue, every minute until ctx ends.
// Tasks may use interval_minutes and/or a standard 5-field cron spec.
func Daemon(ctx context.Context) {
	sched := cron.New()
	for _, t := range Load() {
		if t.Cron == "" || strings.TrimSpace(t.Prompt) == "" {
			continue
		}
		if _, err := cron.ParseStandard(t.Cron); err != nil {
			continue
		}
		if _, err := sched.AddFunc(t.Cron, func() {
			enqueue(t)
			runQueue(ctx)
		}); err != nil {
			continue
		}
	}
	sched.Start()
	defer sched.Stop()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	last := map[string]time.Time{}
	cycle := func() {
		now := time.Now()
		for _, t := range dueTasks(Load(), last, now) {
			last[taskKey(t)] = now
			enqueue(t)
		}
		runQueue(ctx)
	}
	cycle()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			cycle()
		}
	}
}
