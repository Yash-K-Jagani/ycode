package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Yash-K-Jagani/ycode/internal/textutil"
)

func TestTruncateIsRuneSafe(t *testing.T) {
	if got := truncate("hello", 100); got != "hello" {
		t.Fatalf("short text was altered: %q", got)
	}
	if got := truncate("hello", 0); got != "…" {
		t.Fatalf("truncate(_, 0) = %q", got)
	}
	// A multi-byte character at the cut point must not be halved: this text
	// lands in a terminal and in PR comments.
	for n := 1; n <= 8; n++ {
		got := truncate("aaa→bbb✓", n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncate(_, %d) = %q is not valid UTF-8", n, got)
		}
	}
	// Batch job prompts are user text, so non-ASCII has to survive.
	if got := truncate("日本語のタスク", 3); !strings.HasPrefix(got, "日本語") {
		t.Fatalf("truncate cut the wrong place: %q", got)
	}
}

func TestBuildInfoNamesTheVersion(t *testing.T) {
	got := buildInfo()
	if !strings.Contains(got, "ycode") {
		t.Fatalf("buildInfo = %q", got)
	}
	// A version string that lost its placeholder would ship as "ycode  (commit
	// , built )", which tells a user reporting a bug nothing.
	if Version == "" && !strings.Contains(got, "dev") {
		t.Logf("buildInfo with an empty Version: %q", got)
	}
	if !strings.Contains(got, "commit") {
		t.Fatalf("buildInfo does not mention the commit: %q", got)
	}
}

func TestConfigKeysCoverEveryDocumentedKey(t *testing.T) {
	keys := configKeys()
	have := map[string]bool{}
	for _, k := range keys {
		if have[k] {
			t.Fatalf("duplicate config key %q", k)
		}
		have[k] = true
	}
	// These are the keys the README tells people to set. If one is missing
	// from the list, `ycode config set` rejects it and the docs are wrong.
	for _, want := range []string{
		"active_provider", "active_model", "ollama_host", "theme", "zero_data_leak",
		"gemini_api_key", "openrouter_api_key", "groq_api_key",
	} {
		if !have[want] {
			t.Fatalf("configKeys is missing %q", want)
		}
	}
}

// A byte slice of a string containing multi-byte characters produces invalid
// UTF-8, which a terminal renders as a replacement glyph and a model is handed
// as mojibake. The repo brief went through exactly that path.
//
// The pattern matched is a slice expression followed by concatenation, which is
// the truncation idiom - `s[:n] + "…"`. Slicing a []string, []Job or a hash
// does not match, and does not need to: those are not text.
var byteSlice = regexp.MustCompile(`\w+\[:\d+\]\s*\+`)

func TestNoByteSlicingOfTextRemains(t *testing.T) {
	// Tests run with the package directory as the working directory, so find
	// the module root rather than guessing how far up it is.
	root := moduleRoot(t)
	visited := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		visited++
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			// Indexing a slice of structs, maps or byte slices is fine; this
			// only catches `someString[:n]`.
			if !byteSlice.MatchString(line) {
				continue
			}
			if strings.Contains(line, "utf8") || strings.Contains(line, "byte") {
				continue
			}
			t.Errorf("%s:%d slices a string by bytes: %s", path, i+1, strings.TrimSpace(line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A guard that walks nothing passes forever, so prove it looked.
	if visited < 60 {
		t.Fatalf("the walker saw only %d files, so this guard is vacuous", visited)
	}
}

// moduleRoot is the directory holding go.mod, so the walker covers the
// whole module regardless of which package the test lives in.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the working directory")
		}
		dir = parent
	}
}

// The helper exists so the two behaviours are pinned together: short text is
// untouched, and a cut is always on a character boundary.
func TestTextutilIsTheOneImplementation(t *testing.T) {
	if textutil.Truncate("hello", 99) != "hello" {
		t.Fatal("textutil.Truncate altered short text")
	}
	if got := textutil.Truncate("日本語", 2); got != "日本…" {
		t.Fatalf("textutil.Truncate = %q", got)
	}
}
