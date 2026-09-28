package tools

import (
	"context"
	"strings"
	"testing"
)

// A model-supplied repo name is interpolated straight into the API URL, and
// http.NewRequestWithContext returns a nil *http.Request when that URL fails
// to parse. The old code discarded the error and dereferenced the nil request,
// so a typo (or an injected "%zz" copied from a web page) panicked the TUI
// mid-turn — bubbletea recovers that by cancelling the program, losing the
// unsaved transcript. It must be a readable tool error instead.
func TestGitHubToolRejectsMalformedRepoWithoutPanicking(t *testing.T) {
	tool := &GitHubTool{Workdir: t.TempDir()}
	cases := []struct {
		name string
		args string
	}{
		{"pr_list", `{"action":"pr_list","repo":"acme/api%zz"}`},
		{"pr_view", `{"action":"pr_view","repo":"acme%2","number":1}`},
		{"issue_create", `{"action":"issue_create","repo":"a%zz","title":"t","body":"b"}`},
		{"pr_review", `{"action":"pr_review","repo":"acme/api%zz","number":1,"summary":"s"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// A nil token keeps us off the network; we only care that the
			// malformed URL is handled before any request is attempted.
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_TOKEN", "")
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked instead of returning an error: %v", r)
				}
			}()
			out, err := tool.Run(context.Background(), []byte(c.args))
			if err != nil {
				if !strings.Contains(err.Error(), "github") {
					t.Fatalf("error should name the tool: %v", err)
				}
				return
			}
			// No token means the tool bails earlier; that is also fine, as
			// long as it did not panic.
			if out == "" {
				t.Fatal("expected either an error or a message")
			}
		})
	}
}

func TestGitHubToolRejectsNonHTTPURL(t *testing.T) {
	tool := &GitHubTool{Workdir: t.TempDir()}
	for _, url := range []string{"file:///etc/passwd", "ftp://x/y", "not a url"} {
		if _, err := tool.Run(context.Background(), []byte(`{"action":"clone","url":"`+url+`"}`)); err == nil {
			t.Fatalf("clone accepted %q", url)
		}
	}
}
