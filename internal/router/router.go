package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/internal/providers/gemini"
	"github.com/Yash-K-Jagani/ycode/internal/providers/groq"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openrouter"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Router struct {
	cfg   config.Config
	stats *Stats
}

func New(cfg config.Config) *Router { return &Router{cfg: cfg, stats: NewStats()} }

func (r *Router) Update(cfg config.Config) { r.cfg = cfg }

func (r *Router) Stats() *Stats { return r.stats }

func (r *Router) Provider(name string) (providers.Provider, error) {
	if r.cfg.ZeroDataLeak && name != "ollama" && name != "" {
		return nil, fmt.Errorf("zero-data-leak mode: cloud provider %q is blocked (local only)", name)
	}
	switch name {
	case "ollama", "":
		return ollama.New(r.cfg.OllamaHost), nil
	case "gemini":
		return gemini.New(r.cfg.GeminiAPIKey), nil
	case "openrouter":
		return openrouter.New(r.cfg.OpenRouterKey), nil
	case "groq":
		return groq.New(r.cfg.GroqKey), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", name)
	}
}

func (r *Router) Active() (providers.Provider, string, error) {
	p, err := r.Provider(r.cfg.ActiveProvider)
	if err != nil {
		return nil, "", err
	}
	model := r.cfg.ActiveModel
	if model == "" && r.cfg.ActiveProvider == "ollama" {
		models, err := p.ListModels(context.Background())
		if err != nil {
			return nil, "", err
		}
		if len(models) == 0 {
			return nil, "", fmt.Errorf("no ollama models installed — run: ollama pull qwen2.5-coder:7b-instruct-q4_K_M")
		}
		model = models[0].ID
	}
	if model == "" {
		return nil, "", fmt.Errorf("no model selected — use /models")
	}
	return p, model, nil
}

func (r *Router) Stream(ctx context.Context, msgs []apitypes.Message, w io.Writer) (string, error) {
	p, model, err := r.Active()
	if err != nil {
		return "", err
	}
	t0 := time.Now()
	chunk, err := p.Stream(ctx, model, msgs, w)
	r.stats.Record(r.cfg.ActiveProvider, time.Since(t0), len(chunk.Delta)/4, err == nil)
	if err != nil {
		return "", err
	}
	return chunk.Delta, nil
}

// Fallbacks returns other configured cloud providers (have API keys), ranked.
// Empty in zero-data-leak mode.
func (r *Router) Fallbacks() []Fallback {
	if r.cfg.ZeroDataLeak {
		return nil
	}
	var out []Fallback
	if r.cfg.GeminiAPIKey != "" && r.cfg.ActiveProvider != "gemini" {
		out = append(out, Fallback{Provider: "gemini", Model: gemini.DefaultModels[0]})
	}
	if r.cfg.OpenRouterKey != "" && r.cfg.ActiveProvider != "openrouter" {
		out = append(out, Fallback{Provider: "openrouter", Model: openrouter.DefaultModels[0]})
	}
	if r.cfg.GroqKey != "" && r.cfg.ActiveProvider != "groq" {
		out = append(out, Fallback{Provider: "groq", Model: groq.DefaultModels[0]})
	}
	return out
}

type Fallback struct {
	Provider string
	Model    string
}

// StreamWithFallback tries the active provider, then configured cloud fallbacks.
// Returns the text, a fallback note ("" when primary worked), and error.
func (r *Router) StreamWithFallback(ctx context.Context, msgs []apitypes.Message, w io.Writer) (string, string, error) {
	p, model, err := r.Active()
	if err != nil {
		return "", "", err
	}
	t0 := time.Now()
	chunk, err := p.Stream(ctx, model, msgs, w)
	r.stats.Record(r.cfg.ActiveProvider, time.Since(t0), len(chunk.Delta)/4, err == nil)
	if err == nil {
		return chunk.Delta, "", nil
	}
	firstErr := err
	for _, fb := range r.Fallbacks() {
		fp, err := r.Provider(fb.Provider)
		if err != nil {
			continue
		}
		t0 := time.Now()
		chunk, err := fp.Stream(ctx, fb.Model, msgs, w)
		r.stats.Record(fb.Provider, time.Since(t0), len(chunk.Delta)/4, err == nil)
		if err == nil {
			return chunk.Delta, fmt.Sprintf("↳ primary %s failed (%v); fell back to %s/%s", r.cfg.ActiveProvider, firstErr, fb.Provider, fb.Model), nil
		}
	}
	return "", "", firstErr
}

// --- latency stats ---

type ProviderStat struct {
	Calls    int     `json:"calls"`
	Failures int     `json:"failures"`
	TotalMs  int64   `json:"total_ms"`
	Tokens   int     `json:"tokens"`
	AvgTokS  float64 `json:"avg_tok_s,omitempty"`
}

type Stats struct {
	mu         sync.Mutex
	file       string
	ByProvider map[string]*ProviderStat `json:"by_provider"`
}

func statsFile() string { return filepath.Join(config.Dir(), "router.json") }

func NewStats() *Stats {
	s := &Stats{file: statsFile(), ByProvider: map[string]*ProviderStat{}}
	data, _ := os.ReadFile(s.file)
	_ = json.Unmarshal(data, s)
	if s.ByProvider == nil {
		s.ByProvider = map[string]*ProviderStat{}
	}
	return s
}

func (s *Stats) Record(provider string, d time.Duration, tokens int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ByProvider[provider]
	if st == nil {
		st = &ProviderStat{}
		s.ByProvider[provider] = st
	}
	st.Calls++
	st.TotalMs += d.Milliseconds()
	st.Tokens += tokens
	if !ok {
		st.Failures++
	}
	if d.Seconds() > 0 {
		st.AvgTokS = float64(st.Tokens) / (float64(st.TotalMs) / 1000)
	}
	data, _ := json.Marshal(s)
	_ = os.MkdirAll(config.Dir(), 0o755)
	_ = os.WriteFile(s.file, data, 0o644)
}

func (s *Stats) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ByProvider) == 0 {
		return "no latency data yet"
	}
	out := ""
	for name, st := range s.ByProvider {
		avg := 0.0
		if st.Calls > 0 {
			avg = float64(st.TotalMs) / float64(st.Calls) / 1000
		}
		out += fmt.Sprintf("%s: %d calls (%.1fs avg, %.1f tok/s, %d fails)\n", name, st.Calls, avg, st.AvgTokS, st.Failures)
	}
	return out
}
