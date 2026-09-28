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

func writeGo(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func haveFormatter(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// The note must describe what happened. The old code returned " · formatted"
// whenever the formatter was merely installed, so a file that does not even
// parse was announced as formatted — to the model and to the user.
func TestFormatNoteIsHonest(t *testing.T) {
	if !haveFormatter("gofmt") {
		t.Skip("gofmt not installed")
	}
	dir := t.TempDir()

	bad := writeGo(t, dir, "bad.go", "package main\nfunc broken( {\n")
	if note := tryFormat(bad); strings.Contains(note, "formatted") && !strings.Contains(note, "not formatted") {
		t.Fatalf("a file that does not parse was reported as %q", note)
	}

	good := writeGo(t, dir, "good.go", "package main\nfunc   ok( )  {}\n")
	note := tryFormat(good)
	if note != " · formatted" {
		t.Fatalf("a valid file should report formatting, got %q", note)
	}
	// And the file really was reformatted on disk.
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "func   ok") {
		t.Fatalf("gofmt did not actually reformat: %q", data)
	}
}

func TestNoFormatterMeansNoNote(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.unknownext")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if note := tryFormat(p); note != "" {
		t.Fatalf("an unknown extension should produce no note, got %q", note)
	}
}

// The diff shown to the model must match the file on disk. Formatting can
// change the content, and the diff was computed from the pre-format text.
func TestWriteDiffMatchesDiskAfterFormatting(t *testing.T) {
	dir := t.TempDir()
	tool := &WriteTool{Workdir: dir}
	// Deliberately misindented, so gofmt changes it.
	msg := "package main\n\nfunc main() {\n        println(1)\n}\n"
	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"m.go","content":`+mustJSON(msg)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "m.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(onDisk))
	// Every line the diff claims was added must be present on disk, with the
	// formatter's whitespace, not the model's.
	if !strings.Contains(body, "\n\tprintln(1)") {
		t.Fatalf("expected gofmt indentation on disk, got %q", body)
	}
	if !strings.Contains(out, "println(1)") {
		t.Fatalf("the diff omitted the added line:\n%s", out)
	}
	// The reported diff body should agree with the file: compare the added
	// lines the tool shows against what the file contains.
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(out, line) {
			t.Fatalf("diff is missing an on-disk line %q:\n%s\n--- disk ---\n%s", line, out, body)
		}
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
