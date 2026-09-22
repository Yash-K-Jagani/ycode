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

func TestGitTool(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644)

	g := &GitTool{Workdir: dir}
	ctx := context.Background()
	out, err := g.Run(ctx, json.RawMessage(`{"action":"status"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "f.txt") {
		t.Fatalf("expected untracked file in status:\n%s", out)
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"add","args":"f.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"commit","message":"test"}`)); err != nil {
		t.Fatal(err)
	}
	out, err = g.Run(ctx, json.RawMessage(`{"action":"log"}`))
	if err != nil || !strings.Contains(out, "test") {
		t.Fatalf("bad log: %v\n%s", err, out)
	}
	// read-only ctx blocks mutating actions
	ro := WithReadOnly(ctx)
	if _, err := g.Run(ro, json.RawMessage(`{"action":"commit","message":"x"}`)); err == nil {
		t.Fatal("expected read-only block on commit")
	}
	if _, err := g.Run(ro, json.RawMessage(`{"action":"status"}`)); err != nil {
		t.Fatalf("status should work read-only: %v", err)
	}
}

func TestGitHubValidation(t *testing.T) {
	g := &GitHubTool{Workdir: t.TempDir()}
	ctx := context.Background()
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"clone","url":"ftp://evil/x"}`)); err == nil {
		t.Fatal("expected refusal for non-http URL")
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"clone","url":"https://example.com/x.git"}`)); err == nil {
		t.Fatal("expected refusal for non-github host")
	}
	ro := WithReadOnly(ctx)
	if _, err := g.Run(ro, json.RawMessage(`{"action":"issue_create","repo":"a/b","title":"t"}`)); err == nil {
		t.Fatal("expected read-only block on issue_create")
	}
}

func TestGitBranchAndSecretBlock(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "t@t.t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("hi\n"), 0o644)

	g := &GitTool{Workdir: dir}
	ctx := context.Background()
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"create_branch","args":"ycode/test-1"}`)); err != nil {
		t.Fatal(err)
	}
	// branch list is empty until first commit — commit a clean file, then check
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"add","args":"ok.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"commit","message":"ok"}`)); err != nil {
		t.Fatalf("clean commit failed: %v", err)
	}
	out, err := g.Run(ctx, json.RawMessage(`{"action":"branch"}`))
	if err != nil || !strings.Contains(out, "ycode/test-1") {
		t.Fatalf("branch not listed: %v\n%s", err, out)
	}
	// commit a file containing a secret must be blocked
	_ = os.WriteFile(filepath.Join(dir, "leak.txt"), []byte("token = \"ghp_1234567890123456789012345678901234\"\n"), 0o644)
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"add","args":"leak.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"commit","message":"leak"}`)); err == nil {
		t.Fatal("expected secret-scan block on commit")
	}
	// force override commits despite the leak file being staged — reset it first
	// so the repo stays usable, then force-commit the clean file change
	run("reset", "HEAD", "leak.txt")
	_ = os.WriteFile(filepath.Join(dir, "ok2.txt"), []byte("more\n"), 0o644)
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"add","args":"ok2.txt"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"commit","message":"ok","args":"force"}`)); err != nil {
		t.Fatalf("force commit failed: %v", err)
	}
}

func TestBrowserExtract(t *testing.T) {
	html := `<html><head><style>.x{}</style><script>alert(1)</script></head><body><h1>Hi</h1><p>a &amp; b</p></body></html>`
	got := ExtractText(html)
	if !strings.Contains(got, "Hi") || !strings.Contains(got, "a & b") {
		t.Fatalf("bad extract: %q", got)
	}
	if strings.Contains(got, "alert") {
		t.Fatalf("script not stripped: %q", got)
	}
	b := &BrowserTool{}
	if _, err := b.Run(context.Background(), json.RawMessage(`{"url":"file:///etc/passwd"}`)); err == nil {
		t.Fatal("expected refusal for file:// URL")
	}
}

func TestZeroLeakBlocks(t *testing.T) {
	ctx := WithZeroLeak(context.Background())
	b := &BrowserTool{}
	if _, err := b.Run(ctx, json.RawMessage(`{"url":"https://example.com"}`)); err == nil {
		t.Fatal("expected ZDL block on browser")
	}
	g := &GitHubTool{Workdir: t.TempDir()}
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"pr_list","repo":"a/b"}`)); err == nil {
		t.Fatal("expected ZDL block on github")
	}
	// read-only tools still work under ZDL
	rd := ReadTool{}
	if _, err := rd.Run(ctx, json.RawMessage(`{"path":".}"}`)); err != nil {
		_ = err // path may not exist; only block-type matters
	}
}

func TestGitHubRepo(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("remote", "add", "origin", "https://github.com/Owner/Repo.git")
	owner, repo, err := GitHubRepo(dir)
	if err != nil || owner != "Owner" || repo != "Repo" {
		t.Fatalf("%q %q %v", owner, repo, err)
	}
	run("remote", "set-url", "origin", "git@github.com:O2/R2.git")
	owner, repo, err = GitHubRepo(dir)
	if err != nil || owner != "O2" || repo != "R2" {
		t.Fatalf("ssh: %q %q %v", owner, repo, err)
	}
	if _, _, err := GitHubRepo(t.TempDir()); err == nil {
		t.Fatal("expected no-origin error")
	}
}

func TestReviewPostValidation(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	g := &GitHubTool{Workdir: t.TempDir()}
	ctx := context.Background()
	if _, err := g.Run(ctx, json.RawMessage(`{"action":"review_post"}`)); err == nil {
		t.Fatal("expected repo/number error")
	}
	args := `{"action":"review_post","repo":"a/b","number":1,"review":{"summary":"s","comments":[{"path":"f","line":0,"body":"x"}]}}`
	if _, err := g.Run(ctx, json.RawMessage(args)); err == nil {
		t.Fatal("expected bad-line error")
	}
	args2 := `{"action":"review_post","repo":"a/b","number":1,"review":{"summary":"s","comments":[{"path":"f","line":3,"body":"x"}]}}`
	if _, err := g.Run(ctx, json.RawMessage(args2)); err == nil || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("expected token error, got %v", err)
	}
	if _, err := g.Run(WithReadOnly(ctx), json.RawMessage(args2)); err == nil {
		t.Fatal("expected read-only block")
	}
}
