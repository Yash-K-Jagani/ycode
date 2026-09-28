package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ollama is the default provider and the only one permitted under
// zero-data-leak, so a silent misparse here is worse than anywhere else: the
// user has asked for privacy and gets a transcript built from a misread
// stream instead of an error.

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	return c, srv
}

func TestDefaultHostIsLocal(t *testing.T) {
	c := New("")
	if c.Host != "http://localhost:11434" {
		t.Fatalf("Host = %q", c.Host)
	}
	if got := New("http://example.com:11434/").Host; got != "http://example.com:11434" {
		t.Fatalf("trailing slash not trimmed: %q", got)
	}
}

func TestStreamReadsTheNDJSONChatStream(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body["model"] != "qwen2.5-coder:3b" {
			t.Errorf("model = %v", body["model"])
		}
		if body["stream"] != true {
			t.Errorf("stream = %v, want true", body["stream"])
		}
		// Ollama streams one JSON object per line, not SSE.
		_, _ = io.WriteString(w, `{"message":{"content":"Hel"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"content":"lo"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"content":""},"done":true}`+"\n")
		// Anything after done is not part of the answer.
		_, _ = io.WriteString(w, `{"message":{"content":"ignored"},"done":false}`+"\n")
	})

	var streamed strings.Builder
	got, err := c.Stream(context.Background(), "qwen2.5-coder:3b", nil, &streamed)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got.Delta != "Hello" {
		t.Fatalf("Delta = %q, want %q", got.Delta, "Hello")
	}
	if !got.Done {
		t.Fatal("Done should be set")
	}
	if streamed.String() != "Hello" {
		t.Fatalf("streamed %q", streamed.String())
	}
}

// A truncated stream must be an error, not a short answer. Reporting half a
// tool call as a complete turn is how a write gets recorded as done.
func TestStreamReportsATruncatedStream(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Promise more than is delivered, then hang up mid-stream.
		w.Header().Set("Content-Length", "9999")
		_, _ = io.WriteString(w, `{"message":{"content":"half"},"done":false}`+"\n")
	})
	_, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err == nil {
		t.Fatal("a truncated stream should be an error")
	}
}

func TestStreamReportsAnErrorStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"model 'nope' not found, try pulling it first"}`)
	})
	_, err := c.Stream(context.Background(), "nope", nil, io.Discard)
	if err == nil {
		t.Fatal("a 404 should be an error")
	}
	// The daemon's own message is the actionable part: it names the model and
	// the fix.
	for _, want := range []string{"404", "not found", "pulling"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestUnreachableDaemonSaysWhere(t *testing.T) {
	c := New("http://127.0.0.1:1")
	_, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatal("an unreachable daemon should error")
	}
	// The user needs the address to act on it.
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("error %q does not name the address", err)
	}
}

func TestListModels(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"qwen2.5-coder:3b","size":123},{"name":"llama3:8b","size":456}]}`)
	})
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models", len(models))
	}
	for _, m := range models {
		if m.Provider != "ollama" {
			t.Fatalf("model %q attributed to %q", m.ID, m.Provider)
		}
		// Status is what /models shows and what the picker filters on.
		if m.Status != "installed" {
			t.Fatalf("model %q status %q", m.ID, m.Status)
		}
	}
}

func TestNameAndComplete(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"done"},"done":true}`+"\n")
	})
	if c.Name() != "ollama" {
		t.Fatalf("Name = %q", c.Name())
	}
	got, err := c.Complete(context.Background(), "m", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "done" {
		t.Fatalf("Complete = %q", got)
	}
}
