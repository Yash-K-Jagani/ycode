package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type NotebookTool struct{ Workdir string }

func (NotebookTool) Name() string { return "notebook" }
func (NotebookTool) Description() string {
	return "Read Jupyter notebooks natively; execute only if jupyter exists. Args: action (cells|execute), path (required .ipynb)."
}
func (NotebookTool) Schema() string {
	return `{"type":"object","required":["action","path"],"properties":{"action":{"type":"string"},"path":{"type":"string"}}}`
}

type nbCell struct {
	CellType string   `json:"cell_type"`
	Source   []string `json:"source"`
	Outputs  []struct {
		OutputType string              `json:"output_type"`
		Text       []string            `json:"text"`
		Data       map[string][]string `json:"data,omitempty"`
	} `json:"outputs,omitempty"`
}

func (t *NotebookTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Action string `json:"action"`
		Path   string `json:"path"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return "", err
	}
	p := resolve(t.Workdir, a.Path)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if len(data) > 2*1024*1024 {
		return "", fmt.Errorf("notebook too large")
	}
	switch a.Action {
	case "cells":
		var nb struct {
			Cells []nbCell `json:"cells"`
		}
		if err := json.Unmarshal(data, &nb); err != nil {
			return "", fmt.Errorf("not a notebook: %w", err)
		}
		var b strings.Builder
		for i, c := range nb.Cells {
			src := truncate(strings.Join(c.Source, ""), 1500)
			fmt.Fprintf(&b, "--- cell %d [%s] ---\n%s\n", i, c.CellType, src)
			for _, o := range c.Outputs {
				txt := strings.Join(o.Text, "")
				if len(o.Data["text/plain"]) > 0 {
					txt = strings.Join(o.Data["text/plain"], "")
				}
				if strings.TrimSpace(txt) != "" {
					fmt.Fprintf(&b, "[out] %s\n", truncate(txt, 800))
				}
			}
		}
		if len(nb.Cells) == 0 {
			return "(no cells)", nil
		}
		return b.String(), nil
	case "execute":
		if IsReadOnly(ctx) {
			return "", fmt.Errorf("notebook execute is blocked in read-only mode")
		}
		if _, err := exec.LookPath("jupyter"); err != nil {
			return "", fmt.Errorf("jupyter not installed — read cells with action=cells, or run code via the run tool")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "jupyter", "nbconvert", "--to", "notebook", "--execute",
			"--stdout", "--allow-errors", p)
		out, err := cmd.CombinedOutput()
		if len(out) > maxOutBytes {
			out = append(out[:maxOutBytes], []byte("\n…(truncated)")...)
		}
		if err != nil {
			return string(out), fmt.Errorf("execute failed: %v", err)
		}
		return fmt.Sprintf("executed ok (%d bytes of notebook JSON returned)", len(out)), nil
	default:
		return "", fmt.Errorf("unknown notebook action %q", a.Action)
	}
}
