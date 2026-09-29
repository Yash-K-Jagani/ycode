// Package httpx holds the small HTTP helpers that the providers, tools and
// outbound webhooks all need.
package httpx

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// The shared HTTP client.
//
// Every provider built its own http.Client with a blanket timeout and no
// Transport, which meant the defaults of net/http applied:
//
//   - MaxIdleConnsPerHost is 2. A turn streams one request and then sits idle
//     while the user reads the answer, so by the time the next turn starts the
//     connection is usually already closed, and every turn pays a fresh TCP
//     connect and a fresh TLS handshake to a host it had just been talking to.
//     On a metered connection that is also latency the user pays for twice.
//   - There was no connect timeout, so an unreachable host could hang on the
//     TCP handshake for the OS default, which is minutes on some platforms.
//
// The blanket timeout was the other problem, and it is the reason this file
// exists rather than just a tuned Transport. A 10-minute http.Client.Timeout
// covers connect, first byte and the entire body, so a slow local model
// generating a long answer was killed mid-sentence at exactly ten minutes, with
// no way to tell a busy model from a dead one. Splitting it into per-phase
// bounds - connect, TLS, response header, and the gap between stream chunks -
// means each failure gets a timeout proportionate to it, and a healthy stream
// can run as long as it likes.

// Phase bounds. None of them is the length of the work; they are all ceilings
// on being stuck.
const (
	// DialTimeout bounds establishing the TCP connection.
	DialTimeout = 10 * time.Second
	// TLSHandshakeTimeout bounds the TLS handshake.
	TLSHandshakeTimeout = 10 * time.Second
	// ResponseHeaderTimeout bounds the wait between finishing the request and
	// seeing the first response header. A model that accepts a connection and
	// then thinks for a minute before replying is stuck, not busy.
	ResponseHeaderTimeout = 90 * time.Second
	// StreamIdleTimeout bounds the gap between two chunks of a response body.
	//
	// This is the one that matters most for a streaming harness, and it
	// replaces the blanket timeout. A generation is idle between tokens
	// constantly - a 3B model on modest hardware pauses for hundreds of
	// milliseconds all the time - but it is never idle for a minute and a half
	// while holding a connection open. Ninety seconds of silence means the
	// provider is gone, and the connection is closed so the user finds out now
	// rather than in ten minutes.
	StreamIdleTimeout = 90 * time.Second
	// DefaultTimeout is the ceiling for short request/response calls: a model
	// list, a repo clone header, a release check.
	DefaultTimeout = 60 * time.Second
)

// Transport is the tuned transport shared by every outbound client.
//
// It is shared rather than per-client on purpose. A Transport owns the
// connection pool, so a client that carries its own Transport cannot reuse a
// connection from anywhere else - which is what made the per-turn construction
// of http.Client in the providers expensive in the first place.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 128
	// The important one. Two is the default, and two is below the number of
	// things that talk to a provider at once - a model list while a turn
	// streams, a fallback after a failure, an embed call from the RAG
	// retriever during the same turn.
	t.MaxIdleConnsPerHost = 32
	t.IdleConnTimeout = 90 * time.Second
	t.ForceAttemptHTTP2 = true
	t.DialContext = (&net.Dialer{
		Timeout:   DialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	t.TLSHandshakeTimeout = TLSHandshakeTimeout
	t.ResponseHeaderTimeout = ResponseHeaderTimeout
	t.ExpectContinueTimeout = time.Second
	return t
}

var sharedTransport = Transport()

// Shared returns the process-wide transport, for the common case where a
// caller just wants a client with no per-call timeout.
func Shared() *http.Transport { return sharedTransport }

// Client returns a client with an overall deadline, for request/response calls
// that are short by nature: listing models, checking a release, fetching a URL.
func Client(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Transport: sharedTransport, Timeout: timeout}
}

// StreamClient returns a client for a long-lived streaming response.
//
// It deliberately has no Timeout. A local model on modest hardware can
// legitimately take ten minutes to produce a long answer, and the old blanket
// timeout killed exactly that case - mid-sentence, with the partial answer
// discarded and no indication why. The work is bounded by its phases instead:
// DialTimeout, TLSHandshakeTimeout and ResponseHeaderTimeout from the
// transport, and StreamIdleTimeout applied to the body by the caller.
//
// The context is still the real deadline. Anything with a ctx deadline is
// bounded by it.
func StreamClient() *http.Client {
	return &http.Client{Transport: sharedTransport}
}

// BoundedByContext returns a client for calls that already carry their own
// context deadline.
//
// This exists because most of the codebase was calling http.DefaultClient,
// which has no timeout and no tuned transport. The context deadline was doing
// the work in nearly every case - browser, api, github and the webhooks all
// wrap their request - so the missing client timeout was not a hang risk, but
// the missing transport was: those calls never reused a connection, and
// MaxIdleConnsPerHost defaulted to 2.
//
// Using this rather than StreamClient keeps the distinction honest. A client
// with no timeout at all would be a hazard if a caller forgot the context
// deadline, so the fix for that case is a deadline, not a bigger number here.
func BoundedByContext() *http.Client {
	return &http.Client{Transport: sharedTransport}
}

// NewRequest wraps http.NewRequestWithContext so a malformed URL produces an
// error instead of a nil *http.Request.
//
// http.NewRequestWithContext returns (nil, err) when the URL fails to parse,
// and several call sites used to discard err and go straight to
// req.Header.Set(...). That is a nil dereference, and these URLs are often
// built from model-supplied input (a repo name, a user-supplied path), so a
// typo or an injected instruction could kill the TUI mid-turn and lose the
// unsaved transcript.
func NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", url, err)
	}
	if req == nil {
		return nil, fmt.Errorf("invalid URL %q", url)
	}
	return req, nil
}
