package router

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// fakeProvider is a Provider whose Stream behaviour is scripted per test.
type fakeProvider struct {
	id string
	// emit is written to the caller's writer before streamErr is returned, so
	// a test can reproduce a provider that dies mid-answer.
	emit      []string
	streamErr error
	complete  string
}

func (f *fakeProvider) Name() string { return f.id }
func (f *fakeProvider) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return []apitypes.ModelInfo{{ID: f.id + "-model", Provider: f.id}}, nil
}
func (f *fakeProvider) Stream(_ context.Context, _ string, _ []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	for _, s := range f.emit {
		_, _ = io.WriteString(w, s)
	}
	return apitypes.StreamChunk{Delta: strings.Join(f.emit, ""), Done: f.streamErr == nil}, f.streamErr
}
func (f *fakeProvider) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return f.complete, f.streamErr
}

// withProviders builds a router whose factory returns the supplied fakes.
func withProviders(t *testing.T, cfg config.Config, fakes map[string]providers.Provider) *Router {
	t.Helper()
	r := New(cfg)
	r.factory = func(id, _, _ string) (providers.Provider, error) {
		if p, ok := fakes[id]; ok {
			return p, nil
		}
		return nil, errors.New("no fake for " + id)
	}
	return r
}

func fallbackCfg() config.Config {
	cfg := config.Defaults()
	cfg.ActiveProvider = "gemini"
	cfg.ActiveModel = "m1"
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("groq", "k")
	return cfg
}

// The bug this pins: a primary that fails halfway through has already written
// to the caller's writer. Streaming each attempt straight into that writer put
// the tail of a dead answer in front of the head of the working one, and the
// transcript showed two answers welded together.
func TestFallbackDoesNotSplicePartialPrimary(t *testing.T) {
	primary := &fakeProvider{
		id:        "gemini",
		emit:      []string{"This answer is going to be ", "cut off mid-"},
		streamErr: errors.New("connection reset"),
	}
	backup := &fakeProvider{
		id:       "groq",
		emit:     []string{"Complete answer ", "from the fallback."},
		complete: "Complete answer from the fallback.",
	}
	r := withProviders(t, fallbackCfg(), map[string]providers.Provider{
		"gemini": primary,
		"groq":   backup,
	})

	var sink strings.Builder
	full, note, err := r.StreamWithFallback(context.Background(), nil, &sink)
	if err != nil {
		t.Fatalf("expected the fallback to succeed, got %v", err)
	}
	if full != "Complete answer from the fallback." {
		t.Fatalf("returned text = %q", full)
	}
	// The user's writer must have seen the fallback only.
	if got := sink.String(); got != full {
		t.Fatalf("what the user saw is not the answer returned:\n got: %q\nwant: %q", got, full)
	}
	for _, debris := range []string{"cut off", "connection reset", "This answer"} {
		if strings.Contains(sink.String(), debris) {
			t.Errorf("the failed attempt leaked into the transcript: %q", sink.String())
		}
	}
	if !strings.Contains(note, "fell back to groq") {
		t.Errorf("note should explain the fallback, got %q", note)
	}
}

// A primary that succeeds writes through, unchanged.
func TestPrimarySuccessIsWrittenVerbatim(t *testing.T) {
	p := &fakeProvider{id: "gemini", emit: []string{"hello ", "there"}}
	r := withProviders(t, fallbackCfg(), map[string]providers.Provider{"gemini": p})
	var sink strings.Builder
	full, note, err := r.StreamWithFallback(context.Background(), nil, &sink)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if full != "hello there" || sink.String() != "hello there" {
		t.Fatalf("full=%q sink=%q", full, sink.String())
	}
	if note != "" {
		t.Fatalf("no fallback happened, so the note should be empty, got %q", note)
	}
}

// When everything fails, nothing may reach the writer. Writing a partial answer
// from a failed provider is how the transcript ends up showing a reply that was
// never actually delivered.
func TestAllProvidersFailWritesNothing(t *testing.T) {
	bad := func(id string) *fakeProvider {
		return &fakeProvider{id: id, emit: []string{"partial " + id}, streamErr: errors.New(id + " is down")}
	}
	r := withProviders(t, fallbackCfg(), map[string]providers.Provider{
		"gemini":     bad("gemini"),
		"groq":       bad("groq"),
		"openrouter": bad("openrouter"),
	})
	var sink strings.Builder
	full, _, err := r.StreamWithFallback(context.Background(), nil, &sink)
	if err == nil {
		t.Fatal("expected an error when every provider fails")
	}
	if full != "" {
		t.Fatalf("no answer should be returned, got %q", full)
	}
	if sink.String() != "" {
		t.Fatalf("nothing should have been written, got %q", sink.String())
	}
}

