package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
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
		req, _ := http.NewRequestWithContext(ctx, "POST", t.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		_, _ = http.DefaultClient.Do(req)
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
