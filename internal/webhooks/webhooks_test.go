package webhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

// Webhooks fire on session_start, turn_complete and the goal events. There
// were no tests, and both of the ways they could fail - an unparseable config
// and a rejected delivery - were silent, so a broken integration looked
// exactly like a night with nothing worth reporting.

type capture struct {
	mu   sync.Mutex
	body map[string]any
	n    int
}

func (c *capture) add(r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.body = m
}

// writeConfig points config.Dir() at a temporary home and drops a
// webhooks.yaml into it. os.UserHomeDir reads USERPROFILE on Windows and HOME
// elsewhere, so both are set.
func writeConfig(t *testing.T, yaml string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".ycode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "webhooks.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.Dir(); got != dir {
		t.Fatalf("config.Dir() = %q, want %q", got, dir)
	}
}

// captureStderr collects what the code under test writes to os.Stderr, which is
// where delivery and config problems are reported.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

func TestFireDeliversTheEventAndPayload(t *testing.T) {
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		got.add(r)
	}))
	defer srv.Close()

	writeConfig(t, "webhooks:\n  - url: "+srv.URL+"\n")
	Fire("turn_complete", map[string]any{"mode": "goal", "answer": "done"})

	if got.n != 1 {
		t.Fatalf("got %d deliveries, want 1", got.n)
	}
	for k, want := range map[string]any{"event": "turn_complete", "mode": "goal", "answer": "done"} {
		if got.body[k] != want {
			t.Fatalf("payload[%q] = %v, want %v", k, got.body[k], want)
		}
	}
	// A receiver cannot make sense of an event with no timestamp.
	if _, ok := got.body["at"]; !ok {
		t.Fatal("payload has no timestamp")
	}
}

func TestOnlyMatchingTargetsAreCalled(t *testing.T) {
	var wanted, unwanted int
	wantedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wanted++
	}))
	defer wantedSrv.Close()
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unwanted++
	}))
	defer otherSrv.Close()

	writeConfig(t, "webhooks:\n"+
		"  - url: "+wantedSrv.URL+"\n    events: [turn_complete]\n"+
		"  - url: "+otherSrv.URL+"\n    events: [session_start]\n"+
		"  - url: "+otherSrv.URL+"\n    events: ['*']\n")

	Fire("turn_complete", map[string]any{})

	if wanted != 1 {
		t.Fatalf("the matching target got %d deliveries", wanted)
	}
	// The wildcard target must fire; the session_start-only one must not.
	if unwanted != 1 {
		t.Fatalf("expected exactly the wildcard target, got %d", unwanted)
	}
}

func TestNoConfigMeansNoDelivery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	// No webhooks.yaml at all: Fire must be a no-op, not a panic.
	Fire("turn_complete", map[string]any{})
}

// A target that cannot be reached must not stop the others from being tried.
func TestOneBadTargetDoesNotBlockTheRest(t *testing.T) {
	delivered := 0
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered++
	}))
	defer good.Close()

	writeConfig(t, "webhooks:\n"+
		"  - url: \"://not a url\"\n"+
		"  - url: http://127.0.0.1:1/nope\n"+
		"  - url: "+good.URL+"\n")

	Fire("turn_complete", map[string]any{})

	if delivered != 1 {
		t.Fatalf("the reachable target got %d deliveries, want 1", delivered)
	}
}

// The point of the status check: a rejected delivery used to be indistinguishable
// from a successful one.
func TestRejectedDeliveryIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad token")
	}))
	defer srv.Close()

	writeConfig(t, "webhooks:\n  - url: "+srv.URL+"\n")

	stderr := captureStderr(t, func() { Fire("turn_complete", map[string]any{}) })
	if stderr == "" {
		t.Fatal("a 401 delivery was not reported")
	}
	if !strings.Contains(stderr, "401") {
		t.Fatalf("the report does not name the status: %q", stderr)
	}
	if !strings.Contains(stderr, srv.URL) {
		t.Fatalf("the report does not name the target: %q", stderr)
	}
}

func TestUnparseableConfigIsReported(t *testing.T) {
	writeConfig(t, "webhooks: [ this is not: valid: yaml")

	stderr := captureStderr(t, func() { Fire("turn_complete", map[string]any{}) })
	if stderr == "" {
		t.Fatal("an unparseable webhooks.yaml was silently ignored")
	}
	if !strings.Contains(stderr, "cannot parse") {
		t.Fatalf("the report does not say what went wrong: %q", stderr)
	}
}
