package automation

import (
	"context"
	"os"
	"path/filepath"
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
		if err := yaml.Unmarshal(data, &v); err == nil {
			out = append(out, v.Automations...)
		}
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

// Daemon enqueues due tasks and runs the queue, every minute until ctx ends.
// Tasks may use interval_minutes and/or a standard 5-field cron spec.
func Daemon(ctx context.Context) {
	sched := cron.New()
	for _, t := range Load() {
		t := t
		if t.Cron == "" || t.Prompt == "" {
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
		for _, t := range Load() {
			if t.IntervalMinutes <= 0 || t.Prompt == "" {
				continue
			}
			if now.Sub(last[t.Name]) < time.Duration(t.IntervalMinutes)*time.Minute {
				continue
			}
			last[t.Name] = now
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
