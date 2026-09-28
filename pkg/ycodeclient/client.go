// Package ycodeclient is the Go SDK for the ycode local API.
package ycodeclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	Base  string
	HTTP  *http.Client
	Token string
}

// New returns a client. The token is read from YCODE_API_TOKEN, then from
// ~/.ycode/api_token, so a client talking to a non-loopback ycode serve works
// without configuration.
func New(base string) *Client {
	return &Client{Base: strings.TrimSuffix(base, "/"), HTTP: &http.Client{Timeout: 20 * time.Minute}, Token: defaultToken()}
}

// NewWithToken returns a client that sends an explicit token.
func NewWithToken(base, token string) *Client {
	c := New(base)
	if token != "" {
		c.Token = token
	}
	return c
}

func defaultToken() string {
	if t := strings.TrimSpace(os.Getenv("YCODE_API_TOKEN")); t != "" {
		return t
	}
	if home, err := os.UserHomeDir(); err == nil {
		if data, err := os.ReadFile(filepath.Join(home, ".ycode", "api_token")); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

func (c *Client) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("%s %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Models() (map[string]any, error) {
	var v map[string]any
	return v, c.do(http.MethodGet, "/v1/models", nil, &v)
}

func (c *Client) Status() (map[string]any, error) {
	var v map[string]any
	return v, c.do(http.MethodGet, "/v1/status", nil, &v)
}

// Chat runs one turn. goalStatus is empty for non-goal modes, and otherwise
// says how the run ended ("met", "blocked", "budget_exhausted", "stalled") —
// so a caller can tell a finished job from a model that claimed one.
func (c *Client) Chat(prompt, mode, agent, workdir string) (answer, goalStatus string, err error) {
	var v struct {
		Answer     string `json:"answer"`
		GoalStatus string `json:"goal_status"`
	}
	body := map[string]string{"prompt": prompt, "mode": mode, "agent": agent, "workdir": workdir}
	if err := c.do(http.MethodPost, "/v1/chat", body, &v); err != nil {
		return "", "", err
	}
	return v.Answer, v.GoalStatus, nil
}

// Health is a liveness probe. It never requires a token.
func (c *Client) Health() error {
	return c.do(http.MethodGet, "/healthz", nil, nil)
}
