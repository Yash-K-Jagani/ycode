package ctx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type treeEntry struct {
	at  time.Time
	val string
}

var (
	treeMu    sync.Mutex
	treeCache = map[string]treeEntry{}
)

var treeSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".venv": true,
	"dist": true, "build": true, "target": true, ".idea": true, ".vscode": true,
}

// Tree returns a compact relative-path listing of workdir for prompt injection.
// Capped at maxEntries entries and maxChars characters. Result cached 2s per workdir.
func Tree(workdir string, maxEntries, maxChars int) string {
	if maxEntries <= 0 {
		maxEntries = 150
	}
	if maxChars <= 0 {
		maxChars = 4000
	}
	key := fmt.Sprintf("%s:%d:%d", workdir, maxEntries, maxChars)
	treeMu.Lock()
	if e, ok := treeCache[key]; ok && time.Since(e.at) < 2*time.Second {
		v := e.val
		treeMu.Unlock()
		return v
	}
	treeMu.Unlock()
	var paths []string
	_ = filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != workdir && treeSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(paths) >= maxEntries*2 {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(paths)
	if len(paths) > maxEntries {
		paths = paths[:maxEntries]
	}
	var b strings.Builder
	for _, p := range paths {
		if b.Len()+len(p)+1 > maxChars {
			b.WriteString("…(truncated)\n")
			break
		}
		b.WriteString(p + "\n")
	}
	val := b.String()
	treeMu.Lock()
	treeCache[key] = treeEntry{at: time.Now(), val: val}
	treeMu.Unlock()
	return val
}
