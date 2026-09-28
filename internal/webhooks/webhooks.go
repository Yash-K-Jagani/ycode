package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func loadFile() []Target {
	for _, p := range []string{filepath.Join(config.Dir(), "webhooks.yaml")} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var v struct {
			Webhooks []Target `yaml:"webhooks"`
		}
		if err := yaml.Unmarshal(data, &v); err == nil {
			return v.Webhooks
		}
	}
	return nil
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
			_ = resp.Body.Close()
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
