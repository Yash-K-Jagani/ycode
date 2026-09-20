package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	tagRe   = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlRe  = regexp.MustCompile(`<[^>]+>`)
	spaceRe = regexp.MustCompile(`[ \t\xa0]+`)
)

type BrowserTool struct{ Allowlist []string }

func (BrowserTool) Name() string { return "browser" }
func (BrowserTool) Description() string {
	return "Fetch a URL and return main text (scripts/styles stripped). Args: url (required, http/https)."
}
func (BrowserTool) Schema() string {
	return `{"type":"object","required":["url"],"properties":{"url":{"type":"string"}}}`
}

func (t *BrowserTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if IsZeroLeak(ctx) {
		return "", fmt.Errorf("browser is blocked in zero-data-leak mode")
	}
	if !httpURLRe.MatchString(a.URL) {
		return "", fmt.Errorf("refusing non-http(s) URL: %q", a.URL)
	}
	if len(t.Allowlist) > 0 {
		ok := false
		for _, host := range t.Allowlist {
			if strings.Contains(a.URL, host) {
				ok = true
				break
			}
		}
		if !ok {
			return "", fmt.Errorf("URL host not in browser allowlist")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	req.Header.Set("User-Agent", "ycode/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("fetch %d for %s", resp.StatusCode, a.URL)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	text := ExtractText(string(data))
	if len(text) > 24*1024 {
		text = text[:24*1024] + "\n…(truncated)"
	}
	if strings.TrimSpace(text) == "" {
		return "(no readable text)", nil
	}
	return text, nil
}

func ExtractText(html string) string {
	s := tagRe.ReplaceAllString(html, " ")
	s = htmlRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	lines := strings.Split(s, "\n")
	var kept []string
	for _, ln := range lines {
		ln = strings.TrimSpace(spaceRe.ReplaceAllString(ln, " "))
		if ln != "" {
			kept = append(kept, ln)
		}
	}
	return strings.Join(kept, "\n")
}
