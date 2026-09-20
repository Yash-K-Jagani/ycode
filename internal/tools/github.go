package tools

import (
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
	return "GitHub: clone a repo from URL or owner/repo shorthand, list/create PRs and issues. Args: action (clone|pr_list|pr_view|issue_create), url (for clone), repo (owner/name), title/body/number as needed."
}
func (GitHubTool) Schema() string {
	return `{"type":"object","required":["action"],"properties":{"action":{"type":"string"},"url":{"type":"string"},"repo":{"type":"string"},"title":{"type":"string"},"body":{"type":"string"},"number":{"type":"integer"},"dest":{"type":"string"}}}`
}

func (t *GitHubTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		URL    string `json:"url"`
		Repo   string `json:"repo"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Number int    `json:"number"`
		Dest   string `json:"dest"`
	}
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

func ghAPI(ctx context.Context, a struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Number int    `json:"number"`
	Dest   string `json:"dest"`
},
) (string, error) {
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
