package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Yash-K-Jagani/ycode/internal/tools"
)

// watchDirs returns directories to observe (root + subdirs, skips junk).
func watchDirs(workdir string) []string {
	out := []string{workdir}
	_ = filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(out) > 200 {
			return nil
		}
		if d.IsDir() && path != workdir {
			if skipAttachDir(d.Name()) {
				return filepath.SkipDir
			}
			out = append(out, path)
		}
		return nil
	})
	return out
}

func (m *Model) watching() bool { return m.watchCancel != nil }

func (m *Model) stopWatch() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
		m.watchTarget = ""
	}
}

func (m *Model) startWatch(target string) {
	m.stopWatch()
	ctx, cancel := context.WithCancel(context.Background())
	m.watchCancel = cancel
	m.watchTarget = target
	go m.watchLoop(ctx, target)
}

func (m *Model) runWatchedTests() string {
	raw, _ := json.Marshal(map[string]string{"path": m.watchTarget})
	out, err := (&tools.TestGenTool{Workdir: m.workdir}).Run(context.Background(), raw)
	if len(out) > 4000 {
		out = out[:4000] + "\n…(truncated)"
	}
	if err != nil {
		return "watch: FAILED\n" + out
	}
	return "watch: passed\n" + out
}

func (m *Model) watchLoop(ctx context.Context, target string) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer func() { _ = w.Close() }()
	for _, d := range watchDirs(m.workdir) {
		_ = w.Add(d)
	}
	if m.prog != nil {
		m.prog.Send(sysMsg(m.runWatchedTests()))
	}
	var timer *time.Timer
	fire := func() {
		if m.prog != nil {
			m.prog.Send(sysMsg(m.runWatchedTests()))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if strings.HasSuffix(ev.Name, "~") || strings.HasSuffix(ev.Name, ".tmp") {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(1200*time.Millisecond, fire)
		case <-w.Errors:
		}
	}
}
