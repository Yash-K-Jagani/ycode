package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func haveBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pyBin() string {
	if haveBin("python3") {
		return "python3"
	}
	return "python"
}

func TestRunPython(t *testing.T) {
	if !haveBin(pyBin()) {
		t.Skip("no python")
	}
	rt := &RunTool{Workdir: t.TempDir()}
	out, err := rt.Run(context.Background(), json.RawMessage(`{"language":"python","code":"print(6*7)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("bad output: %q", out)
	}
	// file path mode
	out2, err := rt.Run(context.Background(), json.RawMessage(`{"language":"python","code":"import sys\nprint('err here', file=sys.stderr)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "err here") {
		t.Fatalf("stderr not captured: %q", out2)
	}
}

func TestRunGuards(t *testing.T) {
	rt := &RunTool{Workdir: t.TempDir()}
	if _, err := rt.Run(context.Background(), json.RawMessage(`{"language":"cobol","code":"x"}`)); err == nil {
		t.Fatal("expected unsupported-language error")
	}
	if _, err := rt.Run(context.Background(), json.RawMessage(`{"language":"python"}`)); err == nil {
		t.Fatal("expected missing code/path error")
	}
	if _, err := rt.Run(context.Background(), json.RawMessage(`{"language":"python","code":"x = 1\nrm -rf / tmp"}`)); err == nil {
		t.Fatal("expected denylist block")
	}
	if _, err := rt.Run(WithReadOnly(context.Background()), json.RawMessage(`{"language":"python","code":"print(1)"}`)); err == nil {
		t.Fatal("expected read-only block")
	}
}

func TestRunNodeIfPresent(t *testing.T) {
	if !haveBin("node") {
		t.Skip("no node")
	}
	rt := &RunTool{Workdir: t.TempDir()}
	out, err := rt.Run(context.Background(), json.RawMessage(`{"language":"javascript","code":"console.log(1+2)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3") {
		t.Fatalf("bad output: %q", out)
	}
}
