package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtToken(t *testing.T) {
	if tok, ok := atToken("read @main"); !ok || tok != "main" {
		t.Fatalf("%q %v", tok, ok)
	}
	if _, ok := atToken("no token"); ok {
		t.Fatal("false positive")
	}
	if _, ok := atToken("trailing @"); ok {
		t.Fatal("space ends token")
	}
	if tok, ok := atToken("@a @b"); !ok || tok != "b" {
		t.Fatal("should take last token")
	}
	if _, ok := atToken("/models x"); ok {
		t.Fatal("no @ present")
	}
}

func TestAtPaths(t *testing.T) {
	got := atPaths("see @a.go and @a.go plus @dir/x.md ok")
	if len(got) != 2 || got[0] != "a.go" || got[1] != "dir/x.md" {
		t.Fatalf("%v", got)
	}
}

func TestCompleteFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "app.go"), []byte("x"), 0o644)
	got := completeFiles(dir, "ma")
	if len(got) != 1 || got[0] != "main.go" {
		t.Fatalf("%v", got)
	}
	got = completeFiles(dir, "app")
	if len(got) != 1 || got[0] != "sub/app.go" {
		t.Fatalf("basename match: %v", got)
	}
}

func TestExpandAttachments(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("FILEBODY"), 0o644)
	out, missing := expandAttachments(dir, "look @f.txt and @gone.txt")
	if len(missing) != 1 || missing[0] != "gone.txt" {
		t.Fatalf("missing: %v", missing)
	}
	if !strings.Contains(out, "FILEBODY") || !strings.Contains(out, `<attached file="f.txt">`) {
		t.Fatalf("bad expansion:\n%s", out)
	}
	out, missing = expandAttachments(dir, "plain")
	if out != "plain" || len(missing) != 0 {
		t.Fatal("no-token passthrough broken")
	}
}
