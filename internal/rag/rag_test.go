package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/embed"
)

func fakeEmbed(ctx context.Context, inputs []string) ([][]float64, error) {
	vecs := make([][]float64, len(inputs))
	for i, in := range inputs {
		// toy embedding: char histogram of first 8 letters
		v := make([]float64, 8)
		for _, r := range strings.ToLower(in) {
			if r >= 'a' && r < 'a'+8 {
				v[r-'a']++
			}
		}
		vecs[i] = v
	}
	return vecs, nil
}

func TestChunkText(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("line\n")
	}
	ch := ChunkText("f.go", sb.String())
	if len(ch) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(ch))
	}
	if !strings.Contains(ch[0].Text, "line") {
		t.Fatal("bad chunk text")
	}
}

func TestIngestQuery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	db.ResetSharedForTest()
	t.Cleanup(db.ResetSharedForTest)
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("apple.go", "package apple\n// apple apple apple\nfunc Apple() {}\n")
	write("zebra.go", "package zebra\n// zebra zebra zebra\nfunc Zebra() {}\n")
	write("big.bin", "xxxx")
	idx, err := Ingest(context.Background(), dir, dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Chunks) == 0 {
		t.Fatal("no chunks")
	}
	// save/load roundtrip
	loaded, ok := Load(dir)
	if !ok || len(loaded.Chunks) != len(idx.Chunks) {
		t.Fatal("load failed")
	}
	qv, _ := fakeEmbed(context.Background(), []string{"apple apple"})
	top := Query(loaded, qv[0], 1)
	if len(top) != 1 || !strings.Contains(top[0].Path, "apple") {
		t.Fatalf("bad retrieval: %+v", top)
	}
	if FormatContext(top) == "" {
		t.Fatal("empty format")
	}
}

func TestSQLSaveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	db.ResetSharedForTest()
	t.Cleanup(db.ResetSharedForTest)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "apple.go"), []byte("package apple\n// apple apple apple\nfunc Apple() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "zebra.go"), []byte("package zebra\n// zebra zebra zebra\nfunc Zebra() {}\n"), 0o644)
	idx, err := Ingest(context.Background(), dir, dir, fakeEmbed)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Chunks) == 0 {
		t.Fatal("no chunks")
	}
	loaded, ok := Load(dir)
	if !ok || len(loaded.Chunks) != len(idx.Chunks) {
		t.Fatalf("sql load: %v %d", ok, len(loaded.Chunks))
	}
	qv, _ := fakeEmbed(context.Background(), []string{"apple apple"})
	if top := Query(loaded, qv[0], 1); len(top) != 1 {
		t.Fatal("no retrieval from sql index")
	}
}

func TestLiveEmbedIngest(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	em := embed.New("", "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := em.Embed(ctx, []string{"ping"}); err != nil {
		t.Skipf("no embed model: %v", err)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "router.go"), []byte("package router\n// latency stats and cloud fallbacks\nfunc Fallback() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "cache.go"), []byte("package cache\n// semantic similarity cache\nfunc Lookup() {}\n"), 0o644)
	idx, err := Ingest(ctx, dir, dir, em.Embed)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Chunks) == 0 {
		t.Fatal("no chunks")
	}
	qv, err := em.Embed(ctx, []string{"cloud fallback provider retry"})
	if err != nil {
		t.Fatal(err)
	}
	top := Query(idx, qv[0], 2)
	if len(top) == 0 {
		t.Fatal("no results")
	}
	t.Logf("top hit: %s", top[0].Path)
}
