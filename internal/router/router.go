package router

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/db"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

type Router struct {
	cfg   config.Config
	stats *Stats

	// mu guards the provider cache below.
	mu sync.RWMutex
	// cache memoises providers per name. Constructing a provider built a fresh
	// *http.Client, and therefore a fresh connection pool, and Provider was
	// called once per turn and again per fallback candidate. With Go's default
	// MaxIdleConnsPerHost of 2, almost every turn paid a fresh TCP connect and
	// a fresh TLS handshake to a host we had just been talking to. Caching the
	// provider keeps the pool alive across turns.
	cache map[string]cachedProvider

	// factory builds a provider. It is a field rather than a direct call into
	// the providers table so tests can drive the fallback chain without
	// standing up four HTTP servers, and so a future provider type (a local
	// adapter, say) can be substituted without touching the call sites.
	factory func(id, key, host string) (providers.Provider, error)
}

// cachedProvider is a memoised provider plus the key it was built for, so a
// changed API key replaces it instead of being ignored.
type cachedProvider struct {
	prov providers.Provider
	key  string
	host string
}

func New(cfg config.Config) *Router {
	return &Router{
		cfg:     cfg,
		stats:   NewStats(),
		cache:   map[string]cachedProvider{},
		factory: defaultFactory,
	}
}

// defaultFactory is the real construction path: look the spec up and build it.
func defaultFactory(id, key, host string) (providers.Provider, error) {
	spec := providers.Get(id)
	if spec == nil {
		return nil, fmt.Errorf("unknown provider %q", id)
	}
	return spec.New(key, host), nil
}

func (r *Router) Update(cfg config.Config) {
	r.cfg = cfg
	// A new key or host invalidates the cached clients, so a key the user just
	// pasted takes effect on the next turn.
	r.mu.Lock()
	if len(r.cache) > 0 {
		r.cache = map[string]cachedProvider{}
	}
	r.mu.Unlock()
}

func (r *Router) Stats() *Stats { return r.stats }

func (r *Router) Provider(name string) (providers.Provider, error) {
	if r.cfg.ZeroDataLeak && !providers.Local(name) {
		return nil, fmt.Errorf("zero-data-leak mode: cloud provider %q is blocked (local only)", name)
	}
	spec := providers.Get(name)
	if spec == nil {
		return nil, fmt.Errorf("unknown provider %q", name)
	}
	key := r.cfg.KeyFor(spec.ID)
	host := r.cfg.OllamaHost

	r.mu.RLock()
	if c, ok := r.cache[name]; ok && c.key == key && c.host == host {
		r.mu.RUnlock()
		return c.prov, nil
	}
	r.mu.RUnlock()

	prov, err := r.factory(name, key, host)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[name] = cachedProvider{prov: prov, key: key, host: host}
	r.mu.Unlock()
	return prov, nil
}

