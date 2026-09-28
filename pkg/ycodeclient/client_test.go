package ycodeclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is a published package: someone imports it to drive a running ycode.
// The contract that matters most is that a call either returns the parsed body
// or an error that says what the server said - never a nil map with a nil
// error, which would look like an empty server.

func newServer(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

func TestModelsParsesTheBody(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		io.WriteString(w, `{"models":[{"id":"qwen2.5-coder:3b"}]}`)
	})
	got, err := c.Models()
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if got == nil {
		t.Fatal("Models returned a nil map with no error")
	}
	// `return v, c.do(...)` reads v at the same time as the call fills it, and
	// Go does not order a variable read against a function call. If this ever
	// returns nil, the evaluation order flipped.
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), "qwen2.5-coder:3b") {
		t.Fatalf("the body was not parsed into the returned map: %s", raw)
	}
}

func TestStatusParsesTheBody(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"provider":"ollama","model":"qwen2.5-coder:3b"}`)
	})
	got, err := c.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got["provider"] != "ollama" {
		t.Fatalf("Status = %v", got)
	}
}

func TestChatReturnsTheAnswerAndGoalStatus(t *testing.T) {
	var body map[string]string
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		io.WriteString(w, `{"answer":"added the endpoint","goal_status":"met"}`)
	})
	answer, status, err := c.Chat("add /healthz", "goal", "wizard", "/tmp")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if answer != "added the endpoint" {
		t.Fatalf("answer = %q", answer)
	}
	// A caller has to be able to tell a finished job from a model that merely
	// claimed one, which is the whole reason goal_status is on the wire.
	if status != "met" {
		t.Fatalf("goal_status = %q", status)
	}
	for k, want := range map[string]string{
		"prompt": "add /healthz", "mode": "goal", "agent": "wizard", "workdir": "/tmp",
	} {
		if body[k] != want {
			t.Fatalf("sent %s = %q, want %q", k, body[k], want)
		}
	}
}

func TestErrorStatusNamesThePathAndTheServerMessage(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"bearer token required for a non-loopback bind"}`)
	})
	_, _, err := c.Chat("hi", "build", "", "")
	if err == nil {
		t.Fatal("a 401 should be an error")
	}
	for _, want := range []string{"/v1/chat", "401", "bearer token required"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// The token is what makes a non-loopback bind usable at all, so it must be sent
// on every call.
func TestTokenIsSentAsABearerHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	NewWithToken(srv.URL, "secret-token").Health()
	if got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
	// And an explicit token wins over the environment.
	t.Setenv("YCODE_API_TOKEN", "from-env")
	NewWithToken(srv.URL, "secret-token").Health()
	if got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q, the explicit token should win", got)
	}
}

func TestTokenDefaultsToTheEnvironmentThenTheFile(t *testing.T) {
	t.Setenv("YCODE_API_TOKEN", "env-token")
	if got := defaultToken(); got != "env-token" {
		t.Fatalf("defaultToken = %q", got)
	}

	t.Setenv("YCODE_API_TOKEN", "")
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ycode"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ycode", "api_token")
	if err := os.WriteFile(path, []byte("  file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A trailing newline in the file must not end up in the header.
	if got := defaultToken(); got != "file-token" {
		t.Fatalf("defaultToken = %q", got)
	}
}

func TestNoTokenSendsNoHeader(t *testing.T) {
	t.Setenv("YCODE_API_TOKEN", "")
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var present bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, present = r.Header["Authorization"]
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	New(srv.URL).Health()
	if present {
		t.Fatal("a header was sent with no token; a loopback server should not see one")
	}
}

// /healthz is the probe a supervisor uses, so it must not be given a token and
// must not follow a redirect into something that needs one.
func TestHealthSucceedsWithNoBody(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Health(); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestBaseTrailingSlashIsTrimmed(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	New(srv.URL + "/").Health()
	if path != "/healthz" {
		t.Fatalf("path = %q, want /healthz", path)
	}
}

func TestUnreachableServerIsAnError(t *testing.T) {
	c := New("http://127.0.0.1:1")
	if err := c.Health(); err == nil {
		t.Fatal("an unreachable server should error")
	}
}

// Malformed JSON is a bug in the server, and must not read as success.
func TestMalformedResponseIsAnError(t *testing.T) {
	c := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `not json at all`)
	})
	got, err := c.Models()
	if err == nil {
		t.Fatalf("malformed JSON returned no error (map %v)", got)
	}
}

func TestNewWithEmptyTokenFallsBackToTheDefault(t *testing.T) {
	t.Setenv("YCODE_API_TOKEN", "env-token")
	if got := NewWithToken("http://x", "").Token; got != "env-token" {
		t.Fatalf("Token = %q, an empty token should not clear the default", got)
	}
}
