package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Yash-K-Jagani/ycode/internal/gguf"
	"github.com/Yash-K-Jagani/ycode/internal/providers/registry"
)

type ModelsTool struct{ Workdir string }

func (ModelsTool) Name() string { return "models" }
func (ModelsTool) Description() string {
	return "Local model files. Args: action (info|import), path (.gguf file, required), name (for import, default file stem). info is read-only; import registers with Ollama."
}
func (ModelsTool) Schema() string {
	return `{"type":"object","required":["action","path"],"properties":{"action":{"type":"string"},"path":{"type":"string"},"name":{"type":"string"}}}`
}

func (t *ModelsTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Name   string `json:"name"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	if a.Path == "" {
		return "", fmt.Errorf("path required")
	}
	switch a.Action {
	case "info":
		info, err := gguf.Parse(resolve(t.Workdir, a.Path))
		if err != nil {
			return "", err
		}
		return info.Summary(), nil
	case "import":
		if IsReadOnly(ctx) {
			return "", fmt.Errorf("models import is blocked in read-only mode")
		}
		return registry.ImportGGUF(ctx, a.Path, a.Name)
	default:
		return "", fmt.Errorf("unknown models action %q (info|import)", a.Action)
	}
}
