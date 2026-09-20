package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEndpoints(t *testing.T) {
	s := New()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for _, p := range []string{"/healthz", "/v1/models", "/v1/status"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s -> %d", p, resp.StatusCode)
		}
	}
	// chat validation paths (no model call)
	resp, _ := http.Get(ts.URL + "/v1/chat")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
	resp, _ = http.Post(ts.URL+"/v1/chat", "application/json", strings.NewReader(`{}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	resp, _ = http.Post(ts.URL+"/v1/chat", "application/json", strings.NewReader(`{"prompt":"hi","mode":"nope"}`))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad mode, got %d", resp.StatusCode)
	}
}
