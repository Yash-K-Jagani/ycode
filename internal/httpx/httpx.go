// Package httpx holds the small HTTP helpers that the providers, tools and
// outbound webhooks all need.
package httpx

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

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
