package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func fakeEmbed(ctx context.Context, inputs []string) ([][]float64, error) {
	vecs := make([][]float64, len(inputs))
	for i, in := range inputs {
		v := make([]float64, 4)
		for _, r := range strings.ToLower(in) {
			if r >= 'a' && r < 'a'+4 {
				v[r-'a']++
			}
		}
		vecs[i] = v
	}
	return vecs, nil
}

func TestExactAndSemantic(t *testing.T) {
	c := NewAt(filepath.Join(t.TempDir(), "cache.json"), fakeEmbed)
	c.items = nil
	ctx := context.Background()
	c.Store(ctx, "aaa bbb", "answer-1", "ollama", "m")
	if a, ok := c.Lookup(ctx, "aaa bbb", "ollama", "m"); !ok || a != "answer-1" {
		t.Fatalf("exact miss: %q %v", a, ok)
	}
	// near-duplicate (same histogram) hits semantically
	if a, ok := c.Lookup(ctx, "bbb aaa", "ollama", "m"); !ok || a != "answer-1" {
		t.Fatalf("semantic miss: %q %v", a, ok)
	}
	// different provider must not hit
	if _, ok := c.Lookup(ctx, "aaa bbb", "gemini", "m"); ok {
		t.Fatal("cross-provider hit")
	}
	// unrelated prompt misses
	if _, ok := c.Lookup(ctx, "zzz qqq www", "ollama", "m"); ok {
		t.Fatal("false positive")
	}
	h, m_, _ := c.Stats()
	if h != 2 || m_ != 2 {
		t.Fatalf("bad stats: %d/%d", h, m_)
	}
}
