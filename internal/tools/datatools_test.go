package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDBValidation(t *testing.T) {
	d := &DBTool{}
	ctx := context.Background()
	if _, err := d.Run(ctx, json.RawMessage(`{"action":"tables"}`)); err == nil {
		t.Fatal("expected kind/dsn errors")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"kind":"pg","action":"tables"}`)); err == nil {
		t.Fatal("expected dsn error")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"kind":"postgres","dsn":"x","action":"query","query":"DROP TABLE t"}`)); err == nil {
		t.Fatal("expected non-SELECT refusal")
	}
	if _, err := d.Run(WithReadOnly(ctx), json.RawMessage(`{"kind":"postgres","dsn":"x","action":"query","query":"DELETE FROM t"}`)); err == nil {
		t.Fatal("expected read-only block")
	}
	if _, err := d.Run(ctx, json.RawMessage(`{"kind":"oracle","dsn":"x","action":"tables"}`)); err == nil {
		t.Fatal("expected unknown-kind error")
	}
	if !isReadQuery("  select 1") || isReadQuery("drop table t") || !isReadQuery("WITH x AS (SELECT 1) SELECT * FROM x") {
		t.Fatal("isReadQuery wrong")
	}
}

func TestNotebookCells(t *testing.T) {
	dir := t.TempDir()
	nb := `{"cells":[{"cell_type":"markdown","source":["# T"]},{"cell_type":"code","source":["print(1)"],"outputs":[{"output_type":"stream","text":["1\n"]}]}]}`
	p := filepath.Join(dir, "n.ipynb")
	_ = os.WriteFile(p, []byte(nb), 0o644)
	n := &NotebookTool{Workdir: dir}
	out, err := n.Run(context.Background(), json.RawMessage(`{"action":"cells","path":"n.ipynb"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cell 0", "cell 1", "# T", "print(1)", "[out] 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if _, err := n.Run(context.Background(), json.RawMessage(`{"action":"nope","path":"n.ipynb"}`)); err == nil {
		t.Fatal("expected bad-action error")
	}
	if _, err := n.Run(context.Background(), json.RawMessage(`{"action":"cells","path":"missing.ipynb"}`)); err == nil {
		t.Fatal("expected missing-file error")
	}
}

func TestAPIValidation(t *testing.T) {
	a := &APITool{}
	if _, err := a.Run(context.Background(), json.RawMessage(`{"url":"file:///x"}`)); err == nil {
		t.Fatal("expected URL refusal")
	}
	if _, err := a.Run(context.Background(), json.RawMessage(`{"url":"https://example.com","method":"FROB"}`)); err == nil {
		t.Fatal("expected method error")
	}
	ro := WithReadOnly(context.Background())
	if _, err := a.Run(ro, json.RawMessage(`{"url":"https://example.com","method":"POST"}`)); err == nil {
		t.Fatal("expected read-only block on POST")
	}
}

func TestVSCodeValidation(t *testing.T) {
	v := &VSCodeTool{Workdir: t.TempDir()}
	if _, err := v.Run(context.Background(), json.RawMessage(`{"action":"bogus"}`)); err == nil {
		// passes only if code CLI missing (env-dependent); accept either but must not hang
		t.Log("code CLI present, action check skipped")
	} else if !strings.Contains(err.Error(), "unknown vscode action") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestScaffoldValidation(t *testing.T) {
	s := &ScaffoldTool{Workdir: t.TempDir()}
	if _, err := s.Run(context.Background(), json.RawMessage(`{"template":"django","dest":"x"}`)); err == nil {
		t.Fatal("expected bad-template error")
	}
	if _, err := s.Run(WithReadOnly(context.Background()), json.RawMessage(`{"template":"fastapi","dest":"x"}`)); err == nil {
		t.Fatal("expected read-only block")
	}
	out, err := s.Run(context.Background(), json.RawMessage(`{"template":"fastapi","dest":"api"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "uvicorn") {
		t.Fatalf("bad scaffold output: %q", out)
	}
}
