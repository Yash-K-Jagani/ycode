package ycodeclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	Base string
	HTTP *http.Client
}

func New(base string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: 20 * time.Minute}}
}

func (c *Client) get(path string, out any) error {
	resp, err := c.HTTP.Get(c.Base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fmt.Errorf("%s %d: %s", path, resp.StatusCode, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Models() (map[string]any, error) {
	var v map[string]any
	return v, c.get("/v1/models", &v)
}

func (c *Client) Status() (map[string]any, error) {
	var v map[string]any
	return v, c.get("/v1/status", &v)
}

func (c *Client) Chat(prompt, mode, agent, workdir string) (string, error) {
	body, _ := json.Marshal(map[string]string{"prompt": prompt, "mode": mode, "agent": agent, "workdir": workdir})
	resp, err := c.HTTP.Post(c.Base+"/v1/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return "", fmt.Errorf("chat %d: %s", resp.StatusCode, string(b))
	}
	var v struct {
		Answer string `json:"answer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Answer, nil
}
