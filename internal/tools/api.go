package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type APITool struct{ Allowlist []string }

func (APITool) Name() string { return "api" }
func (APITool) Description() string {
	return "REST client (Postman-like): any method, headers, JSON body, pretty output. Args: url (required), method (default GET), headers (map, optional), body (JSON string, optional)."
}
func (APITool) Schema() string {
	return `{"type":"object","required":["url"],"properties":{"url":{"type":"string"},"method":{"type":"string"},"headers":{"type":"object"},"body":{"type":"string"}}}`
}

func (t *APITool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if !httpURLRe.MatchString(a.URL) {
		return "", fmt.Errorf("refusing non-http(s) URL: %q", a.URL)
	}
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return "", fmt.Errorf("unsupported method %q", method)
	}
	if IsReadOnly(ctx) && method != "GET" && method != "HEAD" && method != "OPTIONS" {
		return "", fmt.Errorf("non-GET requests blocked in read-only mode")
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
			return "", fmt.Errorf("URL host not in api allowlist")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var body io.Reader
	if a.Body != "" {
		body = strings.NewReader(a.Body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, a.URL, body)
	req.Header.Set("User-Agent", "ycode/1.0")
	if a.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	out := fmt.Sprintf("HTTP %d\n", resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "json") {
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			data = pretty.Bytes()
		}
	}
	out += string(data)
	if len(out) > 24*1024 {
		out = out[:24*1024] + "\n…(truncated)"
	}
	return out, nil
}