// Returning only the first error meant a user whose key had expired, and whose
// fallbacks were all rate-limited, was told about the primary and nothing else.
func TestAllProvidersFailNamesEveryAttempt(t *testing.T) {
	bad := func(id string) *fakeProvider {
		return &fakeProvider{id: id, emit: []string{"x"}, streamErr: errors.New(id + ": 401 unauthorized")}
	}
	r := withProviders(t, fallbackCfg(), map[string]providers.Provider{
		"gemini":     bad("gemini"),
		"groq":       bad("groq"),
		"openrouter": bad("openrouter"),
	})
	_, _, err := r.StreamWithFallback(context.Background(), nil, io.Discard)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"gemini", "401 unauthorized"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got %q", want, msg)
		}
	}
	if !strings.Contains(msg, "groq") && !strings.Contains(msg, "openrouter") {
		t.Errorf("error should name the fallback attempts too, got %q", msg)
	}
}

// A nil writer is legitimate - the headless path passes io.Discard, and
// discarding is still a writer. Nothing may nil-deref.
func TestFallbackHandlesDiscardWriter(t *testing.T) {
	backup := &fakeProvider{id: "groq", emit: []string{"ok"}}
	r := withProviders(t, fallbackCfg(), map[string]providers.Provider{
		"gemini": &fakeProvider{id: "gemini", emit: []string{"no"}, streamErr: errors.New("down")},
		"groq":   backup,
	})
	full, _, err := r.StreamWithFallback(context.Background(), nil, io.Discard)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if full != "ok" {
		t.Fatalf("full = %q", full)
	}
}

// Zero-data-leak must still short-circuit before any provider is built.
func TestFallbackRespectsZeroDataLeak(t *testing.T) {
	cfg := fallbackCfg()
	cfg.ZeroDataLeak = true
	r := withProviders(t, cfg, map[string]providers.Provider{
		"gemini": &fakeProvider{id: "gemini", emit: []string{"leaked"}},
		"groq":   &fakeProvider{id: "groq", emit: []string{"also leaked"}},
	})
	var sink strings.Builder
	if _, _, err := r.StreamWithFallback(context.Background(), nil, &sink); err == nil {
		t.Fatal("a cloud primary must be refused in zero-data-leak mode")
	}
	if sink.String() != "" {
		t.Fatalf("nothing may be written in zero-data-leak mode, got %q", sink.String())
	}
}

// --- provider caching (P1.8) ---

// A provider built per turn meant a new connection pool per turn, so almost
// every request paid a fresh connect and TLS handshake to the host we had just
// been talking to.
func TestProvidersAreCachedAcrossTurns(t *testing.T) {
	built := 0
	cfg := fallbackCfg()
	r := New(cfg)
	r.factory = func(id, _, _ string) (providers.Provider, error) {
		built++
		return &fakeProvider{id: id, emit: []string{"ok"}}, nil
	}
	for i := 0; i < 5; i++ {
		if _, err := r.Provider("gemini"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if built != 1 {
		t.Fatalf("provider built %d times across 5 lookups, want 1", built)
	}
}

// Caching must not outlive the key it was built for: a user who pastes a new key
// during /connect expects the next turn to use it.
func TestProviderCacheInvalidatesOnKeyChange(t *testing.T) {
	cfg := fallbackCfg()
	r := New(cfg)
	var keys []string
	r.factory = func(id, key, _ string) (providers.Provider, error) {
		keys = append(keys, key)
		return &fakeProvider{id: id}, nil
	}
	if _, err := r.Provider("gemini"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Provider("gemini"); err != nil {
		t.Fatal(err)
	}
	cfg.SetKeyFor("gemini", "a-new-key")
	r.Update(cfg)
	if _, err := r.Provider("gemini"); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected a rebuild after the key changed, builds = %v", keys)
	}
	if keys[1] != "a-new-key" {
		t.Fatalf("rebuilt with the wrong key: %q", keys[1])
	}
}

// Two providers must not collide in the cache.
func TestProviderCacheIsPerName(t *testing.T) {
	r := New(fallbackCfg())
	r.factory = func(id, _, _ string) (providers.Provider, error) {
		return &fakeProvider{id: id}, nil
	}
	a, err := r.Provider("gemini")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Provider("groq")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different providers were handed back the same client")
	}
	if a.Name() != "gemini" || b.Name() != "groq" {
		t.Fatalf("wrong clients: %s / %s", a.Name(), b.Name())
	}
	again, _ := r.Provider("gemini")
	if again != a {
		t.Fatal("the gemini client was not reused")
	}
}

func TestProviderCacheSurvivesFactoryError(t *testing.T) {
	r := New(fallbackCfg())
	r.factory = func(string, string, string) (providers.Provider, error) {
		return nil, errors.New("nope")
	}
	if _, err := r.Provider("gemini"); err == nil {
		t.Fatal("expected the factory error")
	}
	// A failed build must not be cached, or a later successful build would
	// never happen.
	if _, err := r.Provider("gemini"); err == nil {
		t.Fatal("a failed construction should not be memoised")
	}
}
