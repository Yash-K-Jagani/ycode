package httpx

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retrying a provider request.
//
// There was no retry at all. The only thing that resembled one was switching
// to a *different* provider (agent.RunChain), which is a different mechanism
// solving a different problem: it does not help when a single provider is
// briefly rate-limited or its connection blipped, which is the common case.
//
// The constraint that shapes the design is that a streaming request writes to
// the caller's writer as it goes. So the rule is: retry freely while nothing
// has been received, and never once bytes have started arriving. A retry after
// the first chunk would re-send the same prompt and append a second, different
// answer to the same writer - visible corruption, and the worst possible thing
// to do in a UI that is appending to a transcript. DoStream therefore takes a
// body factory rather than a body, so a retry re-reads the request rather than
// trying to rewind a consumed reader.

// StatusError is a final failure carrying the status and as much of the body as
// was worth keeping.
//
// The body matters more than it looks. A 429 from a real provider explains
// itself - "rate limit reached, retry in 8s" - and a bare "http 429" tells the
// user nothing they can act on. It used to reach them, because the caller read
// the body off the response itself; retrying means draining that body to
// release the connection, so the last one is captured here instead.
type StatusError struct {
	Code   int
	Status string
	Body   string
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return "http " + e.Status + ": " + e.Body
	}
	return "http " + e.Status
}

// retryableStatus reports whether a status is worth trying again on the same
// provider.
//
// 429 is deliberately excluded unless the server said when to come back. A rate
// limit is the one failure the provider chain already handles better than a
// retry can: RunChain moves to a different provider, whereas retrying three
// times with backoff just delays that fallback and spends more of the user's
// remaining quota to get the same answer. When Retry-After is present the
// server has done the thinking already, so it is honoured - that is the one
// case where waiting is clearly the right move.
func retryableStatus(code int, hasRetryAfter bool) bool {
	if code == http.StatusTooManyRequests {
		return hasRetryAfter
	}
	return code == http.StatusRequestTimeout || (code >= 500 && code <= 599)
}

// IsRetryableStatus reports whether a status is worth trying again, exposed for
// callers that manage their own request loop. It assumes the server gave a
// Retry-After, which is the permissive reading.
func IsRetryableStatus(code int) bool { return retryableStatus(code, true) }

// Retry-After is either a number of seconds or an HTTP date. Both appear in the
// wild, and providers that rate-limit do use both, so both are honoured. The
// date form is the one usually ignored and it is the one that carries the real
// instruction, because a server that sends a date has usually already picked a
// reset time.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d, true
		}
		// A date already in the past means "retry now".
		return 0, true
	}
	return 0, false
}

// RetryConfig bounds a retry sequence.
type RetryConfig struct {
	// Attempts is the total number of tries, including the first. 1 disables
	// retrying.
	Attempts int
	// Base is the first backoff delay. Each subsequent delay doubles, and the
	// result is jittered so that a fleet of clients that all hit a rate limit
	// together do not all come back together and trip it again.
	Base time.Duration
	// Max caps any single delay, including one from Retry-After.
	//
	// That cap is the whole reason a Retry-After is safe to honour at all. A
	// provider asking for an hour is not going to recover inside one turn, and
	// blocking the user for that long is worse than falling back to another
	// provider - so the request is given up on rather than waited out.
	Max time.Duration
}

// DefaultRetry is what the providers use: three tries, 400ms apart to start.
//
// Three is deliberate. The provider chain already offers a different provider,
// so spending a long time retrying one that is genuinely down delays the
// fallback that would have worked; three tries distinguishes a blip from an
// outage.
func DefaultRetry() RetryConfig {
	return RetryConfig{Attempts: 3, Base: 400 * time.Millisecond, Max: 5 * time.Second}
}

// doRetry runs fn, retrying per policy. fn returns a response or an error, and
// must have produced no output to any caller at the time of the error - which
// is why a streaming caller reads the body only after this returns.
//
// On success or a non-retryable status the response is returned with its body
// intact. On exhaustion it returns nil and a *StatusError carrying the last
// attempt's body, so the message the user sees is the provider's own.
func doRetry(ctx context.Context, cfg RetryConfig, fn func() (*http.Response, error)) (*http.Response, error) {
	if cfg.Attempts < 1 {
		cfg.Attempts = 1
	}
	if cfg.Base <= 0 {
		cfg.Base = 400 * time.Millisecond
	}
	if cfg.Max <= 0 {
		cfg.Max = 5 * time.Second
	}
	delay := cfg.Base
	var lastErr error
	var lastStatus *StatusError

	for attempt := 0; attempt < cfg.Attempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff with full jitter. The jitter is not
			// decoration: without it every ycode session that hit the same
			// rate limit retries in lockstep and trips it again.
			d := delay
			if d > cfg.Max {
				d = cfg.Max
			}
			jittered := time.Duration(rand.Int63n(int64(d) + 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(jittered):
			}
			if delay < cfg.Max {
				delay *= 2
			}
		}

		resp, err := fn()
		if err == nil {
			after, hasAfter := retryAfter(resp)
			if !retryableStatus(resp.StatusCode, hasAfter) {
				return resp, nil
			}
			// Drain a bounded amount so the connection can be reused rather
			// than discarded, and keep what the server said for the error.
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
			_ = resp.Body.Close()
			lastStatus = &StatusError{
				Code:   resp.StatusCode,
				Status: resp.Status,
				Body:   strings.TrimSpace(string(b)),
			}
			lastErr = lastStatus
			if hasAfter && after > delay {
				delay = after
			}
			continue
		}
		// A transport error is retryable: the connection was refused, reset,
		// or timed out before anything was received.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
		lastStatus = nil
	}
	if lastStatus != nil {
		return nil, lastStatus
	}
	if lastErr == nil {
		lastErr = errors.New("request failed")
	}
	return nil, lastErr
}

// DoWithRetry issues req, retrying per policy. req.Body must be nil or
// GetBody-backed, because a retry needs to re-send the body and an
// already-consumed reader cannot be rewound.
func DoWithRetry(ctx context.Context, client *http.Client, req *http.Request, cfg RetryConfig) (*http.Response, error) {
	return doRetry(ctx, cfg, func() (*http.Response, error) {
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			// Copy per attempt: the same request cannot be in flight twice.
			clone := req.Clone(ctx)
			clone.Body = body
			return client.Do(clone)
		}
		return client.Do(req)
	})
}
