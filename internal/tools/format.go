package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tryFormat best-effort formats a file after write/edit.
//
// It reports what actually happened. The previous version returned
// " · formatted" whenever the formatter was merely *installed* — the exit code
// was discarded — so a file with a syntax error was still announced as
// formatted, and the model was told a lie about its own edit.
//
// It never fails the main operation: a missing or unhappy formatter is a
// note, not an error.
func tryFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	argv := formatterFor(ext)
	if len(argv) == 0 {
		return ""
	}
	out, err := exec.Command(argv[0], append(argv[1:], path)...).CombinedOutput()
	if err == nil {
		return " · formatted"
	}
	// Say what went wrong, briefly: a formatter that rejects the file is
	// usually reporting a syntax error the model needs to see.
	note := strings.TrimSpace(string(out))
	if i := strings.IndexByte(note, '\n'); i > 0 {
		note = note[:i]
	}
	if note == "" {
		note = err.Error()
	}
	if len(note) > 120 {
		note = note[:120] + "…"
	}
	return " · not formatted: " + note
}

// formatterFor picks the first installed formatter for an extension.
func formatterFor(ext string) []string {
	have := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	switch ext {
	case ".go":
		if have("gofmt") {
			return []string{"gofmt", "-w"}
		}
		if have("go") {
			return []string{"go", "fmt"}
		}
	case ".py":
		if have("black") {
			return []string{"black", "-q"}
		}
		if have("ruff") {
			return []string{"ruff", "format"}
		}
	case ".js", ".ts", ".tsx", ".jsx", ".json", ".css", ".html", ".yaml", ".yml", ".md":
		if have("prettier") {
			return []string{"prettier", "--write", "--log-level", "silent"}
		}
	}
	return nil
}

// readFormatted re-reads a file after tryFormat so the diff we show reflects
// what is actually on disk. Formatting can change the file (indentation,
// trailing whitespace, import grouping), so a diff computed from the model's
// pre-format text does not match the file the next read will return.
func readFormatted(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return splitLines(string(data))
}
