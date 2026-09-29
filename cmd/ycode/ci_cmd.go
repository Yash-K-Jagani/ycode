package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/headless"
	"github.com/Yash-K-Jagani/ycode/internal/httpx"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/textutil"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/spf13/cobra"
)

// githubRequestTimeout bounds a single api.github.com call made by `ycode ci`.
// Generous, because posting a review means uploading a diff, and a CI runner
// with no deadline at all is a job that hangs until the platform kills it with
// no output and no error to show for it.
const githubRequestTimeout = 2 * time.Minute

func ciCmd() *cobra.Command {
	var post bool
	c := &cobra.Command{
		Use:   "ci",
		Short: "CI: review current diff + run tests, print markdown summary",
		Run: func(cmd *cobra.Command, args []string) {
			summary, failed := ciSummary()
			fmt.Println(summary)
			if post {
				if err := postPRComment(summary); err != nil {
					fmt.Println("comment:", err)
				}
			}
			if failed {
				os.Exit(1)
			}
		},
	}
	c.Flags().BoolVar(&post, "post", false, "post summary as PR comment (needs GH_TOKEN + PR context)")
	return c
}

func ciSummary() (string, bool) {
	var b strings.Builder
	ctx := context.Background()
	workdir, _ := os.Getwd()
	cfg, _ := config.Load()
	raw, _ := json.Marshal(map[string]string{"action": "diff"})
	diff, derr := (&tools.GitTool{Workdir: workdir}).Run(ctx, raw)
	raw2, _ := json.Marshal(map[string]string{"path": workdir})
	testOut, terr := (&tools.TestGenTool{Workdir: workdir}).Run(ctx, raw2)
	b.WriteString("<!-- ycode-ci -->\n## ycode CI summary\n\n")
	if derr != nil || strings.TrimSpace(diff) == "" || diff == "(clean)" {
		b.WriteString("Diff: (clean or unavailable)\n")
	} else {
		// A byte slice here would cut a diff mid-character and send invalid
		// UTF-8 to the model and into the PR comment.
		if len([]rune(diff)) > 20000 {
			diff = textutil.Truncate(diff, 20000) + "\n...(truncated)"
		}
		answer, err := headless.Run(ctx, cfg, "Review this diff briefly (summary + top risks):\n```diff\n"+diff+"\n```", headless.Options{Mode: modes.Chat, Workdir: workdir, Stderr: os.Stderr})
		if err != nil {
			b.WriteString("Review: unavailable (" + err.Error() + ")\n")
		} else {
			b.WriteString(answer + "\n")
		}
	}
	b.WriteString("\n")
	failed := false
	if terr != nil {
		failed = true
		b.WriteString("Tests: FAILED\n```\n" + truncate(testOut, 4000) + "\n```\n")
	} else {
		b.WriteString("Tests: PASSED\n```\n" + truncate(testOut, 4000) + "\n```\n")
	}
	return b.String(), failed
}

// postPRComment upserts the summary as a PR comment (create or update by marker).
func postPRComment(body string) error {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("GH_TOKEN not set")
	}
	owner, repo, err := ciRepo()
	if err != nil {
		return err
	}
	pr, err := ciPRNumber()
	if err != nil {
		return err
	}
	api := func(method, path string, payload any) (int, []byte, error) {
		var rdr io.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			rdr = bytes.NewReader(b)
		}
		// Bounded. This call went out on context.Background() with
		// http.DefaultClient, so it had no deadline and no connection pool:
		// a CI job that hit a stalled api.github.com hung until the runner
		// killed it, having produced no output and no error.
		ctx, cancel := context.WithTimeout(context.Background(), githubRequestTimeout)
		defer cancel()
		req, err := httpx.NewRequest(ctx, method, "https://api.github.com"+path, rdr)
		if err != nil {
			return 0, nil, fmt.Errorf("github request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "Bearer "+token)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := httpx.BoundedByContext().Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return resp.StatusCode, data, nil
	}
	code, data, err := api("GET", fmt.Sprintf("/repos/%s/%s/issues/%s/comments?per_page=100", owner, repo, pr), nil)
	if err != nil {
		return err
	}
	if code >= 400 {
		return fmt.Errorf("list comments %d: %s", code, truncate(string(data), 300))
	}
	var comments []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	_ = json.Unmarshal(data, &comments)
	for _, c := range comments {
		if strings.Contains(c.Body, "<!-- ycode-ci -->") {
			code, data, err := api("PATCH", fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, c.ID), map[string]string{"body": body})
			if err != nil || code >= 400 {
				return fmt.Errorf("update comment: %v %s", err, truncate(string(data), 200))
			}
			fmt.Println("updated PR comment")
			return nil
		}
	}
	code, data, err = api("POST", fmt.Sprintf("/repos/%s/%s/issues/%s/comments", owner, repo, pr), map[string]string{"body": body})
	if err != nil || code >= 400 {
		return fmt.Errorf("create comment: %v %s", err, truncate(string(data), 200))
	}
	fmt.Println("posted PR comment")
	return nil
}

func ciRepo() (string, string, error) {
	if slug := os.Getenv("GITHUB_REPOSITORY"); slug != "" {
		parts := strings.SplitN(slug, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("no GITHUB_REPOSITORY and no git origin")
	}
	u := strings.TrimSpace(string(out))
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndex(u, ":"); strings.Contains(u, "@") && i >= 0 {
		u = u[i+1:]
	} else if strings.HasPrefix(u, "https://") {
		u = strings.TrimPrefix(u, "https://github.com/")
	}
	parts := strings.SplitN(u, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("cannot parse owner/repo from %q", u)
	}
	return parts[0], parts[1], nil
}

func ciPRNumber() (string, error) {
	if n := os.Getenv("GH_PR"); n != "" {
		return n, nil
	}
	if ref := os.Getenv("GITHUB_REF"); ref != "" {
		parts := strings.Split(ref, "/")
		if len(parts) == 4 && parts[1] == "pull" {
			return parts[2], nil
		}
	}
	return "", fmt.Errorf("set GH_PR or run from a pull_request event")
}
