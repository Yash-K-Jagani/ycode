package registry

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Pull installs a model via `ollama pull`. Reports combined output.
func Pull(ctx context.Context, model string) (string, error) {
	if model == "" {
		return "", fmt.Errorf("model name required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ollama", "pull", model)
	out, err := cmd.CombinedOutput()
	if len(out) > 8*1024 {
		out = append(out[:8*1024], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("ollama pull failed: %v", err)
	}
	if len(out) == 0 {
		return "pulled " + model, nil
	}
	return string(out), nil
}
