package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/httpx"
	"gopkg.in/yaml.v3"
)

type Target struct {
	URL    string   `yaml:"url"`
	Events []string `yaml:"events"`
}

// loadFile reads the webhook list. A file that exists but does not parse is a
// configuration mistake, and swallowing it meant webhooks silently stopped
// firing with nothing on screen to say why - so the parse error is reported.
func loadFile() []Target {
	data, err := os.ReadFile(filepath.Join(config.Dir(), "webhooks.yaml"))
	if err != nil {
		return nil
	}
	var v struct {
		Webhooks []Target `yaml:"webhooks"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		fmt.Fprintf(os.Stderr, "webhooks: cannot parse %s: %v\n", filepath.Join(config.Dir(), "webhooks.yaml"), err)
		return nil
	}
	return v.Webhooks
}

// Fire POSTs event payload to matching targets (best-effort, 5s timeout each).
func Fire(event string, payload map[string]any) {
	targets := loadFile()
	if len(targets) == 0 {
		return
	}
	payload["event"] = event
	payload["at"] = time.Now().UTC().Format(time.RFC3339)
	body, _ := json.Marshal(payload)
	for _, t := range targets {
		if len(t.Events) > 0 && !contains(t.Events, event) && !contains(t.Events, "*") {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := httpx.NewRequest(ctx, "POST", t.URL, bytes.NewReader(body))
		if err != nil {
			// A malformed url: in webhooks.yaml is a config mistake, not a
			// reason to drop every other delivery in the list.
			fmt.Fprintf(os.Stderr, "webhook %q: %v\n", t.URL, err)
			cancel()
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
			_ = resp.Body.Close()
			// A receiver that rejected the delivery is a fact the user needs.
			// Without this a 401 or 500 was indistinguishable from success, and
			// a broken integration looked like a quiet night.
			if err == nil && resp.StatusCode >= 400 {
				fmt.Fprintf(os.Stderr, "webhook %q: %s\n", t.URL, resp.Status)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhook %q: %v\n", t.URL, err)
		}
		cancel()
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
