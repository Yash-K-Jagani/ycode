package tools

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// tryFormat best-effort formats a file after write/edit. Returns "" or " · formatted" note.
// Never fails the main operation.
func tryFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		if _, err := exec.LookPath("gofmt"); err == nil {
			_ = exec.Command("gofmt", "-w", path).Run()
			return " · formatted"
		}
		if _, err := exec.LookPath("go"); err == nil {
			_ = exec.Command("go", "fmt", path).Run()
			return " · formatted"
		}
	case ".py":
		if _, err := exec.LookPath("black"); err == nil {
			_ = exec.Command("black", "-q", path).Run()
			return " · formatted"
		}
		if _, err := exec.LookPath("ruff"); err == nil {
			_ = exec.Command("ruff", "format", path).Run()
			return " · formatted"
		}
	case ".js", ".ts", ".tsx", ".jsx", ".json", ".css", ".html", ".yaml", ".yml", ".md":
		if _, err := exec.LookPath("prettier"); err == nil {
			_ = exec.Command("prettier", "--write", "--log-level", "silent", path).Run()
			return " · formatted"
		}
	}
	return ""
}
