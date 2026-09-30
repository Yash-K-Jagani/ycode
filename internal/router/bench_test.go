package router

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Benchmarks for the router, which every provider call goes through.
//
// It is on the hot path for two reasons that are easy to forget: it owns the
// provider cache, which is what stopped every turn paying a fresh TCP connect,
// and it accumulates usage, which is what the daily budget and /status read.

func routerFor(tb testing.TB, active, url string) *Router {
	tb.Helper()
	cfg := config.Defaults()
	cfg.ActiveProvider = active
	cfg.ActiveModel = "m"
	r := New(cfg)
	r.SetFactoryForTest(func(id, key, host string) (providers.Provider, error) {
		return stubProv{id: id, url: url}, nil
	})
	return r
}

// stubProv is a minimal provider that streams from a fixed server.
type stubProv struct{ id, url string }

func (s stubProv) Name() string { return s.id }
func (s stubProv) ListModels(context.Context) ([]apitypes.ModelInfo, error) {
	return nil, nil
}
func (s stubProv) Complete(context.Context, string, []apitypes.Message) (string, error) {
	return "", nil
}
func (s stubProv) Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error) {
	return apitypes.StreamChunk{}, nil
}

func (s stubProv) StreamWithTools(ctx context.Context, model string, msgs []apitypes.Message, _ []apitypes.ToolSpec, w io.Writer) (apitypes.StreamChunk, error) {
	return s.Stream(ctx, model, msgs, w)
}

// The provider cache is what stopped a fresh connection pool per turn. Getting
// it back costs a lock and a map lookup, and it runs several times per turn.
func BenchmarkProviderCacheHit(b *testing.B) {
	r := routerFor(b, "gemini", "http://127.0.0.1:1")
	if _, err := r.Provider("gemini"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := r.Provider("gemini"); err != nil {
			b.Fatal(err)
		}
	}
}

// The cost of the very first call, before the cache is warm. Named for what it
// actually measures: an earlier version of this claimed to be a miss on every
// iteration, which it was not - the cache persists, so it was a slower hit.
func BenchmarkProviderColdCache(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := routerFor(b, "gemini", "http://127.0.0.1:1")
		if _, err := r.Provider("gemini"); err != nil {
			b.Fatal(err)
		}
	}
}

// Fallbacks is consulted at the start of every fallible call and reads config
// each time - which is where a cost per turn would hide.
func BenchmarkFallbacks(b *testing.B) {
	cfg := config.Defaults()
	cfg.SetKeyFor("gemini", "k")
	cfg.SetKeyFor("groq", "k")
	cfg.SetKeyFor("openrouter", "k")
	r := New(cfg)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Fallbacks()
	}
}

// Usage accumulation is called once per provider attempt. TakeUsage resets it,
// and a caller that forgets would double the next turn's figure.
func BenchmarkUsageAccounting(b *testing.B) {
	r := &Router{}
	u := Usage{PromptTok: 1200, ComplTok: 340, Reported: true}
	b.Run("record", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r.record("gemini", "m", u)
		}
	})
	b.Run("record_and_take", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r.record("gemini", "m", u)
			_ = r.TakeUsage()
		}
	})
}

// Usage folding runs per chunk, so a long answer hits it a thousand times.
func BenchmarkUsageAdd(b *testing.B) {
	var acc Usage
	chunk := Usage{PromptTok: 0, ComplTok: 1, Reported: true}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		acc.Add("gemini", "m", chunk)
	}
}

// Every prompt is marshalled to JSON for every provider request. It is not
// expensive, but it happens on every attempt and nobody had measured it.
func BenchmarkMarshalMessages(b *testing.B) {
	msgs := []apitypes.Message{
		{Role: apitypes.RoleSystem, Content: strings.Repeat("system prompt line. ", 60)},
		{Role: apitypes.RoleUser, Content: strings.Repeat("user message. ", 40)},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(msgs); err != nil {
			b.Fatal(err)
		}
	}
}

// The idle watchdog is armed per stream and cancelled at the end.
func BenchmarkStatsRecord(b *testing.B) {
	s := &Stats{ByProvider: map[string]*ProviderStat{}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.Record("gemini", 100*time.Millisecond, 500, true)
	}
}
