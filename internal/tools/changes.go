package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ChangesTool shows the working-tree change overview (git status + diff stat).
type ChangesTool struct{ Workdir string }

func (ChangesTool) Name() string { return "changes" }
func (ChangesTool) Description() string {
	return "Show what changed in the repo (git status + diff stat, read-only). Args: none."
}
func (ChangesTool) Schema() string {
	return `{"type":"object","properties":{}}`
}

func (t *ChangesTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// run returns the output and the error. The old version collapsed every
	// error to "", so a missing git, a non-repo workdir or an expired context
	// produced an empty status and the tool answered "clean" — telling the
	// model there was nothing to do when it had no idea.
	run := func(argv ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", argv...)
		if t.Workdir != "" {
			cmd.Dir = t.Workdir
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			return "", fmt.Errorf("git %s: %s", strings.Join(argv, " "), firstLine(msg))
		}
		return strings.TrimSpace(string(out)), nil
	}

	branch, err := run("branch", "--show-current")
	if err != nil {
		// A repo with no commits yet has no branch; that is not an error.
		if strings.Contains(err.Error(), "git branch") {
			branch = "(no commits yet)"
		} else {
			return "", fmt.Errorf("cannot read git state: %w", err)
		}
	}
	var b strings.Builder
	b.WriteString("branch: " + branch + "\n")

	status, err := run("status", "--short")
	if err != nil {
		return "", fmt.Errorf("cannot read git status: %w", err)
	}
	if status == "" {
		return strings.TrimSpace(b.String()) + "\nclean", nil
	}
	lines := strings.Split(status, "\n")
	if len(lines) > 20 {
		lines = append(lines[:20], fmt.Sprintf("…(%d more)", len(lines)-20))
	}
	b.WriteString(strings.Join(lines, "\n") + "\n")
	if stat, err := run("diff", "--stat", "HEAD", "--"); err == nil && stat != "" {
		b.WriteString(stat + "\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// IsGitRepo reports whether workdir is inside a git repository, without
// running a command that can fail confusingly.
func IsGitRepo(workdir string) bool {
	if workdir == "" {
		workdir = "."
	}
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = workdir
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// DirtyTree returns the short status of a workdir, and an error when git could
// not answer. Callers that gate on "is the tree clean?" must treat the error as
// "unknown", not "clean" — a silently-empty status once let /refactor branch
// over an uncommitted tree, defeating the guard's whole purpose.
func DirtyTree(workdir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git status in %s: %s", filepath.Clean(workdir), firstLine(msg))
	}
	return strings.TrimSpace(string(out)), nil
}
