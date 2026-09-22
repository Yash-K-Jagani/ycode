package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelsTool(t *testing.T) {
	dir := t.TempDir()
	m := &ModelsTool{Workdir: dir}
	ctx := context.Background()
	if _, err := m.Run(ctx, json.RawMessage(`{"action":"info"}`)); err == nil {
		t.Fatal("expected path error")
	}
	if _, err := m.Run(ctx, json.RawMessage(`{"action":"info","path":"nope.gguf"}`)); err == nil {
		t.Fatal("expected missing-file error")
	}
	bad := filepath.Join(dir, "bad.gguf")
	_ = os.WriteFile(bad, []byte("NOPE"), 0o644)
	if _, err := m.Run(ctx, json.RawMessage(`{"action":"info","path":"bad.gguf"}`)); err == nil {
		t.Fatal("expected bad-magic error")
	}
	if _, err := m.Run(ctx, json.RawMessage(`{"action":"explode","path":"bad.gguf"}`)); err == nil {
		t.Fatal("expected bad-action error")
	}
	ro := WithReadOnly(ctx)
	if _, err := m.Run(ro, json.RawMessage(`{"action":"import","path":"bad.gguf"}`)); err == nil {
		t.Fatal("expected read-only block on import")
	}
	// info works read-only; bad magic surfaces the gguf error:
	if _, err := m.Run(ro, json.RawMessage(`{"action":"info","path":"bad.gguf"}`)); err == nil || !strings.Contains(err.Error(), "GGUF") {
		t.Fatalf("want gguf parse error, got %v", err)
	}
}