func (r *Router) Active() (providers.Provider, string, error) {
	p, err := r.Provider(r.cfg.ActiveProvider)
	if err != nil {
		return nil, "", err
	}
	model := r.cfg.ActiveModel
	// A local provider can enumerate what is installed, so an unset model is
	// recoverable here; a cloud catalogue is too large to guess at.
	if model == "" && providers.Local(r.cfg.ActiveProvider) {
		models, err := p.ListModels(context.Background())
		if err != nil {
			return nil, "", err
		}
		if len(models) == 0 {
			return nil, "", fmt.Errorf("no ollama models installed — run: ollama pull %s", providers.DefaultModel("ollama"))
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

// Fallbacks returns other configured cloud providers (those with an API key),
// in registry order. Local is excluded: falling back from a local model to a
// cloud one would leak data even outside zero-data-leak mode, and falling back
// to a different local model is not something the registry can choose.
func (r *Router) Fallbacks() []Fallback {
	if r.cfg.ZeroDataLeak {
		return nil
	}
	var out []Fallback
	for _, s := range providers.All() {
		if s.Local || s.ID == r.cfg.ActiveProvider {
			continue
		}
		if r.cfg.KeyFor(s.ID) == "" {
			continue
		}
		out = append(out, Fallback{Provider: s.ID, Model: s.Recommended})
	}
	return out
}

type Fallback struct {
	Provider string
	Model    string
}

// StreamWithFallback tries the active provider, then configured cloud fallbacks.
// Returns the text, a fallback note ("" when primary worked), and error.
//
// Each attempt streams into a private buffer and is copied to w only once it
// has succeeded in full. That is the whole point of the buffering: a provider
// that fails halfway through has already written its partial answer to the
// writer it was given, so streaming straight into the caller's writer spliced a
// truncated first attempt in front of a complete second one. The user saw two
// answers welded together - the tail of a model that died mid-sentence followed
// by the head of one that worked - and neither the transcript nor the audit log
// could tell that had happened.
//
// A failed attempt's partial output is kept for the error message, so nothing is
// lost diagnostically; it is just not shown as if it were the answer.
func (r *Router) StreamWithFallback(ctx context.Context, msgs []apitypes.Message, w io.Writer) (string, string, error) {
	p, model, err := r.Active()
	if err != nil {
		return "", "", err
	}
	t0 := time.Now()
	var attempt bytes.Buffer
	chunk, err := p.Stream(ctx, model, msgs, &attempt)
	r.stats.Record(r.cfg.ActiveProvider, time.Since(t0), len(chunk.Delta)/4, err == nil)
	if err == nil {
		_, _ = w.Write(attempt.Bytes())
		return chunk.Delta, "", nil
	}
	primaryErr := err
	// Every attempt's error is kept. Returning only the first meant that when
	// all three providers failed, the message named the primary and said
	// nothing about why the other two were rejected - which is usually the
	// part that explains the real problem (an expired key, a rate limit).
	var errs []string
	errs = append(errs, fmt.Sprintf("%s: %v", r.cfg.ActiveProvider, primaryErr))
	for _, fb := range r.Fallbacks() {
		fp, err := r.Provider(fb.Provider)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", fb.Provider, err))
			continue
		}
		attempt.Reset()
		t0 := time.Now()
		chunk, err := fp.Stream(ctx, fb.Model, msgs, &attempt)
		r.stats.Record(fb.Provider, time.Since(t0), len(chunk.Delta)/4, err == nil)
		if err == nil {
			_, _ = w.Write(attempt.Bytes())
			note := fmt.Sprintf("↳ primary %s failed (%v); fell back to %s/%s",
				r.cfg.ActiveProvider, primaryErr, fb.Provider, fb.Model)
			if len(errs) > 1 {
				note += fmt.Sprintf(" (%d earlier attempt(s) also failed)", len(errs)-1)
			}
			return chunk.Delta, note, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", fb.Provider, err))
	}
	return "", "", fmt.Errorf("every provider failed — %s", strings.Join(errs, "; "))
}

// --- latency stats ---

type ProviderStat struct {
	Calls    int     `json:"calls"`
	Failures int     `json:"failures"`
	TotalMs  int64   `json:"total_ms"`
	Tokens   int     `json:"tokens"`
	AvgTokS  float64 `json:"avg_tok_s,omitempty"`
	// FastestTokS is the best single-call rate observed, which is a different
	// question from the average and a useful one: it is what the provider did
	// on its best run, and it is the number to compare a slow session against.
	FastestTokS float64 `json:"fastest_tok_s,omitempty"`
	// TokSRateSum/RateCount accumulate the per-call rates. They are persisted
	// rather than a running mean so that a restart does not silently change
	// the figure the user has been reading.
	TokSRateSum float64 `json:"tok_s_rate_sum,omitempty"`
	RateCount   int     `json:"rate_count,omitempty"`
}

type Stats struct {
	mu         sync.Mutex
	file       string
	conn       *sql.DB
	ByProvider map[string]*ProviderStat `json:"by_provider"`
}

func statsFile() string { return filepath.Join(config.Dir(), "router.json") }

func NewStats() *Stats {
	s := &Stats{file: statsFile(), ByProvider: map[string]*ProviderStat{}}
	s.conn = db.Shared()
	if s.conn != nil {
		if raw, ok := db.KVGet(s.conn, "router/stats"); ok {
			_ = json.Unmarshal([]byte(raw), s)
		} else if data, err := os.ReadFile(s.file); err == nil {
			_ = json.Unmarshal(data, s)
			s.persist()
		}
		if s.ByProvider == nil {
			s.ByProvider = map[string]*ProviderStat{}
		}
		return s
	}
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
	// AvgTokS used to be cumulative tokens over cumulative seconds. That is
	// aggregate throughput, not an average of rates, and the two disagree in a
	// way that misleads: one 10s call producing 1000 tokens followed by nine
	// instant failures reports 100 tok/s as the "average", which reads as a
	// healthy provider when almost every call failed.
	//
	// The mean is now taken over the calls that actually produced tokens. A
	// call that produced none has no rate, and including a zero would drag the
	// average toward a number that describes failures rather than throughput.
	secs := d.Seconds()
	if tokens > 0 && secs > 0 {
		rate := float64(tokens) / secs
		st.TokSRateSum += rate
		st.RateCount++
		if rate > st.FastestTokS {
			st.FastestTokS = rate
		}
	}
	if st.RateCount > 0 {
		st.AvgTokS = st.TokSRateSum / float64(st.RateCount)
	}
	s.persist()
}

func (s *Stats) persist() {
	data, _ := json.Marshal(s)
	if s.conn != nil {
		if err := db.KVSet(s.conn, "router/stats", string(data)); err == nil {
			return
		}
	}
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
