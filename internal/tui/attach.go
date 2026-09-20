package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var atRe = regexp.MustCompile(`@([^\s@]+)`)

// atToken returns the in-progress @-token (text after the last "@" without spaces).
func atToken(input string) (string, bool) {
	i := strings.LastIndex(input, "@")
	if i < 0 {
		return "", false
	}
	token := input[i+1:]
	if token == "" || strings.ContainsAny(token, " \t\n\"'") {
		return "", false
	}
	return token, true
}

// atPaths extracts all @path tokens from submitted text.
func atPaths(input string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range atRe.FindAllStringSubmatch(input, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// completeFiles lists repo-relative paths matching prefix (cap 60).
func completeFiles(workdir, prefix string) []string {
	var out []string
	lower := strings.ToLower(prefix)
	_ = filepath.WalkDir(workdir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != workdir && skipAttachDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= 300 {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(workdir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(strings.ToLower(rel), lower) || strings.HasPrefix(strings.ToLower(filepath.Base(rel)), lower) {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func skipAttachDir(n string) bool {
	switch n {
	case ".git", "node_modules", "__pycache__", ".venv", "dist", "build", "target":
		return true
	}
	return false
}

// expandAttachments reads @-mentioned files; returns expanded text + missing names.
func expandAttachments(workdir, input string) (string, []string) {
	paths := atPaths(input)
	if len(paths) == 0 {
		return input, nil
	}
	var missing []string
	blocks := ""
	n := 0
	for _, p := range paths {
		if n >= 5 {
			break
		}
		full := p
		if !filepath.IsAbs(full) {
			full = filepath.Join(workdir, filepath.FromSlash(p))
		}
		fi, err := os.Stat(full)
		if err != nil {
			missing = append(missing, p)
			continue
		}
		if fi.IsDir() {
			entries, _ := os.ReadDir(full)
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
				if len(names) >= 50 {
					break
				}
			}
			blocks += "\n<attached dir=\"" + p + "\">\n" + strings.Join(names, "\n") + "\n</attached>\n"
			n++
			continue
		}
		if fi.Size() > 24*1024 {
			missing = append(missing, p+" (too large, 24KB cap)")
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			missing = append(missing, p)
			continue
		}
		blocks += "\n<attached file=\"" + p + "\">\n" + string(data) + "\n</attached>\n"
		n++
	}
	if blocks == "" {
		return input, missing
	}
	return input + "\n" + blocks, missing
}
