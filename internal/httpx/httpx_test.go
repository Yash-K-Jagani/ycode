package httpx

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The whole point of the helper: a URL that fails to parse must be an error,
// never a nil request to dereference.
func TestNewRequestRejectsMalformedURL(t *testing.T) {
	bad := []string{
		"https://api.github.com/repos/o%zz/pulls",
		"http://localhost:11434/%zz",
		"://missing-scheme",
		"ht tp://space",
	}
	for _, u := range bad {
		req, err := NewRequest(context.Background(), http.MethodGet, u, nil)
		if err == nil {
			t.Fatalf("NewRequest(%q) succeeded; it must not", u)
		}
		if req != nil {
			t.Fatalf("NewRequest(%q) returned a non-nil request with an error", u)
		}
		if !strings.Contains(err.Error(), "invalid URL") {
			t.Fatalf("error for %q should name the problem, got %v", u, err)
		}
	}
}

func TestNewRequestAcceptsGoodURLs(t *testing.T) {
	for _, u := range []string{
		"https://api.github.com/repos/acme/api/pulls",
		"http://127.0.0.1:11434/api/chat",
		"https://example.com/a%20b?q=1#frag",
	} {
		req, err := NewRequest(context.Background(), http.MethodPost, u, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("NewRequest(%q): %v", u, err)
		}
		if req == nil || req.URL == nil {
			t.Fatalf("NewRequest(%q) returned a request with no URL", u)
		}
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
	}
}

// A nil context is the other input error stdlib checks for; a nil *http.Request
// here would be the same class of nil deref.
func TestNewRequestRejectsNilContext(t *testing.T) {
	//lint:ignore SA1012 deliberately passing nil to check the guard
	req, err := NewRequest(nil, http.MethodGet, "https://example.com", nil) //nolint:staticcheck
	if err == nil {
		t.Fatal("a nil context should surface as an error")
	}
	if req != nil {
		t.Fatal("a nil context returned a non-nil request")
	}
}
