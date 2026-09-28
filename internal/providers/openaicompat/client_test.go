package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// This client is what gemini, openrouter and groq all are, and it runs on every
// turn of every cloud-backed session. It had no tests at all, so the SSE
// parsing and the error reporting were both unverified.

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("testprov", srv.URL, "sk-test")
	return c, srv
}

func sse(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestStreamAccumulatesDeltasAndWritesThemAsTheyArrive(t *testing.T) {
	want := "Hello, world"
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A real stream: a comment line, blank lines, an empty keepalive, then
		// content, then the terminator.
		_, _ = io.WriteString(w, sse(
			": keepalive",
			"",
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{}}]}`,
			`data: {"choices":[{"delta":{"content":", world"}}]}`,
			"data: [DONE]",
			`data: {"choices":[{"delta":{"content":"never read"}}]}`,
		))
	})

	var streamed strings.Builder
	got, err := c.Stream(context.Background(), "m", []apitypes.Message{{Role: "user", Content: "hi"}}, &streamed)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if want := "Hello, world"; got.Delta != want {
		t.Fatalf("Delta = %q, want %q", got.Delta, want)
	}
	if !got.Done {
		t.Fatal("Done should be set")
	}
	// The UI renders from the writer, so it must receive the same text even
	// though the stream was stopped at [DONE].
	if streamed.String() != want {
		t.Fatalf("streamed %q, want %q", streamed.String(), want)
	}
}

// A provider that interleaves a keepalive or splits oddly should not fail the
// turn; a malformed line is skipped, not fatal.
func TestStreamSkipsUnparseableLines(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":"a"}}]}`,
			"data: {not json",
			`data: {"choices":[]}`,
			`data: {"choices":[{"delta":{"content":"b"}}]}`,
			"data: [DONE]",
		))
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("a malformed line should not fail the turn: %v", err)
	}
	if got.Delta != "ab" {
		t.Fatalf("Delta = %q, want %q", got.Delta, "ab")
	}
}

func TestStreamReportsProviderStatusAndBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limit exceeded"}}`)
	})
	_, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err == nil {
		t.Fatal("a 429 should be an error, not an empty answer")
	}
	// The message has to identify the provider, the status and the reason, or
	// the user cannot act on it and the fallback chain cannot explain itself.
	for _, want := range []string{"testprov", "429", "rate limit exceeded"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// The regression: a rejected key used to decode as an empty catalogue, so the
// user was told they had no models instead of that their key was refused.
func TestListModelsReportsAnErrorStatus(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	})
	models, err := c.ListModels(context.Background())
	if err == nil {
		t.Fatalf("a 401 should be an error; got %d models", len(models))
	}
	for _, want := range []string{"testprov", "401", "invalid api key"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestListModelsParsesTheCatalogue(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
	})
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models", len(models))
	}
	for _, m := range models {
		// Provider must be attributed, or cost and latency stats land on the
		// wrong bucket.
		if m.Provider != "testprov" {
			t.Fatalf("model %q attributed to %q", m.ID, m.Provider)
		}
	}
}

func TestExtraHeadersAreSent(t *testing.T) {
	// openrouter needs HTTP-Referer and X-Title; without them it rejects
	// requests, and nothing would say why.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Title"); got != "ycode" {
			t.Errorf("X-Title = %q", got)
		}
		if got := r.Header.Get("HTTP-Referer"); got == "" {
			t.Error("HTTP-Referer missing")
		}
		if r.URL.Path == "/models" {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		_, _ = io.WriteString(w, "data: [DONE]\n")
	})
	c.ExtraHeaders = map[string]string{"X-Title": "ycode", "HTTP-Referer": "https://example.com"}
	if _, err := c.Stream(context.Background(), "m", nil, io.Discard); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
}

func TestStreamSendsTheExpectedRequest(t *testing.T) {
	var body map[string]any
	var path, ct string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		ct = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = io.WriteString(w, "data: [DONE]\n")
	})
	msgs := []apitypes.Message{{Role: "user", Content: "hello"}}
	if _, err := c.Stream(context.Background(), "qwen", msgs, io.Discard); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %q", path)
	}
	if ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if body["model"] != "qwen" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %v, want true", body["stream"])
	}
	sent, ok := body["messages"].([]any)
	if !ok || len(sent) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
}

// A missing key must fail before any request goes out, and say which provider
// and which variable to set.
func TestMissingKeyIsRejectedLocally(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	c := New("groq", srv.URL, "")

	if _, err := c.Stream(context.Background(), "m", nil, io.Discard); err == nil {
		t.Fatal("Stream should refuse without a key")
	} else if !strings.Contains(err.Error(), "groq") {
		t.Fatalf("error %q should name the provider", err)
	}
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Fatal("ListModels should refuse without a key")
	} else if !strings.Contains(err.Error(), "groq") {
		t.Fatalf("error %q should name the provider", err)
	}
	if called {
		t.Fatal("a request was sent despite the missing key")
	}
}

func TestBaseURLTrailingSlashIsTrimmed(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c := New("x", srv.URL+"/", "k")
	if _, err := c.ListModels(context.Background()); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// A doubled slash reaches some providers as a different route.
	if path != "/models" {
		t.Fatalf("path = %q, want /models", path)
	}
}

func TestCompleteReturnsTheText(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":"one "}}]}`,
			`data: {"choices":[{"delta":{"content":"two"}}]}`,
			"data: [DONE]",
		))
	})
	got, err := c.Complete(context.Background(), "m", nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "one two" {
		t.Fatalf("Complete = %q", got)
	}
}

// A cancelled context must not be reported as a completed turn.
func TestContextCancellationIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := New("x", srv.URL, "k")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Stream(ctx, "m", nil, io.Discard); err == nil {
		t.Fatal("a cancelled context should error")
	}
}
