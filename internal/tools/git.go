package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/security"
)

// CtxReadOnly marks a context where mutating actions are forbidden (plan mode).
type roKey struct{}
type zdlKey struct{}

func WithReadOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, roKey{}, true)
}

func IsReadOnly(ctx context.Context) bool {
	v, _ := ctx.Value(roKey{}).(bool)
	return v
}

// WithZeroLeak marks a context where any network-exfiltrating tool is blocked.
func WithZeroLeak(ctx context.Context) context.Context {
	return context.WithValue(ctx, zdlKey{}, true)
}

func IsZeroLeak(ctx context.Context) bool {
	v, _ := ctx.Value(zdlKey{}).(bool)
	return v
}

type GitTool struct{ Workdir string }

func (GitTool) Name() string { return "git" }
func (GitTool) Description() string {
	return "Git operations. Args: action (status|diff|log|branch|show read-only; add|commit|checkout|push|create_branch need write mode), args (extra CLI args; for commit, args='force' overrides secret-scan block), message (for commit)."
}
func (GitTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"args":{"type":"string"},"message":{"type":"string"}}}`
}

var gitReadOnlyActions = map[string]bool{
	"status": true, "diff": true, "log": true, "branch": true, "show": true, "remote": true,
}

func (t *GitTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action  string `json:"action"`
		Args    string `json:"args"`
		Message string `json:"message"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	mutating := !gitReadOnlyActions[a.Action]
	if mutating && IsReadOnly(ctx) {
		return "", fmt.Errorf("git %s is blocked in read-only mode", a.Action)
	}
	var cli []string
	switch a.Action {
	case "status":
		cli = []string{"status", "--short", "--branch"}
	case "diff":
		cli = []string{"diff", "HEAD", "--"}
	case "log":
		cli = []string{"log", "--oneline", "-15"}
	case "branch":
		cli = []string{"branch", "-vv"}
	case "show":
		cli = []string{"show", "--stat", "HEAD"}
	case "add":
		cli = []string{"add", "--", a.Args}
	case "commit":
		if a.Message == "" {
			return "", fmt.Errorf("commit needs a message")
		}
		if !strings.Contains(a.Args, "force") {
			if findings := t.scanWorkdir(ctx); len(findings) > 0 {
				return findings, fmt.Errorf("commit blocked: possible secrets in diff (re-run with args 'force' to override)")
			}
		}
		cli = []string{"commit", "-m", a.Message}
	case "checkout":
		if a.Args == "" {
			return "", fmt.Errorf("checkout needs a branch")
		}
		cli = []string{"checkout", a.Args}
	case "create_branch":
		if a.Args == "" {
			return "", fmt.Errorf("create_branch needs a name")
		}
		cli = []string{"checkout", "-b", strings.Fields(a.Args)[0]}
	case "push":
		cli = []string{"push"}
	default:
		return "", fmt.Errorf("unknown git action %q", a.Action)
	}
	if a.Args != "" && (a.Action == "diff" || a.Action == "log" || a.Action == "show" || a.Action == "status") {
		cli = append(cli, strings.Fields(a.Args)...)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", cli...)
	if t.Workdir != "" {
		cmd.Dir = t.Workdir
	}
	out, err := cmd.CombinedOutput()
	if len(out) > maxOutBytes {
		out = append(out[:maxOutBytes], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("git %s failed: %v", a.Action, err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "(clean)", nil
	}
	return string(out), nil
}

func (t *GitTool) scanWorkdir(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "HEAD", "--")
	if t.Workdir != "" {
		cmd.Dir = t.Workdir
	}
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) == 0 {
		return ""
	}
	fs := security.ScanSecrets(string(out))
	if len(fs) == 0 {
		return ""
	}
	return security.FormatFindings(fs)
}
