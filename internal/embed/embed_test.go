package embed

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCosine(t *testing.T) {
	unit := func(v ...float64) []float64 { return v }
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", unit(1, 2, 3), unit(1, 2, 3), 1},
		{"orthogonal", unit(1, 0), unit(0, 1), 0},
		{"opposite", unit(1, 0), unit(-1, 0), -1},
		{"scaled", unit(1, 2, 3), unit(2, 4, 6), 1},
		// A zero vector has no direction, so any score would be a fiction.
		{"zero vector", unit(0, 0), unit(1, 2), 0},
		{"empty", nil, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Cosine(tc.a, tc.b)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("Cosine = %v, want %v", got, tc.want)
			}
		})
	}
}

// Cosine is symmetric; a retrieval index that ranked inconsistently with the
// query path would quietly return worse results for half of all searches.
func TestCosineIsSymmetric(t *testing.T) {
	a := []float64{0.3, -0.7, 0.2, 0.9}
	b := []float64{0.5, 0.1, -0.4, 0.6}
	if x, y := Cosine(a, b), Cosine(b, a); math.Abs(x-y) > 1e-12 {
		t.Fatalf("Cosine(a,b) = %v but Cosine(b,a) = %v", x, y)
	}
}

func TestEmbedParsesTheResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("body: %v", err)
		}
		if body["model"] != "nomic-embed-text" {
			t.Errorf("model = %v", body["model"])
		}
		in, _ := body["input"].([]any)
		if len(in) != 2 {
			t.Errorf("input = %v", body["input"])
		}
		_, _ = io.WriteString(w, `{"embeddings":[[1,0],[0,1]]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	if c.Model != "nomic-embed-text" {
		t.Fatalf("default model = %q", c.Model)
	}
	got, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(got))
	}
	// Order matters: the caller zips these back to its inputs.
	if got[0][0] != 1 || got[1][1] != 1 {
		t.Fatalf("embeddings = %v", got)
	}
}

// A missing embedding model is the common local setup failure, so the error has
// to name the model and the fix.
func TestEmbedErrorNamesTheModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model not found"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "nomic-embed-text").Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("a 404 should be an error")
	}
	for _, want := range []string{"404", "nomic-embed-text", "ollama pull"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestUnreachableDaemonSaysHowToStartIt(t *testing.T) {
	_, err := New("http://127.0.0.1:1", "nomic-embed-text").Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("an unreachable daemon should error")
	}
	if !strings.Contains(err.Error(), "nomic-embed-text") {
		t.Fatalf("error %q does not name the model to pull", err)
	}
}

func TestDefaultsAndTrailingSlash(t *testing.T) {
	c := New("", "")
	if c.Host != "http://localhost:11434" {
		t.Fatalf("Host = %q", c.Host)
	}
	if got := New("http://example.com:11434/", "").Host; got != "http://example.com:11434" {
		t.Fatalf("trailing slash not trimmed: %q", got)
	}
}
