package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- transport and client construction ---

// The defaults that made every turn pay a fresh handshake. MaxIdleConnsPerHost
// of 2 is the one that bites, and it is the number worth pinning.
func TestTransportIsTunedForReuse(t *testing.T) {
	tr := Transport()
	if tr.MaxIdleConnsPerHost < 8 {
		t.Errorf("MaxIdleConnsPerHost = %d; a provider talking to itself will evict the connection",
			tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout == 0 {
		t.Error("no idle timeout; connections leak")
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("HTTP/2 not attempted")
	}
	if tr.ResponseHeaderTimeout == 0 {
		t.Error("a server that accepts and never replies would hang forever")
	}
	if tr.TLSHandshakeTimeout == 0 {
		t.Error("no TLS handshake timeout")
	}
	if tr.DialContext == nil {
		t.Error("no dial timeout; an unreachable host hangs on the OS default")
	}
}

// The whole reason httpx exists: a streaming client with a blanket timeout kills
// a long generation at the wall, which is exactly the case a local model on
// modest hardware produces.
func TestStreamClientHasNoBlanketTimeout(t *testing.T) {
	c := StreamClient()
	if c.Timeout != 0 {
		t.Fatalf("StreamClient.Timeout = %v; a long generation would be killed mid-sentence", c.Timeout)
	}
}

func TestClientDefaultsTimeout(t *testing.T) {
	if got := Client(0).Timeout; got != DefaultTimeout {
		t.Fatalf("Client(0).Timeout = %v, want %v", got, DefaultTimeout)
	}
	if got := Client(time.Second).Timeout; got != time.Second {
		t.Fatalf("Client(1s).Timeout = %v", got)
	}
}

func TestSharedTransportIsReused(t *testing.T) {
	// Two calls must return the same transport, or the per-construction cost
	// comes straight back. Compared through variables rather than as
	// Shared() != Shared(), which reads as a tautology even though two calls are
	// made - and which a linter is right to flag.
	first, second := Shared(), Shared()
	if first == nil {
		t.Fatal("Shared returned no transport")
	}
	if first != second {
		t.Fatal("Shared returned different transports")
	}
	if Client(time.Second).Transport != StreamClient().Transport {
		t.Fatal("clients do not share a transport, so they cannot share a pool")
	}
}

// --- idle watchdog ---

func TestWatchIdleFiresOnAStalledStream(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	pr, pw := io.Pipe()
	// The cancel is what unblocks the read; the derived context is what a real
	// caller would hold, and cancelling it is the watchdog's whole mechanism.
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := WatchIdle(cancel, pr, 150*time.Millisecond)
	defer w.Close()

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = pw.Write([]byte("first"))
		// Then nothing, forever.
	}()

	buf := make([]byte, 64)
	done := make(chan error, 1)
	go func() {
		_, err := r.Read(buf)
		done <- err
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled stream was not interrupted")
	}
	// Give the watchdog a moment to record that it was the one that fired.
	deadline := time.Now().Add(3 * time.Second)
	for !w.Fired() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !w.Fired() {
		t.Fatal("the watchdog did not record that it fired")
	}
}

// A stream that keeps sending is left alone. This is the case a naive
// implementation breaks: a model that pauses for a few hundred milliseconds
// between tokens is normal, and cutting it off would be worse than useless.
func TestWatchIdleLeavesAHealthyStreamAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	pr, pw := io.Pipe()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, w := WatchIdle(cancel, pr, 2*time.Second)
	defer func() { _ = pw.Close() }()
	defer w.Close()

	go func() {
		for i := 0; i < 20; i++ {
			_, _ = pw.Write([]byte("chunk"))
			// Well under the idle bound, but slower than one tick.
			time.Sleep(30 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := strings.Repeat("chunk", 20); string(got) != want {
		t.Fatalf("got %d bytes, want %d", len(got), len(want))
	}
	if w.Fired() {
		t.Fatal("a healthy stream was cut off")
	}
}

func TestWatchIdleDisabledByZero(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	called := false
	r, w := WatchIdle(func() { called = true }, pr, 0)
	if r != io.Reader(pr) {
		t.Fatal("a zero idle bound should install no wrapper")
	}
	// The handle is still usable, so callers never have to nil-check one.
	w.Close()
	if w.Fired() {
		t.Fatal("a disabled watchdog must never fire")
	}
	if called {
		t.Fatal("a disabled watchdog must not cancel anything")
	}
}

func TestStreamWatchCloseIsIdempotent(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	_, w := WatchIdle(func() {}, pr, time.Minute)
	w.Close()
	w.Close()
}

// The body must be released, which is what returns the connection to the pool
// rather than discarding it.
func TestWithStreamIdleReleasesTheBody(t *testing.T) {
	pr, pw := io.Pipe()
	_ = pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r, release, w := WithStreamIdle(ctx, cancel, pr, time.Minute)
	_, _ = io.ReadAll(r)
	release()
	if w.Fired() {
		t.Fatal("a completed stream should not report a stall")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("release should cancel the derived context")
	}
}

// --- retry policy ---

func TestRetryableStatus(t *testing.T) {
	cases := []struct {
		code int
		// A 429 is only retried when the server told us when to come back.
		retryable      bool
		withRetryAfter bool
	}{
		{500, true, true}, {502, true, true}, {503, true, true}, {504, true, true},
		{408, true, true},
		{429, false, false}, {429, true, true},
		{400, false, true}, {401, false, true}, {403, false, true}, {404, false, true},
		{200, false, true}, {201, false, true}, {204, false, true},
	}
	for _, c := range cases {
		if got := retryableStatus(c.code, c.withRetryAfter); got != c.retryable {
			t.Errorf("retryableStatus(%d, after=%v) = %v, want %v",
				c.code, c.withRetryAfter, got, c.retryable)
		}
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "7")
	d, ok := retryAfter(resp)
	if !ok || d != 7*time.Second {
		t.Fatalf("got %v %v", d, ok)
	}
}

func TestRetryAfterDate(t *testing.T) {
	// The date form is the one usually ignored and it is the one that carries
	// the real instruction.
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
	d, ok := retryAfter(resp)
	if !ok {
		t.Fatal("a date Retry-After was not honoured")
	}
	if d <= 0 || d > 35*time.Second {
		t.Fatalf("got %v, want roughly 30s", d)
	}
}

func TestRetryAfterPastDateMeansNow(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	d, ok := retryAfter(resp)
	if !ok || d != 0 {
		t.Fatalf("got %v %v; a past date means retry immediately", d, ok)
	}
}

func TestRetryAfterAbsentOrNonsense(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	if _, ok := retryAfter(resp); ok {
		t.Error("no header should not claim a delay")
	}
	resp.Header.Set("Retry-After", "soonish")
	if _, ok := retryAfter(resp); ok {
		t.Error("an unparseable value should not claim a delay")
	}
	resp.Header.Set("Retry-After", "-5")
	if _, ok := retryAfter(resp); ok {
		t.Error("a negative delay should not be honoured")
	}
}

// The policy: a 429 without Retry-After is handed straight back so the caller
// can report it and the provider chain can move on, rather than being retried
// three times with backoff. Retrying here would delay the fallback that
// actually helps, and spend more of the user's quota to get the same answer.
//
// Note it arrives as a response rather than an error: a non-retryable status is
// not a transport failure, and the caller is the one that knows how to phrase
// it. DoWithRetry only errors when the request itself could not be completed.
func TestRateLimitWithoutRetryAfterIsNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"slow down"}`)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), "POST", srv.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := DoWithRetry(context.Background(), Client(time.Second), req, DefaultRetry())
	if err != nil {
		t.Fatalf("a non-retryable status is not a transport error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "slow down") {
		t.Fatalf("the provider's explanation was lost: %q", b)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("made %d attempts; a 429 without Retry-After should not be retried", n)
	}
}

// With a Retry-After the server has said when to come back, so waiting is
// right and the request is retried.
func TestRateLimitWithRetryAfterIsRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "recovered")
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), Client(2*time.Second), req,
		RetryConfig{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "recovered" {
		t.Fatalf("got %q", b)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("made %d attempts, want 2", n)
	}
}

func TestServerErrorIsRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "recovered")
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	resp, err := DoWithRetry(context.Background(), Client(2*time.Second), req, RetryConfig{Attempts: 3, Base: time.Millisecond, Max: 2 * time.Millisecond})
	if err != nil {
		t.Fatalf("a transient 502 should recover: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "recovered" {
		t.Fatalf("got %q", b)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("made %d attempts, want 3", n)
	}
}

func TestExhaustedRetriesReportTheLastStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "still down")
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL, nil)
	_, err := DoWithRetry(context.Background(), Client(time.Second), req, RetryConfig{Attempts: 2, Base: time.Millisecond, Max: time.Millisecond})
	if err == nil {
		t.Fatal("expected an error")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want a *StatusError, got %T: %v", err, err)
	}
	if se.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", se.Code)
	}
	if !strings.Contains(se.Body, "still down") {
		t.Fatalf("the last response body was lost: %q", se.Body)
	}
}

// The body has to be re-sendable, or a retry silently posts nothing and the
// provider answers a different question.
func TestRetryResendsTheBody(t *testing.T) {
	var bodies []string
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		<-mu
		bodies = append(bodies, string(b))
		mu <- struct{}{}
		if len(bodies) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// strings.NewReader gives http.NewRequest a GetBody, which is what makes
	// re-sending possible.
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, strings.NewReader(`{"model":"m"}`))
	resp, err := DoWithRetry(context.Background(), Client(2*time.Second), req, RetryConfig{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	<-mu
	defer func() { mu <- struct{}{} }()
	if len(bodies) != 2 {
		t.Fatalf("got %d attempts", len(bodies))
	}
	for i, b := range bodies {
		if b != `{"model":"m"}` {
			t.Fatalf("attempt %d sent %q; the body must be identical on every try", i, b)
		}
	}
}

func TestRetryStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	_, err := DoWithRetry(ctx, Client(time.Second), req, RetryConfig{Attempts: 5, Base: 50 * time.Millisecond, Max: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled; a cancelled turn must stop retrying", err)
	}
}

// A cancelled turn is the most common reason a request is abandoned, and it must
// not be mistaken for a provider problem worth retrying.
func TestTransportErrorOnCancelledContextIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	// The error is discarded deliberately: this test is about how many attempts
	// were made, not about what came back.
	_, _ = doRetry(ctx, RetryConfig{Attempts: 3, Base: time.Millisecond, Max: time.Millisecond},
		func() (*http.Response, error) {
			calls++
			return nil, context.Canceled
		})
	if calls != 1 {
		t.Fatalf("made %d attempts on a cancelled context", calls)
	}
}

func TestStatusErrorFormats(t *testing.T) {
	withBody := &StatusError{Code: 429, Status: "429 Too Many Requests", Body: "quota exceeded"}
	if got := withBody.Error(); !strings.Contains(got, "429") || !strings.Contains(got, "quota exceeded") {
		t.Fatalf("got %q", got)
	}
	noBody := &StatusError{Code: 500, Status: "500 Internal Server Error"}
	if got := noBody.Error(); strings.Contains(got, ": ") && strings.Count(got, ":") > 1 {
		t.Fatalf("an empty body should not add a dangling separator: %q", got)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	if !IsRetryableStatus(503) {
		t.Error("503 should be retryable")
	}
	if IsRetryableStatus(400) {
		t.Error("400 should not be retryable")
	}
}

func TestDefaultRetryIsBounded(t *testing.T) {
	// A long retry sequence delays the provider-chain fallback that would have
	// worked, so the default has to stay short.
	c := DefaultRetry()
	if c.Attempts > 3 {
		t.Fatalf("Attempts = %d; the chain is the better answer to a down provider", c.Attempts)
	}
	if c.Max > 30*time.Second {
		t.Fatalf("Max = %v; too long to hold a turn open", c.Max)
	}
}

// The provider-side guarantee: a stream is never retried once it has produced
// output, because a second attempt would append a second answer to the same
// writer. This is checked through the real client rather than the helper.
func TestStreamingRequestIsNotRetriedAfterOutputBegins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Announce good data, then fail the connection mid-stream. A retry at
		// this point would duplicate the first chunk in the caller's writer.
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hijack and close, which surfaces as a read error on the client.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}
	}))
	defer srv.Close()

	c := StreamClient()
	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL, strings.NewReader("{}"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The response was a 200, so no retry path is entered at all; the failure
	// surfaces later, while reading, which is exactly the point.
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func ExampleStatusError() {
	e := &StatusError{Code: http.StatusTooManyRequests, Status: "429 Too Many Requests", Body: "quota exceeded"}
	fmt.Println(e)
	// Output: http 429 Too Many Requests: quota exceeded
}
