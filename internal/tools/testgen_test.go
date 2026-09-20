package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilterArgs(t *testing.T) {
	if got := filterArgs("Go", "TestX"); strings.Join(got, " ") != "-run TestX" {
		t.Fatalf("go: %v", got)
	}
	if got := filterArgs("Python", "TestX"); strings.Join(got, " ") != "-k TestX" {
		t.Fatalf("py: %v", got)
	}
	if filterArgs("Go", "") != nil {
		t.Fatal("empty run should give nil")
	}
}

func TestTestgenFilterTempModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmpmod\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package tmpmod\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestBeta(t *testing.T) {}\n"), 0o644)
	tg := &TestGenTool{Workdir: dir}
	out, err := tg.Run(context.Background(), json.RawMessage(`{"path":".","run":"TestAlpha","args":"-v"}`))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "TestAlpha") || strings.Contains(out, "TestBeta") {
		t.Fatalf("filter did not isolate:\n%s", out)
	}
}
