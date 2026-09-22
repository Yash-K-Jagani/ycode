package registry

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/gguf"
)

// ImportGGUF validates a .gguf file and registers it with Ollama
// (ollama create). Name defaults to the file stem.
func ImportGGUF(ctx context.Context, path, name string) (string, error) {
	info, err := gguf.Parse(path)
	if err != nil {
		return "", fmt.Errorf("not a usable gguf: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	if strings.ContainsAny(name, " \t\"'") || name == "" {
		return "", fmt.Errorf("bad model name %q", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	modelfile := "FROM " + abs + "\n"
	if info.Architecture != "" {
		modelfile += fmt.Sprintf("# arch=%s tensors=%d\n", info.Architecture, info.Tensors)
	}
	tmp, err := os.CreateTemp("", "Modelfile-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(modelfile); err != nil {
		return "", err
	}
	_ = tmp.Close()
	cmd := exec.CommandContext(ctx, "ollama", "create", name, "-f", tmp.Name())
	out, err := cmd.CombinedOutput()
	if len(out) > 4*1024 {
		out = append(out[:4*1024], []byte("\n…(truncated)")...)
	}
	if err != nil {
		return string(out), fmt.Errorf("ollama create failed: %v", err)
	}
	return fmt.Sprintf("imported %s as %q\n%s", info.Summary(), name, string(out)), nil
}
