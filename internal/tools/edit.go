package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type EditTool struct{ Workdir string }

func (EditTool) Name() string { return "edit" }
func (EditTool) Description() string {
	return "Surgical string replacement in a file (preferred over write for changes). Args: path (required), old_string (required, must match exactly once — anchor a small unique block, never the whole file), new_string (required)."
}
func (EditTool) Schema() string {
	return `{"type":"object","required":["path","old_string","new_string"],"properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}}}`
}

func (t *EditTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsReadOnly(ctx) {
		return "", fmt.Errorf("edit is blocked in read-only mode")
	}
	p := resolve(t.Workdir, a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	s := string(data)
	actual := a.OldString
	if len(data) > 0 && float64(len(a.OldString))/float64(len(data)) > 0.8 {
		return "", fmt.Errorf("old_string covers >80%% of %s — anchor a smaller unique block instead of rewriting the file", p)
	}
	n := countOccurrences(s, actual)
	if n == 0 {
		if stripped := stripLineNumbers(a.OldString); stripped != a.OldString {
			if m := countOccurrences(s, stripped); m == 1 {
				actual = stripped
				n = 1
			}
		}
	}
	if n == 0 {
		return "", fmt.Errorf("old_string not found in %s%s", p, suggestLines(s, a.OldString))
	}
	if n > 1 {
		return "", fmt.Errorf("old_string matches %d times in %s - be more specific%s", n, p, suggestLines(s, a.OldString))
	}
	s = replaceOnce(s, actual, a.NewString)
	backupFile(t.Workdir, p, data)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return "", err
	}
	out := fmt.Sprintf("edited %s\n```diff\n%s```",
		p, diffBlock(splitLines(a.OldString), splitLines(a.NewString), 60))
	return strings.TrimRight(out, "\n"), nil
}

func countOccurrences(s, sub string) int {
	if sub == "" {
		return 0
	}
	n := 0
	for i := 0; i+len(sub) <= len(s); {
		if s[i:i+len(sub)] == sub {
			n++
			i += len(sub)
		} else {
			i++
		}
	}
	return n
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

var lineNumPrefix = regexp.MustCompile(`(?m)^\d+:\s?`)

// stripLineNumbers removes "12: " prefixes models copy from read output.
func stripLineNumbers(s string) string {
	return lineNumPrefix.ReplaceAllString(s, "")
}

// suggestLines finds up to 3 file lines resembling the failed anchor.
func suggestLines(content, anchor string) string {
	anchorWords := contentWords(anchor)
	if len(anchorWords) == 0 {
		return ""
	}
	type hit struct {
		n     int
		line  string
		score int
	}
	var hits []hit
	for i, ln := range splitLines(content) {
		words := contentWords(ln)
		if len(words) == 0 {
			continue
		}
		shared := 0
		for w := range words {
			if anchorWords[w] {
				shared++
			}
		}
		if shared > 0 {
			hits = append(hits, hit{i + 1, ln, shared})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	var b strings.Builder
	b.WriteString(" Did you mean:")
	for _, h := range hits {
		if len(b.String()) > 600 {
			break
		}
		fmt.Fprintf(&b, "\n  L%d: %s", h.n, truncate(strings.TrimSpace(h.line), 120))
		if len(b.String()) > 600 {
			break
		}
	}
	return b.String()
}

func contentWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(stripLineNumbers(s))) {
		w = strings.Trim(w, "\"'`,;:.()[]{}")
		if len(w) > 2 {
			out[w] = true
		}
	}
	return out
}
