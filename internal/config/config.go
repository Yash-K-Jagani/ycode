package config

import (
	"os"
	"path/filepath"

	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/spf13/viper"
)

type Config struct {
	OllamaHost     string `mapstructure:"ollama_host"`
	Theme          string `mapstructure:"theme"`
	ActiveProvider string `mapstructure:"active_provider"`
	ActiveModel    string `mapstructure:"active_model"`
	ZeroDataLeak   bool   `mapstructure:"zero_data_leak"`
	// DailyBudgetUSD is a hard ceiling on what a day's metered turns may cost.
	// Zero means unlimited, which is the default and the only sensible value
	// for a local provider. plan.md promised "budget limits with hard stops"
	// from the start and nothing enforced it until now.
	DailyBudgetUSD float64 `mapstructure:"daily_budget_usd"`
	// Keys holds each provider's API key, keyed by provider id. It is
	// deliberately mapstructure:"-" - a secret must never be written to
	// config.yaml. Populated from the environment or the OS keyring at load
	// time, and from `ycode config set` at runtime.
	//
	// This was three fields, one per provider, which meant a new provider
	// needed a field, an accessor, a line in Load, and a line in every switch
	// that touched credentials. A map needs none of them.
	Keys map[string]string `mapstructure:"-"`
	// The *_key_env fields are overrides for the variable a key is read
	// from. Unlike Keys they are ordinary settings and are persisted.
	GeminiKeyEnv     string `mapstructure:"gemini_key_env"`
	OpenRouterKeyEnv string `mapstructure:"openrouter_key_env"`
	GroqKeyEnv       string `mapstructure:"groq_key_env"`
}

func Defaults() Config {
	return Config{
		OllamaHost:       "http://localhost:11434",
		Theme:            "dark",
		ActiveProvider:   "ollama",
		ActiveModel:      "",
		Keys:             map[string]string{},
		GeminiKeyEnv:     "GEMINI_API_KEY",
		OpenRouterKeyEnv: "OPENROUTER_API_KEY",
		GroqKeyEnv:       "GROQ_API_KEY",
	}
}

func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ycode")
}

func Load() (Config, error) {
	cfg := Defaults()
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(Dir())
	v.AddConfigPath(".ycode")
	v.SetEnvPrefix("YCODE")
	v.AutomaticEnv()
	_ = v.ReadInConfig()
	_ = v.Unmarshal(&cfg)
	// Read every key-bearing provider from the environment, then the keyring.
	// Derived from the registry, so a new provider is picked up without a
	// line here - which is the whole reason the keys are a map.
	if cfg.Keys == nil {
		cfg.Keys = map[string]string{}
	}
	for _, spec := range providers.All() {
		if !spec.NeedsKey {
			continue
		}
		if k := firstNonEmpty(os.Getenv(cfg.KeyEnvFor(spec.ID)), keyringGet(cfg.KeyEnvFor(spec.ID))); k != "" {
			cfg.Keys[spec.ID] = k
		}
	}
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		cfg.OllamaHost = h
	}
	return cfg, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// SetupState describes whether the current config can actually serve a chat
// turn, so the first-run experience can guide the user instead of silently
// failing on the first message.
type SetupState struct {
	// Ready is true when a provider is selected, a model is chosen, and any
	// required API key is present.
	Ready bool
	// Reason is a short human-readable explanation when Ready is false.
	Reason string
	// NeedsKey is true when the active provider is a cloud provider whose
	// API key has not been supplied.
	NeedsKey bool
	// NeedsModel is true when no model has been selected yet.
	NeedsModel bool
	// KeyEnv names the environment variable holding the missing key.
	KeyEnv string
}

// KeyFor returns the configured API key for a provider, or "" if it has none.
// A Config built by hand rather than by Load has a nil map, so this must not
// assume one exists.
func (c Config) KeyFor(provider string) string {
	if c.Keys == nil {
		return ""
	}
	return c.Keys[provider]
}

// SetKeyFor stores an API key against a provider. It reports false for a
// provider that stores no key, so a caller cannot silently drop one. Setting
// an empty value clears the key rather than storing a blank.
func (c *Config) SetKeyFor(provider, key string) bool {
	spec := providers.Get(provider)
	if spec == nil || !spec.NeedsKey {
		return false
	}
	if c.Keys == nil {
		c.Keys = map[string]string{}
	}
	if key == "" {
		delete(c.Keys, provider)
		return true
	}
	c.Keys[provider] = key
	return true
}

// KeyEnvFor returns the environment variable a provider's key is read from,
// honouring a per-install override.
func (c Config) KeyEnvFor(provider string) string {
	switch provider {
	case "gemini":
		return c.GeminiKeyEnv
	case "openrouter":
		return c.OpenRouterKeyEnv
	case "groq":
		return c.GroqKeyEnv
	}
	if s := providers.Get(provider); s != nil {
		return s.KeyEnv
	}
	return ""
}

// SetupNeeded reports whether onboarding is still required for this config.
func SetupNeeded(c Config) SetupState {
	if c.ActiveModel == "" {
		return SetupState{
			Reason:     "no model selected",
			NeedsModel: true,
		}
	}
	spec := providers.Get(c.ActiveProvider)
	if spec == nil {
		// An unrecognised provider cannot be repaired by supplying a key, so
		// leave NeedsKey false and let the caller re-run the provider picker.
		return SetupState{Reason: "unknown provider " + c.ActiveProvider}
	}
	if spec.NeedsKey && c.KeyFor(spec.ID) == "" {
		return SetupState{
			Reason:   spec.Label + " API key not set",
			NeedsKey: true,
			KeyEnv:   c.KeyEnvFor(spec.ID),
		}
	}
	return SetupState{Ready: true}
}

func keyringGet(account string) string {
	if account == "" {
		return ""
	}
	if secret, ok := keys.Get(account); ok {
		return secret
	}
	return ""
}

func (c Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	v := viper.New()
	v.Set("ollama_host", c.OllamaHost)
	v.Set("theme", c.Theme)
	v.Set("active_provider", c.ActiveProvider)
	v.Set("active_model", c.ActiveModel)
	v.Set("zero_data_leak", c.ZeroDataLeak)
	v.Set("daily_budget_usd", c.DailyBudgetUSD)
	v.Set("gemini_key_env", c.GeminiKeyEnv)
	v.Set("openrouter_key_env", c.OpenRouterKeyEnv)
	v.Set("groq_key_env", c.GroqKeyEnv)
	return v.WriteConfigAs(filepath.Join(Dir(), "config.yaml"))
}
