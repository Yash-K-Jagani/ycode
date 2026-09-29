package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A response the client cannot read used to be reported as a successful empty
// answer: every unparseable line was skipped, and a stream in which none parsed
// produced "" with a nil error. The user saw "the model returned nothing" for
// what was really a protocol mismatch, and the turn silently produced no work.
func TestStreamFailsWhenNoEventParses(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			"data: not json at all",
			"data: {oops",
			"data: [DONE]",
		))
	})
	var sink strings.Builder
	got, err := c.Stream(context.Background(), "m", nil, &sink)
	if err == nil {
		t.Fatalf("expected an error, got a silent empty answer (delta %q)", got.Delta)
	}
	if !strings.Contains(err.Error(), "none parsed") {
		t.Fatalf("error should say nothing parsed, got %v", err)
	}
	// The error must name what arrived, or the user still cannot tell a
	// protocol mismatch from a model that said nothing.
	if !strings.Contains(err.Error(), "not json at all") {
		t.Fatalf("error should quote the offending event, got %v", err)
	}
}

func TestStreamSucceedsWithEmptyAnswer(t *testing.T) {
	// A model genuinely returning nothing is not an error. Only "we read the
	// stream and understood none of it" is.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":""}}]}`,
			"data: [DONE]",
		))
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("an empty but well-formed answer must not error: %v", err)
	}
	if got.Delta != "" || !got.Done {
		t.Fatalf("got %+v", got)
	}
}

func TestStreamSucceedsWhenThereAreNoEventsAtAll(t *testing.T) {
	// Some gateways answer a stream request with a body and no data lines.
	// Zero events is not evidence of a parse failure, so it must not error.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "")
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.Delta != "" {
		t.Fatalf("delta = %q", got.Delta)
	}
}

// One bad line among good ones is tolerable: a single truncated chunk should
// not lose the whole answer.
func TestStreamToleratesSomeUnparseableLines(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":"good"}}]}`,
			`data: {"choices":[{"delta":{`,
			`data: {"choices":[{"delta":{"content":" tail"}}]}`,
			"data: [DONE]",
		))
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("one bad line must not fail the whole stream: %v", err)
	}
	if got.Delta != "good tail" {
		t.Fatalf("delta = %q", got.Delta)
	}
}

func TestStreamReadsReportedUsage(t *testing.T) {
	// Token counts drive the cost figure. Before this they were always zero and
	// the harness divided text length by four, which looks like a measurement
	// on screen but is a guess.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"choices":[],"usage":{"prompt_tokens":1234,"completion_tokens":56}}`,
			"data: [DONE]",
		))
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.PromptTok != 1234 || got.ComplTok != 56 {
		t.Fatalf("usage = %d/%d, want 1234/56", got.PromptTok, got.ComplTok)
	}
	if !got.UsageReported() {
		t.Fatal("UsageReported should be true when the server sent counts")
	}
}

func TestStreamWithoutUsageIsNotReportedAsMeasured(t *testing.T) {
	// The distinction that matters: a server that sends no usage must not look
	// like one that sent zero.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(
			`data: {"choices":[{"delta":{"content":"hi"}}]}`,
			"data: [DONE]",
		))
	})
	got, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got.UsageReported() {
		t.Fatalf("no usage was sent, so it must not be reported as measured: %+v", got)
	}
}

func TestStreamAsksForUsage(t *testing.T) {
	// Without this the server has no reason to send counts at all.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		opts, ok := body["stream_options"].(map[string]any)
		if !ok {
			t.Errorf("stream_options missing from request: %v", body)
		} else if opts["include_usage"] != true {
			t.Errorf("include_usage not requested: %v", opts)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(`data: {"choices":[{"delta":{"content":"ok"}}]}`, "data: [DONE]"))
	})
	if _, err := c.Stream(context.Background(), "m", nil, io.Discard); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// A 401 must not decode as an empty catalogue, and a 200 with a broken body
// must not decode as an empty answer.
func TestStreamNonJSONBodyIsAnError(t *testing.T) {
	// A proxy, gateway or SSO page answers 200 with HTML. There are no data:
	// lines at all, so a parser that only counts data: lines sees nothing to
	// complain about and reports a successful empty answer.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>gateway login page</body></html>")
	})
	_, err := c.Stream(context.Background(), "m", nil, io.Discard)
	if err == nil {
		t.Fatal("an HTML login page must not be reported as an empty model answer")
	}
	if !strings.Contains(err.Error(), "not an event stream") {
		t.Fatalf("error should explain that no stream arrived, got %v", err)
	}
	if !strings.Contains(err.Error(), "login page") {
		t.Fatalf("error should name the likely cause, got %v", err)
	}
}
