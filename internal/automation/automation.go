package automation

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/batch"
	"github.com/Yash-K-Jagani/ycode/internal/config"
	"gopkg.in/yaml.v3"
)

type Task struct {
	Name            string `yaml:"name"`
	Prompt          string `yaml:"prompt"`
	Mode            string `yaml:"mode"`
	IntervalMinutes int    `yaml:"interval_minutes"`
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

// Daemon enqueues due tasks and runs the queue, every minute until ctx ends.
func Daemon(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	last := map[string]time.Time{}
	cycle := func() {
		q := batch.Load()
		now := time.Now()
		for _, t := range Load() {
			if t.IntervalMinutes <= 0 || t.Prompt == "" {
				continue
			}
			if now.Sub(last[t.Name]) < time.Duration(t.IntervalMinutes)*time.Minute {
				continue
			}
			last[t.Name] = now
			cwd, _ := os.Getwd()
			q.Add("[automation:"+t.Name+"] "+t.Prompt, t.Mode, "", cwd)
		}
		q.RunPending(ctx)
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
