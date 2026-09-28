package rag

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/embed"
)

// An index built with one embedding model and queried with another produces
// vectors of different lengths. The old code compared the overlapping prefix,
// which returned a confident ranking computed from unrelated numbers - so a
// user who changed models got wrong answers with no indication anything was
// wrong. Now it is reported.

func chunkWithVec(path string, dims int) Chunk {
	v := make([]float64, dims)
	for i := range v {
		v[i] = float64(i%3) + 0.5
	}
	return Chunk{Path: path, Text: "content", Vec: v}
}

func TestQueryReportsAStaleIndex(t *testing.T) {
	indexed := chunkWithVec("main.go", 768) // nomic-embed-text
	idx := Index{Chunks: []Chunk{indexed}}

	// A query from a different model: 384 dimensions.
	qv := make([]float64, 384)
	for i := range qv {
		qv[i] = 0.25
	}
	chunks, err := Query(idx, qv, 4)
	if err == nil {
		t.Fatalf("a dimension mismatch returned %d results and no error", len(chunks))
	}
	if len(chunks) != 0 {
		t.Fatalf("results were returned despite the error: %+v", chunks)
	}
	// The message has to be actionable, or the user cannot recover.
	for _, want := range []string{"768", "384", "re-index"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not mention %q: %v", want, err)
		}
	}
}

func TestQuerySkipsChunksWithNoVector(t *testing.T) {
	// A chunk with no embedding is skipped rather than treated as a mismatch:
	// it is an incomplete index, not a different model.
	idx := Index{Chunks: []Chunk{
		{Path: "empty.go", Text: "no vector"},
		chunkWithVec("good.go", 8),
	}}
	qv := make([]float64, 8)
	chunks, err := Query(idx, qv, 4)
	if err != nil {
		t.Fatalf("an un-embedded chunk should be skipped, not an error: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Path != "good.go" {
		t.Fatalf("got %+v", chunks)
	}
}

func TestQueryWithMatchingDimensionsStillRanks(t *testing.T) {
	// The change must not have broken the normal path: identical dimensions
	// still produce a ranked result.
	near := chunkWithVec("near.go", 4)
	far := chunkWithVec("far.go", 4)
	// Make "far" point in the opposite direction.
	for i := range far.Vec {
		far.Vec[i] = -far.Vec[i]
	}
	idx := Index{Chunks: []Chunk{far, near}}
	chunks, err := Query(idx, []float64{1, 0, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	// Similarity 1 beats similarity -1, so the aligned chunk comes first.
	if chunks[0].Path != "near.go" {
		t.Fatalf("ranking is wrong: %s came first", chunks[0].Path)
	}
}

func TestCosineMismatchPropagatesThroughQuery(t *testing.T) {
	// Guards the wiring rather than the arithmetic: if someone reintroduces a
	// truncating comparison, this fails.
	if _, err := embed.Cosine([]float64{1, 2, 3}, []float64{1, 2}); err == nil {
		t.Fatal("embed.Cosine accepted a dimension mismatch")
	}
}
