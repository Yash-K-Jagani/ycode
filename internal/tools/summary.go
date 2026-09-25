package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type SummaryTool struct{}

func (SummaryTool) Name() string { return "summary" }
func (SummaryTool) Description() string {
	return "Structural summary of a file: size, language, line/word counts, head preview, symbol outline. Args: path (required). Read-only."
}
func (SummaryTool) Schema() string {
	return `{"type":"object","required":["path"],"properties":{"path":{"type":"string"}}}`
}

var symbolRes = map[string]*regexp.Regexp{
	".go":   regexp.MustCompile(`^(func|type|const|var)\s+([A-Za-z0-9_]+)`),
	".py":   regexp.MustCompile(`^(def|class)\s+([A-Za-z0-9_]+)`),
	".js":   regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?(?:function\s+([A-Za-z0-9_]+)|(?:const|let|var)\s+([A-Za-z0-9_]+)\s*=|class\s+([A-Za-z0-9_]+))`),
	".ts":   regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?(?:function\s+([A-Za-z0-9_]+)|(?:const|let|var)\s+([A-Za-z0-9_]+)\s*=|class\s+([A-Za-z0-9_]+)|interface\s+([A-Za-z0-9_]+))`),
	".rs":   regexp.MustCompile(`^(pub\s+)?(fn|struct|enum|trait|mod)\s+([A-Za-z0-9_]+)`),
	".java": regexp.MustCompile(`^\s*(public|private|protected)?\s*(class|interface|enum)\s+([A-Za-z0-9_]+)`),
	".rb":   regexp.MustCompile(`^(def|class|module)\s+([A-Za-z0-9_:]+)`),
	".php":  regexp.MustCompile(`^(class|function)\s+([A-Za-z0-9_\\]+)`),
}

func (SummaryTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if a.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	lines := splitLines(string(data))
	words := len(strings.Fields(string(data)))
	ext := strings.ToLower(filepath.Ext(a.Path))
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %d lines, %d words, %d bytes\n", a.Path, len(lines), words, len(data))
	syms := outline(ext, lines)
	if len(syms) > 0 {
		if len(syms) > 40 {
			syms = syms[:40]
		}
		b.WriteString("symbols:\n- " + strings.Join(syms, "\n- ") + "\n")
	}
	head := lines
	if len(head) > 30 {
		head = head[:30]
	}
	b.WriteString("head:\n```\n" + strings.Join(head, "\n"))
	if len(lines) > 30 {
		fmt.Fprintf(&b, "\n…(%d more lines)", len(lines)-30)
	}
	b.WriteString("\n```")
	return b.String(), nil
}

func outline(ext string, lines []string) []string {
	re, ok := symbolRes[ext]
	if !ok {
		switch ext {
		case ".tsx", ".jsx":
			re = symbolRes[".ts"]
		case ".cs":
			re = symbolRes[".java"]
		default:
			return nil
		}
	}
	seen := map[string]bool{}
	var out []string
	for i, ln := range lines {
		m := re.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil {
			continue
		}
		name := ""
		for _, g := range m[1:] {
			if g != "" && name == "" {
				if g == "func" || g == "def" || g == "class" || g == "pub" {
					continue
				}
				name = g
			}
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, fmt.Sprintf("%s (L%d)", name, i+1))
	}
	sort.Strings(out)
	return out
}
