// Package providers declares the model providers ycode can talk to and the
// client each one needs.
package providers

import (
	"context"
	"io"

	"github.com/Yash-K-Jagani/ycode/internal/providers/gemini"
	"github.com/Yash-K-Jagani/ycode/internal/providers/groq"
	"github.com/Yash-K-Jagani/ycode/internal/providers/ollama"
	"github.com/Yash-K-Jagani/ycode/internal/providers/openrouter"
	"github.com/Yash-K-Jagani/ycode/pkg/apitypes"
)

// Provider is a backend ycode can stream completions from.
type Provider interface {
	Name() string
	ListModels(ctx context.Context) ([]apitypes.ModelInfo, error)
	Stream(ctx context.Context, model string, msgs []apitypes.Message, w io.Writer) (apitypes.StreamChunk, error)
	Complete(ctx context.Context, model string, msgs []apitypes.Message) (string, error)
}

// Spec is everything ycode needs to know about a provider. Adding one means
// adding a row here and a key accessor on config.Config — the construction
// switch, the fallback chain, the zero-data-leak check, the price table and
// the onboarding picker are all derived from this.
type Spec struct {
	// ID is the stable name used in config, the CLI, and the API.
	ID string
	// Label is the one-line description shown in the provider picker.
	Label string
	// Local marks a provider that runs on the user's own machine: free,
	// unmetered, and the only kind zero-data-leak mode permits.
	Local bool
	// NeedsKey is true when the provider is unusable without an API key.
	NeedsKey bool
	// KeyEnv is the default environment variable holding that key. Empty
	// when NeedsKey is false. config may override it per install.
	KeyEnv string
	// PromptUSD and ComplUSD are prices per 1K tokens. Both zero for local.
	PromptUSD float64
	ComplUSD  float64
	// Models are the curated defaults, best first. Recommended is the one
	// suggested when a provider is chosen with no model preference.
	Models      []string
	Recommended string
	// SizeHint is what the setup wizard tells the user to expect, e.g.
	// "needs ~2 GB". Empty where download size is meaningless (a cloud API).
	SizeHint string
	// New builds a client. key is the API key, host the local daemon URL;
	// each provider uses the one it needs and ignores the other.
	New func(key, host string) Provider
}

// specs is the provider table, in the order the picker offers them: local
// first, because it is the recommended default.
var specs = []Spec{
	{
		ID:     "ollama",
		Label:  "Ollama (local, free, private - recommended)",
		Local:  true,
		Models: []string{"qwen2.5-coder:3b", "qwen2.5-coder:7b-instruct-q4_K_M"},
		// The 3B model is the recommendation because it is the one that
		// reliably loads and drives tools on ordinary hardware; the 7B is
		// listed for machines that have the memory for it.
		Recommended: "qwen2.5-coder:3b",
		SizeHint:    "needs ~2 GB",
		New:         func(key, host string) Provider { return ollama.New(host) },
	},
	{
		ID:        "gemini",
		Label:     "Google Gemini (fast, generous free tier)",
		NeedsKey:  true,
		KeyEnv:    "GEMINI_API_KEY",
		PromptUSD: 0.000075,
		ComplUSD:  0.0003,
		Models:    []string{"gemini-2.5-flash", "gemini-2.5-pro"},
		// A small model cannot drive tools reliably, so prefer the capable
		// one for unattended work.
		Recommended: "gemini-2.5-pro",
		New:         func(key, host string) Provider { return gemini.New(key) },
	},
	{
		ID:        "openrouter",
		Label:     "OpenRouter (many models, pay per token)",
		NeedsKey:  true,
		KeyEnv:    "OPENROUTER_API_KEY",
		PromptUSD: 0.0001,
		ComplUSD:  0.0003,
		Models:    openrouter.DefaultModels,
		// Free tiers first: a failed run should not become a bill.
		Recommended: openrouter.DefaultModels[0],
		New:         func(key, host string) Provider { return openrouter.New(key) },
	},
	{
		ID:          "groq",
		Label:       "Groq (very fast, free tier)",
		NeedsKey:    true,
		KeyEnv:      "GROQ_API_KEY",
		PromptUSD:   0.00005,
		ComplUSD:    0.00008,
		Models:      groq.DefaultModels,
		Recommended: groq.DefaultModels[0],
		New:         func(key, host string) Provider { return groq.New(key) },
	},
}

var byID = func() map[string]*Spec {
	m := make(map[string]*Spec, len(specs))
	for i := range specs {
		m[specs[i].ID] = &specs[i]
	}
	return m
}()

// All returns every provider, in picker order.
func All() []Spec { return specs }

// IDs returns the provider names in picker order.
func IDs() []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.ID
	}
	return out
}

// Get returns the spec for a provider, or nil if there is none. An empty name
// means the default provider, which is local.
func Get(id string) *Spec {
	if id == "" {
		return byID["ollama"]
	}
	return byID[id]
}

// Known reports whether a provider name is registered.
func Known(id string) bool { return Get(id) != nil }

// Local reports whether a provider runs on the user's own machine. Unknown
// names are treated as local, so a typo cannot silently be blocked.
func Local(id string) bool {
	s := Get(id)
	return s == nil || s.Local
}

// Pricing returns USD per 1K prompt and completion tokens. Unknown providers
// price at zero rather than guessing.
func Pricing(id string) (prompt, compl float64) {
	s := Get(id)
	if s == nil {
		return 0, 0
	}
	return s.PromptUSD, s.ComplUSD
}

// Free reports providers with no metered spend: local compute is unlimited
// by design, so the tracker records volumes only. An unknown provider is
// reported as metered — under-reporting spend is the worse mistake.
func Free(id string) bool {
	s := Get(id)
	return s != nil && s.PromptUSD == 0 && s.ComplUSD == 0
}

// DefaultModel is the model suggested for a provider.
func DefaultModel(id string) string {
	if s := Get(id); s != nil {
		return s.Recommended
	}
	return ""
}
