package config

import (
	"os"
	"path/filepath"

	"github.com/Yash-K-Jagani/ycode/internal/keys"
	"github.com/Yash-K-Jagani/ycode/internal/providers"
	"github.com/spf13/viper"
)

type Config struct {
	OllamaHost       string `mapstructure:"ollama_host"`
	Theme            string `mapstructure:"theme"`
	ActiveProvider   string `mapstructure:"active_provider"`
	ActiveModel      string `mapstructure:"active_model"`
	ZeroDataLeak     bool   `mapstructure:"zero_data_leak"`
	GeminiAPIKey     string `mapstructure:"-"`
	OpenRouterKey    string `mapstructure:"-"`
	GroqKey          string `mapstructure:"-"`
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
	cfg.GeminiAPIKey = firstNonEmpty(os.Getenv(cfg.GeminiKeyEnv), keyringGet(cfg.GeminiKeyEnv))
	cfg.OpenRouterKey = firstNonEmpty(os.Getenv(cfg.OpenRouterKeyEnv), keyringGet(cfg.OpenRouterKeyEnv))
	cfg.GroqKey = firstNonEmpty(os.Getenv(cfg.GroqKeyEnv), keyringGet(cfg.GroqKeyEnv))
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

// keyAccessors maps a provider to its stored API key. It is the only place
// that knows which struct field holds a given provider's key, so adding a
// provider means one row here and one in providers.specs. registry_test.go
// asserts the two tables agree.
var keyAccessors = map[string]func(Config) string{
	"gemini":     func(c Config) string { return c.GeminiAPIKey },
	"openrouter": func(c Config) string { return c.OpenRouterKey },
	"groq":       func(c Config) string { return c.GroqKey },
}

// KeyFor returns the configured API key for a provider, or "" if it has none.
func (c Config) KeyFor(provider string) string {
	if f, ok := keyAccessors[provider]; ok {
		return f(c)
	}
	return ""
}

// SetKeyFor stores an API key against a provider. It reports false for a
// provider that stores no key, so a caller cannot silently drop one.
func (c *Config) SetKeyFor(provider, key string) bool {
	if provider == "gemini" {
		c.GeminiAPIKey = key
		return true
	}
	if provider == "openrouter" {
		c.OpenRouterKey = key
		return true
	}
	if provider == "groq" {
		c.GroqKey = key
		return true
	}
	return false
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
	v.Set("gemini_key_env", c.GeminiKeyEnv)
	v.Set("openrouter_key_env", c.OpenRouterKeyEnv)
	v.Set("groq_key_env", c.GroqKeyEnv)
	return v.WriteConfigAs(filepath.Join(Dir(), "config.yaml"))
}
