package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var httpURLRe = regexp.MustCompile(`^https?://[^\s"'<>]+$`)

func token() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

type GitHubTool struct{ Workdir string }

func (GitHubTool) Name() string { return "github" }
func (GitHubTool) Description() string {
	return "GitHub: clone a repo from URL or owner/repo shorthand, list/create PRs and issues, post PR reviews. Args: action (clone|pr_list|pr_view|issue_create|review_post), url (for clone), repo (owner/name), title/body/number as needed, review (for review_post: {summary, comments:[{path,line,body}]})."
}
func (GitHubTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"url":{"type":"string"},"repo":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"number":{"type":"integer"},"dest":{"type":"string"},"review":{"type":"object"}}}`
}

type reviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

type githubArgs struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Number int    `json:"number"`
	Dest   string `json:"dest"`
	Review struct {
		Summary  string          `json:"summary"`
		Comments []reviewComment `json:"comments"`
	} `json:"review"`
}

func (t *GitHubTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a githubArgs
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsZeroLeak(ctx) {
		return "", fmt.Errorf("github is blocked in zero-data-leak mode")
	}
	switch a.Action {
	case "clone":
		url := a.URL
		if isRepoShorthand(url) {
			url = "https://github.com/" + url + ".git"
		}
		return gitClone(ctx, t.Workdir, url, a.Dest)
	case "pr_list", "pr_view", "issue_create":
		if IsReadOnly(ctx) && a.Action == "issue_create" {
			return "", fmt.Errorf("github issue_create is blocked in read-only mode")
		}
		return ghAPI(ctx, a)
	case "review_post":
		if IsReadOnly(ctx) {
			return "", fmt.Errorf("github review_post is blocked in read-only mode")
		}
		return postReview(ctx, a)
	default:
		return "", fmt.Errorf("unknown github action %q", a.Action)
	}
}

func isRepoShorthand(s string) bool {
	if strings.Contains(s, "://") || !strings.Contains(s, "/") {
		return false
	}
	parts := strings.Split(s, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// GitHubRepo resolves owner/repo from the workdir git origin.
func GitHubRepo(workdir string) (string, string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	if workdir != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("no git origin in %s", workdir)
	}
	u := strings.TrimSuffix(strings.TrimSpace(string(out)), ".git")
	if i := strings.LastIndex(u, ":"); strings.Contains(u, "@") && i >= 0 {
		u = u[i+1:]
	} else if strings.HasPrefix(u, "https://") {
		u = strings.TrimPrefix(u, "https://github.com/")
	}
	parts := strings.SplitN(u, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[1], "/") {
		return "", "", fmt.Errorf("origin is not a GitHub repo: %q", u)
	}
	return parts[0], parts[1], nil
}

func gitClone(ctx context.Context, workdir, url, dest string) (string, error) {
	if !httpURLRe.MatchString(url) {
		return "", fmt.Errorf("refusing to clone non-http(s) URL: %q", url)
	}
	if !strings.Contains(url, "github.com") && !strings.Contains(url, "gitlab.com") {
		return "", fmt.Errorf("only github.com/gitlab.com URLs allowed: %q", url)
	}
	if dest == "" {
		base := url[strings.LastIndex(url, "/")+1:]
		dest = strings.TrimSuffix(base, ".git")
	}
	target := resolve(workdir, dest)
	if _, err := os.Stat(target); err == nil {
		return "", fmt.Errorf("destination already exists: %s", target)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth=1", url, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("clone failed: %v", err)
	}
	return fmt.Sprintf("cloned %s → %s", url, target), nil
}

func ghAPI(ctx context.Context, a githubArgs) (string, error) {
	tk := token()
	var endpoint, method string
	var payload any
	switch a.Action {
	case "pr_list":
		method, endpoint = "GET", fmt.Sprintf("https://api.github.com/repos/%s/pulls?state=open&per_page=20", a.Repo)
	case "pr_view":
		method, endpoint = "GET", fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", a.Repo, a.Number)
	case "issue_create":
		method, endpoint = "POST", fmt.Sprintf("https://api.github.com/repos/%s/issues", a.Repo)
		payload = map[string]string{"title": a.Title, "body": a.Body}
	}
	var body io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = strings.NewReader(string(b))
	}
	req, _ := http.NewRequestWithContext(ctx, method, endpoint, body)
	req.Header.Set("Accept", "application/vnd.github+json")
	if tk != "" {
		req.Header.Set("Authorization", "Bearer "+tk)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("github %d: %s", resp.StatusCode, truncate(string(data), 500))
	}
	return summarizeGH(a.Action, data)
}

// postReview creates a PR review with inline comments (line = new-side line).
func postReview(ctx context.Context, a githubArgs) (string, error) {
	if a.Repo == "" || a.Number == 0 {
		return "", fmt.Errorf("review_post needs repo (owner/name) and number")
	}
	if len(a.Review.Comments) == 0 && a.Review.Summary == "" {
		return "", fmt.Errorf("review_post needs review.summary and/or review.comments")
	}
	tk := token()
	if tk == "" {
		return "", fmt.Errorf("GH_TOKEN/GITHUB_TOKEN not set")
	}
	comments := make([]map[string]any, 0, len(a.Review.Comments))
	for _, c := range a.Review.Comments {
		if c.Path == "" || c.Line <= 0 || c.Body == "" {
			return "", fmt.Errorf("each comment needs path, line (>0), body")
		}
		comments = append(comments, map[string]any{
			"path": c.Path, "line": c.Line, "side": "RIGHT", "body": c.Body,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"body": a.Review.Summary, "event": "COMMENT", "comments": comments,
	})
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/reviews", a.Repo, a.Number)
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(payload))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+tk)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("github %d: %s", resp.StatusCode, truncate(string(data), 500))
	}
	var v struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(data, &v)
	return fmt.Sprintf("posted review #%d %s (%d inline comments)", v.ID, v.HTMLURL, len(comments)), nil
}

func summarizeGH(action string, data []byte) (string, error) {
	switch action {
	case "pr_list":
		var prs []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(data, &prs); err != nil {
			return "", err
		}
		if len(prs) == 0 {
			return "(no open PRs)", nil
		}
		var b strings.Builder
		for _, p := range prs {
			fmt.Fprintf(&b, "#%d %s (@%s)\n", p.Number, p.Title, p.User.Login)
		}
		return b.String(), nil
	case "pr_view":
		var p struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return "", err
		}
		return fmt.Sprintf("#%d [%s] %s\n\n%s", p.Number, p.State, p.Title, truncate(p.Body, 3000)), nil
	default:
		var v struct {
			Number  int    `json:"number"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return truncate(string(data), 1000), nil
		}
		return fmt.Sprintf("created #%d %s", v.Number, v.HTMLURL), nil
	}
}
