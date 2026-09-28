package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	loopback := []string{"", "127.0.0.1:8471", "localhost:8471", "[::1]:8471", ":8471", "127.0.0.5:1"}
	for _, a := range loopback {
		if !IsLoopback(a) {
			t.Fatalf("%q should be loopback", a)
		}
	}
	exposed := []string{"0.0.0.0:8471", "192.168.1.5:8471", "example.com:80", "[::]:8471"}
	for _, a := range exposed {
		if IsLoopback(a) {
			t.Fatalf("%q is not loopback", a)
		}
	}
}

func TestTokenIsStableAndPersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	tokenFilePath = func() string { return filepath.Join(home, ".ycode", "api_token") }
	first := Token()
	if first == "" {
		t.Fatal("no token was minted")
	}
	if second := Token(); second != first {
		t.Fatalf("Token() is not stable: %q then %q", first, second)
	}
	p := filepath.Join(home, ".ycode", "api_token")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("the token was not persisted: %v", err)
	}
	data, _ := os.ReadFile(p)
	if strings.TrimSpace(string(data)) != first {
		t.Fatal("the persisted token does not match")
	}
	// An env override wins, so a caller can supply its own.
	t.Setenv(tokenEnv, "from-env")
	if got := Token(); got != "from-env" {
		t.Fatalf("YCODE_API_TOKEN was ignored, got %q", got)
	}
}

// A loopback bind stays usable without a token, because that is the common
// case; anything else must present one.
func TestAuthRequired(t *testing.T) {
	t.Setenv(anonEnv, "")
	if authRequired("127.0.0.1:8471") {
		t.Fatal("loopback should not require a token")
	}
	if !authRequired("0.0.0.0:8471") {
		t.Fatal("a non-loopback bind must require a token")
	}
	// The migration escape hatch.
	t.Setenv(anonEnv, "1")
	if authRequired("0.0.0.0:8471") {
		t.Fatal("the migration env var should disable auth")
	}
}

// stub is a stand-in for the real mux: the auth wrapper is what is under
// test, and hitting /v1/status for real would open the SQLite singleton, which
// stays locked on Windows and breaks temp-dir cleanup.
func stub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})
}

func TestWithAuthRefusesWithoutToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv(anonEnv, "")
	t.Setenv(tokenEnv, "test-token-abc")

	h := withAuth(stub(), "0.0.0.0:8471")

	// healthz stays open, so a liveness probe still works.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz should be open, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Bearer") {
		t.Fatalf("the 401 should say how to authenticate: %s", rec.Body.String())
	}

	for _, hdr := range []map[string]string{
		{"Authorization": "Bearer test-token-abc"},
		{"X-Ycode-Token": "test-token-abc"},
	} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("%v should authenticate, got 401", hdr)
		}
		if rec.Body.String() != "reached" {
			t.Fatalf("the request did not reach the handler: %q", rec.Body.String())
		}
	}

	// A wrong token is refused.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token must be refused, got %d", rec.Code)
	}
	// A prefix of the token is not the token.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer test-token-ab")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("a token prefix must be refused")
	}

	// A query token works too, for clients that cannot set headers.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status?token=test-token-abc", nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("a query token should authenticate")
	}
}

func TestLoopbackNeedsNoAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	t.Setenv(anonEnv, "")
	t.Setenv(tokenEnv, "test-token-abc")
	rec := httptest.NewRecorder()
	withAuth(stub(), "127.0.0.1:8471").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("loopback should not demand a token")
	}
	if rec.Body.String() != "reached" {
		t.Fatalf("the request did not reach the handler: %q", rec.Body.String())
	}
}
